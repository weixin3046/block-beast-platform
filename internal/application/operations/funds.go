package operations

import (
	"context"
	"github.com/block-beast/platform/internal/domain/wallet"
	"time"
)

type FundStatistic = CurrencyStatistic

func formatCurrencyStatistic(v *CurrencyStatistic) error {
	var err error
	for _, pair := range []struct {
		minor int64
		out   *string
	}{
		{v.StakeMinor, &v.Stake}, {v.PayoutMinor, &v.Payout}, {v.DepositMinor, &v.Deposit}, {v.CreditMinor, &v.Credit}, {v.ClearanceMinor, &v.Clearance}, {v.GiftMinor, &v.Gift}, {v.PenaltyMinor, &v.Penalty}, {v.BalanceMinor, &v.Balance},
	} {
		*pair.out, err = wallet.FormatDisplayAmount(pair.minor, v.Decimals)
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) dashboardFunds(ctx context.Context, result *Dashboard, from, to time.Time) error {
	ids := []int64{}
	players := map[int64]int{}
	for i := range result.Players {
		result.Players[i].Funds = []FundStatistic{}
		ids = append(ids, result.Players[i].UserID)
		players[result.Players[i].UserID] = i
	}
	result.Global = []CurrencyStatistic{}
	rows, err := s.pool.Query(ctx, `
 WITH b AS(SELECT wallet_id,count(*) AS n,sum(stake_minor) AS stake,sum(payout_minor) AS payout FROM bets WHERE created_at >= $1 AND created_at < $2 GROUP BY wallet_id),
 l AS(SELECT wallet_id,
 sum(amount_minor) FILTER(WHERE business_type IN ('deposit','lulu_deposit')) AS deposit,
 sum(amount_minor) FILTER(WHERE business_type='admin_credit') AS credit,
 -sum(amount_minor) FILTER(WHERE business_type IN ('admin_debit','point_withdrawal_debit','lulu_withdrawal_debit') OR entry_type='withdrawal_debit') AS clearance,
 sum(amount_minor) FILTER(WHERE business_type='admin_reward') AS gift,
 -sum(amount_minor) FILTER(WHERE business_type='admin_penalty') AS penalty
 FROM ledger_entries WHERE occurred_at >= $1 AND occurred_at < $2 GROUP BY wallet_id)
 SELECT COALESCE(u.public_id,0),w.currency,c.decimals,
 COALESCE(sum(b.n),0),COALESCE(sum(b.stake),0),COALESCE(sum(b.payout),0),
 COALESCE(sum(l.deposit),0),COALESCE(sum(l.credit),0),COALESCE(sum(l.clearance),0),COALESCE(sum(l.gift),0),COALESCE(sum(l.penalty),0),
 sum(w.available_minor::numeric+w.frozen_minor)
 FROM wallets w JOIN users u ON u.id=w.user_id JOIN currencies c ON c.code=w.currency LEFT JOIN b ON b.wallet_id=w.id LEFT JOIN l ON l.wallet_id=w.id
 WHERE NOT u.is_virtual
 GROUP BY GROUPING SETS((u.public_id,w.currency,c.decimals),(w.currency,c.decimals))
 HAVING GROUPING(u.public_id)=1 OR u.public_id=ANY($3::bigint[])
 ORDER BY w.currency,COALESCE(u.public_id,0)`, from, to, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var v CurrencyStatistic
		if err = rows.Scan(&id, &v.Currency, &v.Decimals, &v.BetCount, &v.StakeMinor, &v.PayoutMinor, &v.DepositMinor, &v.CreditMinor, &v.ClearanceMinor, &v.GiftMinor, &v.PenaltyMinor, &v.BalanceMinor); err != nil {
			return err
		}
		if err = formatCurrencyStatistic(&v); err != nil {
			return err
		}
		if id == 0 {
			result.Global = append(result.Global, v)
		} else if i, ok := players[id]; ok {
			result.Players[i].Funds = append(result.Players[i].Funds, v)
		}
	}
	return rows.Err()
}
