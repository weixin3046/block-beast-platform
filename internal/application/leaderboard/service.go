package leaderboard

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInvalidMetric = errors.New("metric must be turnover, profit, or wins")
var ErrInvalidCurrency = errors.New("currency must be USDT, POINTS, JADE, or ORIGIN_STONE")

type Entry struct {
	Rank                int64  `json:"rank"`
	UserID              string `json:"user_id"`
	DisplayName         string `json:"display_name"`
	Currency            string `json:"currency"`
	EffectiveStakeMinor int64  `json:"effective_stake_minor"`
	PayoutMinor         int64  `json:"payout_minor"`
	NetProfitMinor      int64  `json:"net_profit_minor"`
	BetCount            int64  `json:"bet_count"`
	WinCount            int64  `json:"win_count"`
}

type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, now: time.Now}
}

func (service *Service) RefreshDaily(ctx context.Context, date time.Time) (int64, error) {
	day := date.UTC().Format("2006-01-02")
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM leaderboard_daily WHERE leaderboard_date=$1`, day); err != nil {
		return 0, err
	}
	result, err := tx.Exec(ctx, `
		INSERT INTO leaderboard_daily (
			leaderboard_date,currency,user_id,effective_stake_minor,payout_minor,
			net_profit_minor,bet_count,win_count,updated_at
		)
		SELECT $1,w.currency,b.user_id,sum(b.stake_minor),sum(b.payout_minor),
			sum(b.payout_minor-b.stake_minor),count(*)::integer,
			(count(*) FILTER (WHERE b.status='won'))::integer,$2
		FROM bets b JOIN wallets w ON w.id=b.wallet_id
		WHERE b.status IN ('won','lost')
		  AND b.settled_at >= $1::date
		  AND b.settled_at < $1::date + interval '1 day'
		GROUP BY w.currency,b.user_id`, day, service.now().UTC())
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func (service *Service) ListDaily(ctx context.Context, date time.Time, currency, metric string, limit int) ([]Entry, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency != "USDT" && currency != "POINTS" && currency != "JADE" && currency != "ORIGIN_STONE" {
		return nil, ErrInvalidCurrency
	}
	orderColumn := ""
	switch metric {
	case "", "turnover":
		orderColumn = "effective_stake_minor"
	case "profit":
		orderColumn = "net_profit_minor"
	case "wins":
		orderColumn = "win_count"
	default:
		return nil, ErrInvalidMetric
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := `
		SELECT row_number() OVER (ORDER BY l.` + orderColumn + ` DESC,l.effective_stake_minor DESC,l.user_id),
			l.user_id::text,u.display_name,l.currency,l.effective_stake_minor,l.payout_minor,
			l.net_profit_minor,l.bet_count,l.win_count
		FROM leaderboard_daily l JOIN users u ON u.id=l.user_id
		WHERE l.leaderboard_date=$1 AND l.currency=$2
		ORDER BY l.` + orderColumn + ` DESC,l.effective_stake_minor DESC,l.user_id
		LIMIT $3`
	rows, err := service.pool.Query(ctx, query, date.UTC().Format("2006-01-02"), currency, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Entry, 0)
	for rows.Next() {
		var item Entry
		if err := rows.Scan(&item.Rank, &item.UserID, &item.DisplayName, &item.Currency, &item.EffectiveStakeMinor, &item.PayoutMinor, &item.NetProfitMinor, &item.BetCount, &item.WinCount); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
