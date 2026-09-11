package rebate

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrForbidden = errors.New("无权管理返水配置")
	ErrConflict  = errors.New("返水配置版本已变化，请刷新后重试")
	ErrNotFound  = errors.New("返水配置不存在")
)

type Service struct{ pool *pgxpool.Pool }

func NewService(p *pgxpool.Pool) *Service { return &Service{pool: p} }

type Level struct {
	Level        int `json:"level"`
	RatePerMille int `json:"rate_per_mille"`
}
type Config struct {
	ID        string    `json:"id"`
	RoomID    string    `json:"game_room_id"`
	RoomName  string    `json:"game_room_name"`
	Enabled   bool      `json:"enabled"`
	Version   int64     `json:"version"`
	Levels    []Level   `json:"levels"`
	UpdatedAt time.Time `json:"updated_at"`
}
type ConfigQuery struct{ RoomID string }
type ConfigUpdate struct {
	Version int64   `json:"version"`
	Enabled bool    `json:"enabled"`
	Levels  []Level `json:"levels"`
}

func validateLevels(levels []Level) error {
	if len(levels) != 6 {
		return ErrInvalid
	}
	rates := make([]int, 6)
	seen := make([]bool, 6)
	for _, l := range levels {
		if l.Level < 1 || l.Level > 6 || l.RatePerMille < 0 || l.RatePerMille > 1000 || seen[l.Level-1] {
			return ErrInvalid
		}
		seen[l.Level-1] = true
		rates[l.Level-1] = l.RatePerMille
	}
	for i := 1; i < 6; i++ {
		if rates[i] < rates[i-1] {
			return ErrInvalid
		}
	}
	return nil
}

const configSelect = `SELECT c.id::text,c.room_id::text,gr.name,c.enabled,c.version,c.rates,c.updated_at FROM room_rebate_configs c JOIN game_rooms gr ON gr.id=c.room_id`

func scanConfig(row pgx.Row) (Config, error) {
	var c Config
	var rates []int
	e := row.Scan(&c.ID, &c.RoomID, &c.RoomName, &c.Enabled, &c.Version, &rates, &c.UpdatedAt)
	for i, r := range rates {
		c.Levels = append(c.Levels, Level{i + 1, r})
	}
	return c, e
}
func (s *Service) ListConfigs(ctx context.Context, q ConfigQuery) ([]Config, error) {
	if q.RoomID != "" {
		if _, e := uuid.Parse(q.RoomID); e != nil {
			return nil, ErrInvalid
		}
	}
	rows, e := s.pool.Query(ctx, configSelect+` WHERE ($1='' OR c.room_id::text=$1) ORDER BY gr.sort_order`, q.RoomID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Config{}
	for rows.Next() {
		c, e := scanConfig(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Service) UpdateConfig(ctx context.Context, actor, id string, in ConfigUpdate) (Config, error) {
	if _, e := uuid.Parse(actor); e != nil {
		return Config{}, ErrForbidden
	}
	if _, e := uuid.Parse(id); e != nil {
		return Config{}, ErrNotFound
	}
	if in.Version < 1 || in.Version == math.MaxInt64 || validateLevels(in.Levels) != nil {
		return Config{}, ErrInvalid
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return Config{}, e
	}
	defer tx.Rollback(ctx)
	var allowed bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE u.id=$1 AND u.status='active' AND r.code IN ('admin','operator'))`, actor).Scan(&allowed); e != nil {
		return Config{}, e
	}
	if !allowed {
		return Config{}, ErrForbidden
	}
	old, e := scanConfig(tx.QueryRow(ctx, configSelect+` WHERE c.id=$1 FOR UPDATE OF c`, id))
	if errors.Is(e, pgx.ErrNoRows) {
		return Config{}, ErrNotFound
	}
	if e != nil {
		return Config{}, e
	}
	if old.Version != in.Version {
		return Config{}, ErrConflict
	}
	rates := make([]int, 6)
	for _, l := range in.Levels {
		rates[l.Level-1] = l.RatePerMille
	}
	if _, e = tx.Exec(ctx, `UPDATE room_rebate_configs SET enabled=$2,rates=$3,version=version+1,updated_at=now() WHERE id=$1`, id, in.Enabled, rates); e != nil {
		return Config{}, e
	}
	result, e := scanConfig(tx.QueryRow(ctx, configSelect+` WHERE c.id=$1`, id))
	if e != nil {
		return Config{}, e
	}
	payload, e := json.Marshal(map[string]any{"before": old, "after": result})
	if e != nil {
		return Config{}, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,target_type,target_id,payload) VALUES($1,$2,'rebate.config.update','rebate_config',$3,$4)`, uuid.NewString(), actor, id, payload); e != nil {
		return Config{}, e
	}
	return result, tx.Commit(ctx)
}
