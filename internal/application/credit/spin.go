package credit

import (
	"context"
	"crypto/rand"
	"errors"
	"github.com/block-beast/platform/internal/domain/wallet"
	"math"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const BizLuckySpinReward = "lucky_spin_reward"
const BizLuckySpinCost = "lucky_spin_cost"

var ErrActivityUnavailable = errors.New("activity is unavailable")

type SpinPrize struct {
	Disabled    bool   `json:"disabled"`
	ID          string `json:"id"`
	Label       string `json:"label"`
	Currency    string `json:"currency"`
	AmountMinor int64  `json:"amount_minor"`
	Weight      int64  `json:"weight"`
}

type SpinResult struct {
	CostDecimals         int       `json:"cost_decimals"`
	RewardDecimals       int       `json:"reward_decimals"`
	Cost                 string    `json:"cost"`
	Reward               string    `json:"reward"`
	CostAvailable        string    `json:"cost_available"`
	RewardAvailable      string    `json:"reward_available"`
	CostBalanceAfterSpin int64     `json:"cost_balance_after_spin_minor"`
	ID                   string    `json:"id"`
	SpinID               string    `json:"spin_id"`
	PrizeID              string    `json:"prize_id"`
	PrizeLabel           string    `json:"prize_label"`
	RewardCurrency       string    `json:"reward_currency"`
	RewardMinor          int64     `json:"reward_minor"`
	CostCurrency         string    `json:"cost_currency"`
	CostMinor            int64     `json:"cost_minor"`
	CostBalance          int64     `json:"cost_balance"`
	RewardBalance        int64     `json:"reward_balance"`
	CreatedAt            time.Time `json:"created_at"`
}

func (service *Service) LuckySpin(ctx context.Context, userID, spinID, requestID string) (SpinResult, error) {
	if userID == "" || spinID == "" || requestID == "" {
		return SpinResult{}, ErrUserNotFound
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return SpinResult{}, err
	}
	defer tx.Rollback(ctx)

	if existing, err := findSpin(ctx, tx, userID, requestID); err == nil {
		if err := formatSpin(ctx, tx, &existing); err != nil {
			return SpinResult{}, err
		}
		return existing, tx.Commit(ctx)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return SpinResult{}, err
	}

	config, err := findSpinConfig(ctx, tx, spinID, true)
	if err != nil {
		return SpinResult{}, ErrActivityUnavailable
	}
	// Use the same wallet ordering as task claims and round settlement.
	// Otherwise A->B task rewards and B->A spins can deadlock.
	rows, err := tx.Query(ctx, "SELECT id FROM wallets WHERE user_id=$1 ORDER BY id FOR UPDATE", userID)
	if err != nil {
		return SpinResult{}, err
	}
	for rows.Next() {
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return SpinResult{}, err
	}
	// A concurrent identical draw may have committed while we waited.
	if existing, e := findSpin(ctx, tx, userID, requestID); e == nil {
		if e = formatSpin(ctx, tx, &existing); e != nil {
			return SpinResult{}, e
		}
		return existing, tx.Commit(ctx)
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return SpinResult{}, e
	}
	prize, ok := choosePrize(config.Prizes)
	if !ok {
		return SpinResult{}, ErrActivityUnavailable
	}

	costBalance, err := deductBalance(ctx, tx, userID, config.CostCurrency, config.CostMinor)
	if err != nil {
		return SpinResult{}, err
	}
	spinRecordID := uuid.NewString()
	if err := writeLedger(ctx, tx, userID, config.CostCurrency, BizLuckySpinCost, spinRecordID, -config.CostMinor, costBalance, "转盘参与消耗", ""); err != nil {
		return SpinResult{}, err
	}
	rewardBalance, err := addBalance(ctx, tx, userID, prize.Currency, prize.AmountMinor)
	if err != nil {
		return SpinResult{}, err
	}
	recordID := spinRecordID
	if err := writeLedger(ctx, tx, userID, prize.Currency, BizLuckySpinReward, recordID, prize.AmountMinor, rewardBalance, "幸运转盘奖励", ""); err != nil {
		return SpinResult{}, err
	}
	result := SpinResult{
		ID: recordID, SpinID: config.ID, PrizeID: prize.ID, PrizeLabel: prize.Label,
		RewardCurrency: prize.Currency, RewardMinor: prize.AmountMinor,
		CostCurrency: config.CostCurrency, CostMinor: config.CostMinor, CostBalance: costBalance,
		RewardBalance: rewardBalance, CreatedAt: time.Now().UTC(),
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO lucky_spin_records(
			id,user_id,client_request_id,spin_id,prize_id,prize_label,
			reward_currency,reward_minor,cost_currency,cost_minor,cost_balance_after,reward_balance_after
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING created_at`,
		result.ID, userID, requestID, config.ID, prize.ID, prize.Label,
		prize.Currency, prize.AmountMinor, config.CostCurrency, config.CostMinor, costBalance, rewardBalance).Scan(&result.CreatedAt); err != nil {
		return SpinResult{}, err
	}
	if err := formatSpin(ctx, tx, &result); err != nil {
		return SpinResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SpinResult{}, err
	}
	return result, nil
}

func choosePrize(prizes []SpinPrize) (SpinPrize, bool) {
	var total int64
	for _, prize := range prizes {
		if prize.Disabled {
			continue
		}
		if prize.ID == "" || prize.Label == "" || prize.AmountMinor <= 0 ||
			prize.Weight < 0 || total > math.MaxInt64-prize.Weight || !validCurrency(prize.Currency) {
			return SpinPrize{}, false
		}
		total += prize.Weight
	}
	if total <= 0 {
		return SpinPrize{}, false
	}
	value, err := rand.Int(rand.Reader, big.NewInt(total))
	if err != nil {
		return SpinPrize{}, false
	}
	cursor := value.Int64()
	for _, prize := range prizes {
		if prize.Disabled {
			continue
		}
		if cursor < prize.Weight {
			return prize, true
		}
		cursor -= prize.Weight
	}
	return SpinPrize{}, false
}

func findSpin(ctx context.Context, tx pgx.Tx, userID, requestID string) (SpinResult, error) {
	var result SpinResult
	err := tx.QueryRow(ctx, `
		SELECT id,spin_id,prize_id,prize_label,reward_currency,reward_minor,cost_currency,cost_minor,cost_balance_after,reward_balance_after,created_at
		FROM lucky_spin_records WHERE user_id=$1 AND client_request_id=$2`,
		userID, requestID).Scan(
		&result.ID, &result.SpinID, &result.PrizeID, &result.PrizeLabel, &result.RewardCurrency,
		&result.RewardMinor, &result.CostCurrency, &result.CostMinor, &result.CostBalance, &result.RewardBalance, &result.CreatedAt,
	)
	return result, err
}

func formatSpin(ctx context.Context, tx pgx.Tx, r *SpinResult) error {
	if err := tx.QueryRow(ctx, `SELECT decimals FROM currencies WHERE code=$1`, r.CostCurrency).Scan(&r.CostDecimals); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT decimals FROM currencies WHERE code=$1`, r.RewardCurrency).Scan(&r.RewardDecimals); err != nil {
		return err
	}
	r.CostBalanceAfterSpin = r.CostBalance
	if r.CostCurrency == r.RewardCurrency {
		r.CostBalanceAfterSpin = r.RewardBalance
	}
	return formatSpinValues(r)
}
func formatSpinValues(r *SpinResult) error {
	var err error
	for _, p := range []struct {
		minor    int64
		decimals int
		out      *string
	}{{r.CostMinor, r.CostDecimals, &r.Cost}, {r.RewardMinor, r.RewardDecimals, &r.Reward}, {r.CostBalanceAfterSpin, r.CostDecimals, &r.CostAvailable}, {r.RewardBalance, r.RewardDecimals, &r.RewardAvailable}} {
		*p.out, err = wallet.FormatDisplayAmount(p.minor, p.decimals)
		if err != nil {
			return err
		}
	}
	return nil
}
