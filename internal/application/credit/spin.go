package credit

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const BizLuckySpinReward = "lucky_spin_reward"
const BizLuckySpinCost = "lucky_spin_cost"

var ErrActivityUnavailable = errors.New("activity is unavailable")

type SpinPrize struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Currency    string `json:"currency"`
	AmountMinor int64  `json:"amount_minor"`
	Weight      int64  `json:"weight"`
}

type SpinResult struct {
	ID             string    `json:"id"`
	SpinID         string    `json:"spin_id"`
	PrizeID        string    `json:"prize_id"`
	PrizeLabel     string    `json:"prize_label"`
	RewardCurrency string    `json:"reward_currency"`
	RewardMinor    int64     `json:"reward_minor"`
	CostCurrency   string    `json:"cost_currency"`
	CostMinor      int64     `json:"cost_minor"`
	CostBalance    int64     `json:"cost_balance"`
	RewardBalance  int64     `json:"reward_balance"`
	CreatedAt      time.Time `json:"created_at"`
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
		return existing, tx.Commit(ctx)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return SpinResult{}, err
	}

	config, err := findSpinConfig(ctx, tx, spinID, true)
	if err != nil {
		return SpinResult{}, ErrActivityUnavailable
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
	if err := tx.Commit(ctx); err != nil {
		return SpinResult{}, err
	}
	return result, nil
}

func choosePrize(prizes []SpinPrize) (SpinPrize, bool) {
	var total int64
	for _, prize := range prizes {
		if prize.ID == "" || prize.Label == "" || prize.AmountMinor <= 0 ||
			prize.Weight <= 0 || !validCurrency(prize.Currency) {
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
