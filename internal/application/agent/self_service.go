package agent

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrChildLevelForbidden = errors.New("无权设置该下级的代理等级")
var ErrChildLevelInvalid = errors.New("代理等级必须为0至6")

type IncomePeriod struct {
	Period string               `json:"period"`
	From   time.Time            `json:"from"`
	To     time.Time            `json:"to"`
	Income []DirectPlayerIncome `json:"income"`
}
type IncomeSummary struct {
	Timezone string         `json:"timezone"`
	AsOf     time.Time      `json:"as_of"`
	Items    []IncomePeriod `json:"items"`
}

func incomePeriods(now time.Time) []IncomePeriod {
	local := now.In(time.FixedZone("Asia/Shanghai", 8*3600))
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	week := day.AddDate(0, 0, -(int(day.Weekday())+6)%7)
	return []IncomePeriod{
		{"today", day, day.AddDate(0, 0, 1), []DirectPlayerIncome{}},
		{"yesterday", day.AddDate(0, 0, -1), day, []DirectPlayerIncome{}},
		{"this_week", week, week.AddDate(0, 0, 7), []DirectPlayerIncome{}},
		{"last_week", week.AddDate(0, 0, -7), week, []DirectPlayerIncome{}},
	}
}

func (s *Service) IncomeSummary(ctx context.Context, owner string) (IncomeSummary, error) {
	out := IncomeSummary{Timezone: "Asia/Shanghai", AsOf: time.Now().UTC()}
	out.Items = incomePeriods(out.AsOf)
	// One statement gives all periods the same database snapshot.
	rows, err := s.pool.Query(ctx, `WITH periods(n,start_at,end_at) AS (VALUES (0,$2::timestamptz,$3::timestamptz),(1,$4::timestamptz,$2::timestamptz),(2,$5::timestamptz,$6::timestamptz),(3,$7::timestamptz,$5::timestamptz)) SELECT p.n,c.currency,sum(c.amount_minor) FROM periods p JOIN commission_entries c ON c.created_at>=p.start_at AND c.created_at<p.end_at WHERE c.beneficiary_user_id=$1 AND c.status='paid' GROUP BY p.n,c.currency ORDER BY p.n,c.currency`, owner, out.Items[0].From, out.Items[0].To, out.Items[1].From, out.Items[2].From, out.Items[2].To, out.Items[3].From)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var n int
		var v DirectPlayerIncome
		if err = rows.Scan(&n, &v.Currency, &v.AmountMinor); err != nil {
			return out, err
		}
		out.Items[n].Income = append(out.Items[n].Income, v)
	}
	return out, rows.Err()
}

func (s *Service) SetDirectPlayerLevel(ctx context.Context, owner string, target int64, level int) error {
	if level < 0 || level > 6 {
		return ErrChildLevelInvalid
	}
	if owner == "" || target < 10001 {
		return ErrChildLevelForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Serialize with relation creation and lock both users in stable UUID order.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('agent-relation-graph',0))`); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id::text,public_id,COALESCE(agent_level,0),is_virtual,status FROM users WHERE id=$1 OR public_id=$2 ORDER BY id FOR UPDATE`, owner, target)
	if err != nil {
		return err
	}
	var ownerLevel, childLevel int
	var child string
	var ownerOK, childOK bool
	for rows.Next() {
		var id, status string
		var public int64
		var l int
		var virtual bool
		if err = rows.Scan(&id, &public, &l, &virtual, &status); err != nil {
			rows.Close()
			return err
		}
		if id == owner {
			ownerLevel = l
			ownerOK = !virtual && status == "active"
		}
		if public == target {
			child = id
			childLevel = l
			childOK = !virtual && status != "disabled"
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !ownerOK || !childOK || child == owner || ownerLevel < 1 || level >= ownerLevel || childLevel >= ownerLevel {
		return ErrChildLevelForbidden
	}
	var parent string
	if err = tx.QueryRow(ctx, `SELECT parent_user_id::text FROM agent_relations WHERE user_id=$1`, child).Scan(&parent); errors.Is(err, pgx.ErrNoRows) {
		return ErrChildLevelForbidden
	} else if err != nil {
		return err
	}
	if parent != owner {
		return ErrChildLevelForbidden
	}
	if childLevel == level {
		return tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET agent_level=NULLIF($2,0),updated_at=now() WHERE id=$1`, child, level); err != nil {
		return err
	}
	if level > 0 {
		if _, err = tx.Exec(ctx, `INSERT INTO agent_commission_rates(agent_user_id,rate_basis_points) VALUES($1,0) ON CONFLICT(agent_user_id) DO NOTHING`, child); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,target_type,target_id,payload) VALUES($1,$2,'agent.direct_player.level.update','user',$3,jsonb_build_object('old_level',$4::int,'agent_level',$5::int))`, uuid.NewString(), owner, child, childLevel, level); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
