package operations

import (
	"context"
	"time"
)

// FundStatistic 按币种统计，禁止把不同精度/币种的资金直接相加。
type FundStatistic struct {
	Currency       string `json:"currency"`
	CreditMinor    int64  `json:"credit_minor"`
	ClearanceMinor int64  `json:"clearance_minor"`
	GiftMinor      int64  `json:"gift_minor"`
	PenaltyMinor   int64  `json:"penalty_minor"`
}

func (s *Service) dashboardFunds(ctx context.Context, result *Dashboard, from, to time.Time) error {
	ids := make([]int64, 0, len(result.Players))
	players := map[int64]int{}
	for i := range result.Players {
		result.Players[i].Funds = []FundStatistic{}
		ids = append(ids, result.Players[i].UserID)
		players[result.Players[i].UserID] = i
	}
	globals := map[string]int{}
	for i := range result.Global {
		globals[result.Global[i].Currency] = i
	}
	rows, err := s.pool.Query(ctx, `
 SELECT COALESCE(u.public_id,0),w.currency,
 COALESCE(sum(le.amount_minor) FILTER(WHERE le.business_type='admin_credit'),0),
 COALESCE(-sum(le.amount_minor) FILTER(WHERE le.business_type IN ('admin_debit','point_withdrawal_debit') OR le.entry_type='withdrawal_debit'),0),
 COALESCE(sum(le.amount_minor) FILTER(WHERE le.business_type='admin_reward'),0),
 COALESCE(-sum(le.amount_minor) FILTER(WHERE le.business_type='admin_penalty'),0)
 FROM ledger_entries le JOIN wallets w ON w.id=le.wallet_id JOIN users u ON u.id=w.user_id
 WHERE NOT u.is_virtual AND le.occurred_at BETWEEN $1 AND $2
 AND (le.business_type IN ('admin_credit','admin_debit','admin_reward','admin_penalty','point_withdrawal_debit') OR le.entry_type='withdrawal_debit')
 GROUP BY GROUPING SETS ((u.public_id,w.currency),(w.currency))
 HAVING GROUPING(u.public_id)=1 OR u.public_id=ANY($3::bigint[])
 ORDER BY w.currency`, from, to, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var user int64
		var v FundStatistic
		if err = rows.Scan(&user, &v.Currency, &v.CreditMinor, &v.ClearanceMinor, &v.GiftMinor, &v.PenaltyMinor); err != nil {
			return err
		}
		if user == 0 {
			if i, ok := globals[v.Currency]; ok {
				result.Global[i].ClearanceMinor = v.ClearanceMinor
				result.Global[i].GiftMinor = v.GiftMinor
				result.Global[i].PenaltyMinor = v.PenaltyMinor
			}
		} else if i, ok := players[user]; ok {
			result.Players[i].Funds = append(result.Players[i].Funds, v)
		}
	}
	return rows.Err()
}
