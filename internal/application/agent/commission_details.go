package agent

import (
	"context"
	"encoding/json"
	"time"
)

type CommissionDetail struct {
	Commission
	SourceUserID      int64           `json:"source_user_id"`
	SourceLoginName   string          `json:"source_login_name"`
	SourceDisplayName string          `json:"source_display_name"`
	GameType          string          `json:"game_type"`
	GameName          string          `json:"game_name"`
	Sequence          int64           `json:"sequence"`
	CreatedAt         *time.Time      `json:"created_at"`
	PlayMode          string          `json:"play_mode"`
	Selection         json.RawMessage `json:"selection"`
}

func (s *Service) ListCommissionDetails(ctx context.Context, owner, currency string, from, to time.Time, limit int) ([]CommissionDetail, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	var start, end any
	if !from.IsZero() {
		start = from
	}
	if !to.IsZero() {
		end = to
	}
	rows, err := s.pool.Query(ctx, `SELECT c.id::text,c.source_bet_id::text,c.beneficiary_user_id::text,c.currency,c.amount_minor,c.status,g.code,g.name,r.sequence,c.created_at,COALESCE(b.play_mode,''),b.selection,src.public_id,COALESCE(src.login_name,''),src.display_name FROM commission_entries c JOIN bets b ON b.id=c.source_bet_id JOIN users src ON src.id=b.user_id JOIN rounds r ON r.id=b.round_id JOIN game_types g ON g.id=r.game_type_id WHERE c.beneficiary_user_id=$1 AND ($2='' OR c.currency=$2) AND ($3::timestamptz IS NULL OR c.created_at >= $3) AND ($4::timestamptz IS NULL OR c.created_at < $4) ORDER BY c.created_at DESC NULLS LAST,c.id DESC LIMIT $5`, owner, currency, start, end, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CommissionDetail{}
	for rows.Next() {
		var v CommissionDetail
		if err = rows.Scan(&v.ID, &v.BetID, &v.AgentID, &v.Currency, &v.AmountMinor, &v.Status, &v.GameType, &v.GameName, &v.Sequence, &v.CreatedAt, &v.PlayMode, &v.Selection, &v.SourceUserID, &v.SourceLoginName, &v.SourceDisplayName); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}
