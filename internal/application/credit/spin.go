package credit

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const BizLuckySpinReward = "lucky_spin_reward"

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
	PrizeID        string    `json:"prize_id"`
	PrizeLabel     string    `json:"prize_label"`
	RewardCurrency string    `json:"reward_currency"`
	RewardMinor    int64     `json:"reward_minor"`
	StaminaCost    int64     `json:"stamina_cost"`
	StaminaBalance int64     `json:"stamina_balance"`
	RewardBalance  int64     `json:"reward_balance"`
	CreatedAt      time.Time `json:"created_at"`
}

type spinConfig struct {
	Enabled bool `json:"enabled"`
	Items   []struct {
		ID          string `json:"id"`
		Enabled     bool   `json:"enabled"`
		StaminaCost int64  `json:"stamina_cost"`
	} `json:"items"`
	SpinPrizes []SpinPrize `json:"spin_prizes"`
}

func (service *Service) LuckySpin(ctx context.Context, userID, requestID string) (SpinResult, error) {
	if userID == "" || requestID == "" {
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

	var raw json.RawMessage
	if err := tx.QueryRow(ctx, `
		SELECT value FROM platform_configs
		WHERE key='activity.center' AND visibility='public'`).Scan(&raw); err != nil {
		return SpinResult{}, ErrActivityUnavailable
	}
	var config spinConfig
	if json.Unmarshal(raw, &config) != nil || !config.Enabled {
		return SpinResult{}, ErrActivityUnavailable
	}
	var cost int64
	for _, item := range config.Items {
		if item.ID == "lucky-spin" && item.Enabled {
			cost = item.StaminaCost
		}
	}
	prize, ok := choosePrize(config.SpinPrizes)
	if cost <= 0 || !ok {
		return SpinResult{}, ErrActivityUnavailable
	}

	staminaBalance, err := deductBalance(ctx, tx, userID, CurrencyStamina, cost)
	if err != nil {
		return SpinResult{}, err
	}
	rewardBalance, err := addBalance(ctx, tx, userID, prize.Currency, prize.AmountMinor)
	if err != nil {
		return SpinResult{}, err
	}
	recordID := uuid.NewString()
	if err := writeLedger(ctx, tx, userID, prize.Currency, BizLuckySpinReward, recordID, prize.AmountMinor, rewardBalance, "幸运转盘奖励", ""); err != nil {
		return SpinResult{}, err
	}
	result := SpinResult{
		ID: recordID, PrizeID: prize.ID, PrizeLabel: prize.Label,
		RewardCurrency: prize.Currency, RewardMinor: prize.AmountMinor,
		StaminaCost: cost, StaminaBalance: staminaBalance,
		RewardBalance: rewardBalance, CreatedAt: time.Now().UTC(),
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO lucky_spin_records(
			id,user_id,client_request_id,prize_id,prize_label,
			reward_currency,reward_minor,stamina_cost
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING created_at`,
		result.ID, userID, requestID, prize.ID, prize.Label,
		prize.Currency, prize.AmountMinor, cost).Scan(&result.CreatedAt); err != nil {
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
			prize.Weight <= 0 || (prize.Currency != CurrencyPoints &&
			prize.Currency != CurrencyUSDT && prize.Currency != CurrencyStamina) {
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
		SELECT id,prize_id,prize_label,reward_currency,reward_minor,stamina_cost,created_at
		FROM lucky_spin_records WHERE user_id=$1 AND client_request_id=$2`,
		userID, requestID).Scan(
		&result.ID, &result.PrizeID, &result.PrizeLabel, &result.RewardCurrency,
		&result.RewardMinor, &result.StaminaCost, &result.CreatedAt,
	)
	return result, err
}
