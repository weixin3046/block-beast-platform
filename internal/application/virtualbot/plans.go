package virtualbot

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"math"
	"strings"
	"time"
)

var ErrInvalidPlan = errors.New("机器人计划参数无效")
var ErrPlanNotFound = errors.New("机器人计划不存在")
var ErrPlanConflict = errors.New("请求编号已用于不同机器人计划")
var ErrForbidden = errors.New("无权管理机器人计划")

type Selection struct {
	PlayMode string `json:"play_mode"`
	Pick     string `json:"pick"`
}
type PlanInput struct {
	UserID        int64       `json:"user_id"`
	Enabled       bool        `json:"enabled"`
	GameType      string      `json:"game_type"`
	GameRoomID    string      `json:"game_room_id"`
	Currency      string      `json:"currency"`
	MinStakeMinor int64       `json:"min_stake_minor"`
	MaxStakeMinor int64       `json:"max_stake_minor"`
	Selections    []Selection `json:"selections"`
	SkipMin       int         `json:"skip_min"`
	SkipMax       int         `json:"skip_max"`
}
type Plan struct {
	PlanInput
	ID                string     `json:"id"`
	LoginName         string     `json:"login_name"`
	DisplayName       string     `json:"display_name"`
	IsVirtual         bool       `json:"is_virtual"`
	UserStatus        string     `json:"user_status"`
	NextRoundSequence *int64     `json:"next_round_sequence"`
	LastSeenSequence  *int64     `json:"last_seen_sequence"`
	LastCheckedAt     *time.Time `json:"last_checked_at"`
	LastStatus        string     `json:"last_status"`
	LastError         string     `json:"last_error"`
	LastBetID         *string    `json:"last_bet_id"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}
type PlanPage struct {
	Items []Plan `json:"items"`
	Total int64  `json:"total"`
}

func validatePlan(in PlanInput) error {
	if _, e := uuid.Parse(in.GameRoomID); e != nil {
		return ErrInvalidPlan
	}
	if in.UserID < 100000 || in.Currency == "" || in.MinStakeMinor <= 0 || in.MaxStakeMinor < in.MinStakeMinor || in.MaxStakeMinor > math.MaxInt64/100000 {
		return ErrInvalidPlan
	}
	if in.SkipMin < 1 || in.SkipMax < in.SkipMin || in.SkipMax > 10000 || len(in.Selections) == 0 || len(in.Selections) > 24 {
		return ErrInvalidPlan
	}
	switch in.GameType {
	case "hash_9", "hash_13", "hash_17", "hash_19", "hash_23", "hash_29":
	default:
		return ErrInvalidPlan
	}
	seen := map[Selection]bool{}
	for _, v := range in.Selections {
		if seen[v] {
			return ErrInvalidPlan
		}
		seen[v] = true
		switch v.PlayMode {
		case "road":
			if v.Pick != "odd" && v.Pick != "even" && v.Pick != "big" && v.Pick != "small" {
				return ErrInvalidPlan
			}
		case "guess", "dodge":
			if len(v.Pick) != 1 || v.Pick[0] < '0' || v.Pick[0] > '9' {
				return ErrInvalidPlan
			}
		default:
			return ErrInvalidPlan
		}
	}
	return nil
}

func authorize(ctx context.Context, tx pgx.Tx, actor string) error {
	var ok bool
	e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE u.id=$1 AND u.status='active' AND r.code IN ('admin','operator'))`, actor).Scan(&ok)
	if e != nil {
		return e
	}
	if !ok {
		return ErrForbidden
	}
	return nil
}
func auditPlan(ctx context.Context, tx pgx.Tx, actor, action, id string) error {
	_, e := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,target_type,target_id,payload) VALUES(gen_random_uuid(),$1,$2,'robot_plan',$3,'{}')`, actor, action, id)
	return e
}
func validatePlanDB(ctx context.Context, tx pgx.Tx, in PlanInput) (string, error) {
	if e := validatePlan(in); e != nil {
		return "", e
	}
	var user string
	e := tx.QueryRow(ctx, `SELECT id::text FROM users WHERE public_id=$1 AND is_virtual`, in.UserID).Scan(&user)
	if errors.Is(e, pgx.ErrNoRows) {
		return "", ErrInvalidPlan
	}
	if e != nil {
		return "", e
	}
	for _, pick := range in.Selections {
		var ok bool
		e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM game_rooms r
   JOIN game_room_types rt ON rt.room_id=r.id JOIN game_types gt ON gt.id=rt.game_type_id
   JOIN hash_room_currency_configs c ON c.room_id=r.id AND c.currency=$3
   JOIN currencies cur ON cur.code=c.currency AND cur.enabled
   WHERE r.id=$1 AND r.enabled AND r.game_kind='hash' AND gt.code=$2 AND gt.enabled
   AND $4>=c.min_stake_minor AND $5<=CASE $6 WHEN 'guess' THEN c.guess_max_stake_minor WHEN 'dodge' THEN c.dodge_max_stake_minor ELSE c.road_max_stake_minor END)`,
			in.GameRoomID, in.GameType, in.Currency, in.MinStakeMinor, in.MaxStakeMinor, pick.PlayMode).Scan(&ok)
		if e != nil {
			return "", e
		}
		if !ok {
			return "", ErrInvalidPlan
		}
	}
	return user, nil
}

const planSelect = `SELECT p.id::text,u.public_id,p.enabled,p.game_type,p.game_room_id::text,p.currency,p.min_stake_minor,p.max_stake_minor,p.selections,p.skip_min,p.skip_max,
 COALESCE(u.login_name,''),u.display_name,u.is_virtual,u.status,p.next_round_sequence,p.last_seen_sequence,p.last_checked_at,p.last_status,p.last_error,p.last_bet_id::text,p.created_at,p.updated_at
 FROM robot_plans p JOIN users u ON u.id=p.user_id `

type scanner interface{ Scan(...any) error }

func scanPlan(row scanner) (Plan, error) {
	var v Plan
	var picks []byte
	e := row.Scan(&v.ID, &v.UserID, &v.Enabled, &v.GameType, &v.GameRoomID, &v.Currency, &v.MinStakeMinor, &v.MaxStakeMinor, &picks, &v.SkipMin, &v.SkipMax,
		&v.LoginName, &v.DisplayName, &v.IsVirtual, &v.UserStatus, &v.NextRoundSequence, &v.LastSeenSequence, &v.LastCheckedAt, &v.LastStatus, &v.LastError, &v.LastBetID, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return v, ErrPlanNotFound
	}
	if e != nil {
		return v, e
	}
	e = json.Unmarshal(picks, &v.Selections)
	return v, e
}
func (s *Service) GetPlan(ctx context.Context, id string) (Plan, error) {
	if _, e := uuid.Parse(id); e != nil {
		return Plan{}, ErrPlanNotFound
	}
	return scanPlan(s.pool.QueryRow(ctx, planSelect+" WHERE p.id=$1 AND NOT p.deleted", id))
}
func (s *Service) ListPlans(ctx context.Context, q string, limit, offset int) (PlanPage, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	out := PlanPage{Items: []Plan{}}
	tx, e := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	filter := ` WHERE NOT p.deleted AND ($1='' OR u.public_id::text=$1 OR u.login_name ILIKE '%'||$1||'%' OR u.display_name ILIKE '%'||$1||'%')`
	if e = tx.QueryRow(ctx, "SELECT count(*) FROM robot_plans p JOIN users u ON u.id=p.user_id"+filter, q).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := tx.Query(ctx, planSelect+filter+" ORDER BY p.created_at DESC,p.id DESC LIMIT $2 OFFSET $3", q, limit, offset)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		v, e := scanPlan(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	return out, tx.Commit(ctx)
}

func (s *Service) CreatePlan(ctx context.Context, actor, key string, in PlanInput) (Plan, error) {
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if len(key) == 0 || len(key) > 128 {
		return Plan{}, ErrInvalidPlan
	}
	if e := validatePlan(in); e != nil {
		return Plan{}, e
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return Plan{}, e
	}
	defer tx.Rollback(ctx)
	if e = authorize(ctx, tx, actor); e != nil {
		return Plan{}, e
	}
	// Serialize replays of this actor's creation requests; only this short transaction holds the lock.
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,731))`, actor+":"+key); e != nil {
		return Plan{}, e
	}
	payload, _ := json.Marshal(in)
	var old, id string
	var deleted bool
	e = tx.QueryRow(ctx, `SELECT id::text,create_input::text,deleted FROM robot_plans WHERE operator_id=$1 AND request_id=$2`, actor, key).Scan(&id, &old, &deleted)
	if e == nil {
		var prior PlanInput
		if json.Unmarshal([]byte(old), &prior) != nil {
			return Plan{}, ErrPlanConflict
		}
		canonical, _ := json.Marshal(prior)
		if string(canonical) != string(payload) || deleted {
			return Plan{}, ErrPlanConflict
		}
		v, e := scanPlan(tx.QueryRow(ctx, planSelect+" WHERE p.id=$1", id))
		if e != nil {
			return v, e
		}
		return v, tx.Commit(ctx)
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return Plan{}, e
	}
	user, e := validatePlanDB(ctx, tx, in)
	if e != nil {
		return Plan{}, e
	}
	picks, _ := json.Marshal(in.Selections)
	id = uuid.NewString()
	_, e = tx.Exec(ctx, `INSERT INTO robot_plans(id,user_id,operator_id,request_id,create_input,enabled,game_type,game_room_id,currency,min_stake_minor,max_stake_minor,selections,skip_min,skip_max)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, id, user, actor, key, payload, in.Enabled, in.GameType, in.GameRoomID, in.Currency, in.MinStakeMinor, in.MaxStakeMinor, picks, in.SkipMin, in.SkipMax)
	if e != nil {
		return Plan{}, e
	}
	if e = auditPlan(ctx, tx, actor, "robot_plan.create", id); e != nil {
		return Plan{}, e
	}
	v, e := scanPlan(tx.QueryRow(ctx, planSelect+" WHERE p.id=$1", id))
	if e != nil {
		return v, e
	}
	return v, tx.Commit(ctx)
}

func (s *Service) UpdatePlan(ctx context.Context, actor, id string, in PlanInput) (Plan, error) {
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if _, e := uuid.Parse(id); e != nil {
		return Plan{}, ErrPlanNotFound
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return Plan{}, e
	}
	defer tx.Rollback(ctx)
	if e = authorize(ctx, tx, actor); e != nil {
		return Plan{}, e
	}
	old, e := scanPlan(tx.QueryRow(ctx, planSelect+" WHERE p.id=$1 AND NOT p.deleted FOR UPDATE OF p", id))
	if e != nil {
		return Plan{}, e
	}
	if in.UserID != old.UserID {
		return Plan{}, ErrInvalidPlan
	}
	if _, e = validatePlanDB(ctx, tx, in); e != nil {
		return Plan{}, e
	}
	picks, _ := json.Marshal(in.Selections)
	_, e = tx.Exec(ctx, `UPDATE robot_plans SET enabled=$2,game_type=$3,game_room_id=$4,currency=$5,min_stake_minor=$6,max_stake_minor=$7,selections=$8,skip_min=$9,skip_max=$10,
 next_round_sequence=NULL,last_seen_sequence=NULL,last_checked_at=NULL,last_status='ready',last_error='',updated_at=now() WHERE id=$1`,
		id, in.Enabled, in.GameType, in.GameRoomID, in.Currency, in.MinStakeMinor, in.MaxStakeMinor, picks, in.SkipMin, in.SkipMax)
	if e != nil {
		return Plan{}, e
	}
	if e = auditPlan(ctx, tx, actor, "robot_plan.update", id); e != nil {
		return Plan{}, e
	}
	v, e := scanPlan(tx.QueryRow(ctx, planSelect+" WHERE p.id=$1", id))
	if e != nil {
		return v, e
	}
	return v, tx.Commit(ctx)
}

func (s *Service) SetPlanEnabled(ctx context.Context, actor, id string, enabled bool) (Plan, error) {
	if _, e := uuid.Parse(id); e != nil {
		return Plan{}, ErrPlanNotFound
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return Plan{}, e
	}
	defer tx.Rollback(ctx)
	if e = authorize(ctx, tx, actor); e != nil {
		return Plan{}, e
	}
	old, e := scanPlan(tx.QueryRow(ctx, planSelect+" WHERE p.id=$1 AND NOT p.deleted FOR UPDATE OF p", id))
	if e != nil {
		return Plan{}, e
	}
	if enabled {
		if _, e = validatePlanDB(ctx, tx, old.PlanInput); e != nil {
			return Plan{}, e
		}
	}
	if old.Enabled != enabled {
		_, e = tx.Exec(ctx, `UPDATE robot_plans SET enabled=$2,next_round_sequence=NULL,last_seen_sequence=NULL,last_checked_at=NULL,last_status=CASE WHEN $2 THEN 'ready' ELSE 'stopped' END,last_error='',updated_at=now() WHERE id=$1`, id, enabled)
		if e != nil {
			return Plan{}, e
		}
		if e = auditPlan(ctx, tx, actor, "robot_plan.enabled", id); e != nil {
			return Plan{}, e
		}
	}
	v, e := scanPlan(tx.QueryRow(ctx, planSelect+" WHERE p.id=$1", id))
	if e != nil {
		return v, e
	}
	return v, tx.Commit(ctx)
}
func (s *Service) DeletePlan(ctx context.Context, actor, id string) error {
	if _, e := uuid.Parse(id); e != nil {
		return ErrPlanNotFound
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = authorize(ctx, tx, actor); e != nil {
		return e
	}
	tag, e := tx.Exec(ctx, `UPDATE robot_plans SET enabled=false,deleted=true,last_status='deleted',updated_at=now() WHERE id=$1 AND NOT deleted`, id)
	if e != nil {
		return e
	}
	if tag.RowsAffected() > 0 {
		if e = auditPlan(ctx, tx, actor, "robot_plan.delete", id); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
