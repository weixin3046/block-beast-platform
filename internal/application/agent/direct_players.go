package agent

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrPlayerQueryForbidden = errors.New("无权查询该团队")
var ErrPlayerQueryInvalid = errors.New("下级查询参数无效")

type DirectPlayerQuery struct {
	ParentUserID  int64
	Cursor        string
	PlayerType    string
	From, To      time.Time
	Limit, Offset int
}
type DirectPlayerIncome struct {
	Currency    string `json:"currency"`
	AmountMinor int64  `json:"amount_minor"`
}
type DirectPlayer struct {
	HasChildren   bool                 `json:"has_children"`
	Depth         int                  `json:"depth"`
	ParentUserID  int64                `json:"parent_user_id"`
	TodayIncome   []DirectPlayerIncome `json:"today_income"`
	HistoryIncome []DirectPlayerIncome `json:"history_income"`
	AgentLevel    int                  `json:"agent_level"`
	UserID        int64                `json:"user_id"`
	LoginName     string               `json:"login_name"`
	DisplayName   string               `json:"display_name"`
	AvatarURL     string               `json:"avatar_url"`
	IsVirtual     bool                 `json:"is_virtual"`
	CreatedAt     time.Time            `json:"created_at"`
	Income        []DirectPlayerIncome `json:"income"`
}
type DirectPlayers struct {
	ParentUserID int64          `json:"parent_user_id"`
	HasMore      bool           `json:"has_more"`
	NextCursor   string         `json:"next_cursor"`
	Total        int64          `json:"total"`
	Items        []DirectPlayer `json:"items"`
}

func (s *Service) ListDirectPlayers(ctx context.Context, parent string, q DirectPlayerQuery) (DirectPlayers, error) {
	if parent == "" || (q.PlayerType != "" && q.PlayerType != "all" && q.PlayerType != "real" && q.PlayerType != "virtual") || (!q.From.IsZero() && !q.To.IsZero() && !q.To.After(q.From)) {
		return DirectPlayers{}, ErrPlayerQueryInvalid
	}
	var after int64
	if q.ParentUserID < 0 {
		return DirectPlayers{}, ErrPlayerQueryInvalid
	}
	if q.Cursor != "" {
		var e error
		after, e = strconv.ParseInt(q.Cursor, 10, 64)
		if e != nil || after <= 0 || q.Offset != 0 {
			return DirectPlayers{}, ErrPlayerQueryInvalid
		}
	}
	if q.Limit <= 0 || q.Limit > 100 {
		q.Limit = 50
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return DirectPlayers{}, err
	}
	defer tx.Rollback(ctx)
	out := DirectPlayers{Items: make([]DirectPlayer, 0)}
	target := parent
	depth := 1
	if q.ParentUserID > 0 {
		err = tx.QueryRow(ctx, `SELECT id::text FROM users WHERE public_id=$1`, q.ParentUserID).Scan(&target)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, ErrPlayerQueryForbidden
		}
		if err != nil {
			return out, err
		}
		// Walk upwards from the requested node: cost depends on depth, not team size.
		err = tx.QueryRow(ctx, `WITH RECURSIVE ancestors AS (
   SELECT $1::uuid user_id,0 depth,ARRAY[$1::uuid] visited
   UNION ALL SELECT ar.parent_user_id,a.depth+1,a.visited||ar.parent_user_id
   FROM ancestors a JOIN agent_relations ar ON ar.user_id=a.user_id
   WHERE ar.parent_user_id IS NOT NULL AND NOT ar.parent_user_id=ANY(a.visited)
  ) SELECT depth+1 FROM ancestors WHERE user_id=$2`, target, parent).Scan(&depth)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, ErrPlayerQueryForbidden
		}
		if err != nil {
			return out, err
		}
	}
	if err = tx.QueryRow(ctx, `SELECT public_id FROM users WHERE id=$1`, target).Scan(&out.ParentUserID); err != nil {
		return out, err
	}
	const members = ` FROM agent_relations d JOIN users u ON u.id=d.user_id WHERE d.parent_user_id=$1 AND d.user_id<>$1 AND ($2 IN ('','all') OR ($2='real' AND NOT u.is_virtual) OR ($2='virtual' AND u.is_virtual))`
	if err = tx.QueryRow(ctx, `SELECT count(*)`+members, target, q.PlayerType).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT u.id::text,u.public_id,COALESCE(u.login_name,''),u.display_name,COALESCE(u.avatar_url,''),u.is_virtual,u.created_at,COALESCE(u.agent_level,0),
 EXISTS(SELECT 1 FROM agent_relations child WHERE child.parent_user_id=u.id AND child.user_id<>u.id)`+members+` AND u.public_id>$3 ORDER BY u.public_id LIMIT $4 OFFSET $5`, target, q.PlayerType, after, q.Limit+1, q.Offset)
	if err != nil {
		return out, err
	}
	ids := make([]string, 0)
	positions := map[string]int{}
	for rows.Next() {
		var id string
		var item DirectPlayer
		if err = rows.Scan(&id, &item.UserID, &item.LoginName, &item.DisplayName, &item.AvatarURL, &item.IsVirtual, &item.CreatedAt, &item.AgentLevel, &item.HasChildren); err != nil {
			rows.Close()
			return out, err
		}
		if len(out.Items) == q.Limit {
			out.HasMore = true
			break
		}
		item.Depth = depth
		item.ParentUserID = out.ParentUserID
		positions[id] = len(out.Items)
		ids = append(ids, id)
		item.Income = make([]DirectPlayerIncome, 0)
		item.TodayIncome = make([]DirectPlayerIncome, 0)
		item.HistoryIncome = make([]DirectPlayerIncome, 0)
		out.Items = append(out.Items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(ids) > 0 {
		var from, to any
		if !q.From.IsZero() {
			from = q.From
		}
		if !q.To.IsZero() {
			to = q.To
		}
		today := incomePeriods(time.Now())[0]
		rows, err = tx.Query(ctx, `SELECT b.user_id::text,c.currency,
 sum(c.amount_minor) FILTER (WHERE ($3::timestamptz IS NULL OR c.created_at >= $3) AND ($4::timestamptz IS NULL OR c.created_at < $4)),
 sum(c.amount_minor) FILTER (WHERE c.created_at >= $5 AND c.created_at < $6),
 sum(c.amount_minor)
 FROM commission_entries c JOIN bets b ON b.id=c.source_bet_id WHERE c.beneficiary_user_id=$1 AND b.user_id=ANY($2::uuid[]) AND c.status='paid' GROUP BY b.user_id,c.currency ORDER BY c.currency`, parent, ids, from, to, today.From, today.To)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var id string
			var income DirectPlayerIncome
			var filtered, daily *int64
			if err = rows.Scan(&id, &income.Currency, &filtered, &daily, &income.AmountMinor); err != nil {
				rows.Close()
				return out, err
			}
			i := positions[id]
			out.Items[i].HistoryIncome = append(out.Items[i].HistoryIncome, income)
			if filtered != nil {
				out.Items[i].Income = append(out.Items[i].Income, DirectPlayerIncome{income.Currency, *filtered})
			}
			if daily != nil {
				out.Items[i].TodayIncome = append(out.Items[i].TodayIncome, DirectPlayerIncome{income.Currency, *daily})
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
	}
	if out.HasMore {
		out.NextCursor = strconv.FormatInt(out.Items[len(out.Items)-1].UserID, 10)
	}
	return out, tx.Commit(ctx)
}
