package agent

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type DirectPlayerQuery struct {
	PlayerType    string
	From, To      time.Time
	Limit, Offset int
}
type DirectPlayerIncome struct {
	Currency    string `json:"currency"`
	AmountMinor int64  `json:"amount_minor"`
}
type DirectPlayer struct {
	AgentLevel  int                  `json:"agent_level"`
	UserID      int64                `json:"user_id"`
	LoginName   string               `json:"login_name"`
	DisplayName string               `json:"display_name"`
	AvatarURL   string               `json:"avatar_url"`
	IsVirtual   bool                 `json:"is_virtual"`
	CreatedAt   time.Time            `json:"created_at"`
	Income      []DirectPlayerIncome `json:"income"`
}
type DirectPlayers struct {
	Total int64          `json:"total"`
	Items []DirectPlayer `json:"items"`
}

func (s *Service) ListDirectPlayers(ctx context.Context, parent string, q DirectPlayerQuery) (DirectPlayers, error) {
	if parent == "" || (q.PlayerType != "" && q.PlayerType != "all" && q.PlayerType != "real" && q.PlayerType != "virtual") || (!q.From.IsZero() && !q.To.IsZero() && !q.To.After(q.From)) {
		return DirectPlayers{}, errors.New("invalid direct player query")
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
	const members = ` FROM agent_relations ar JOIN users u ON u.id=ar.user_id WHERE ar.parent_user_id=$1 AND ($2 IN ('','all') OR ($2='real' AND NOT u.is_virtual) OR ($2='virtual' AND u.is_virtual))`
	if err = tx.QueryRow(ctx, `SELECT count(*)`+members, parent, q.PlayerType).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT u.id::text,u.public_id,COALESCE(u.login_name,''),u.display_name,COALESCE(u.avatar_url,''),u.is_virtual,u.created_at,COALESCE(u.agent_level,0)`+members+` ORDER BY u.public_id LIMIT $3 OFFSET $4`, parent, q.PlayerType, q.Limit, q.Offset)
	if err != nil {
		return out, err
	}
	ids := make([]string, 0)
	positions := map[string]int{}
	for rows.Next() {
		var id string
		var item DirectPlayer
		if err = rows.Scan(&id, &item.UserID, &item.LoginName, &item.DisplayName, &item.AvatarURL, &item.IsVirtual, &item.CreatedAt, &item.AgentLevel); err != nil {
			rows.Close()
			return out, err
		}
		positions[id] = len(out.Items)
		ids = append(ids, id)
		item.Income = make([]DirectPlayerIncome, 0)
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
		rows, err = tx.Query(ctx, `SELECT b.user_id::text,c.currency,sum(c.amount_minor) FROM commission_entries c JOIN bets b ON b.id=c.source_bet_id WHERE c.beneficiary_user_id=$1 AND b.user_id=ANY($2::uuid[]) AND c.status='paid' AND ($3::timestamptz IS NULL OR c.created_at >= $3) AND ($4::timestamptz IS NULL OR c.created_at < $4) GROUP BY b.user_id,c.currency ORDER BY c.currency`, parent, ids, from, to)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var id string
			var income DirectPlayerIncome
			if err = rows.Scan(&id, &income.Currency, &income.AmountMinor); err != nil {
				rows.Close()
				return out, err
			}
			i := positions[id]
			out.Items[i].Income = append(out.Items[i].Income, income)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
	}
	return out, tx.Commit(ctx)
}
