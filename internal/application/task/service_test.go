package task

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/application/credit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTaskCurrencyValidation(t *testing.T) {
	for _, currency := range []string{"USDT", "POINTS", "JADE", "ORIGIN_STONE", "CUSTOM"} {
		if !validAccumulationCurrency(currency) {
			t.Fatalf("accumulation currency %s rejected", currency)
		}
	}
	if validAccumulationCurrency("") {
		t.Fatal("empty accumulation currency accepted")
	}
	for _, currency := range []string{"STAMINA", "USDT_STAMINA", "JADE_STAMINA", "ORIGIN_STONE_STAMINA"} {
		if !validRewardCurrency(currency) {
			t.Fatalf("reward currency %s rejected", currency)
		}
	}
	if !validRewardCurrency("CUSTOM") || validRewardCurrency("") {
		t.Fatal("reward currency validation is incorrect")
	}
}

func TestBetDateUsesChinaTime(t *testing.T) {
	service := &Service{now: func() time.Time {
		return time.Date(2026, time.August, 30, 16, 30, 0, 0, time.UTC)
	}}
	if got := service.betDate(); got != "2026-08-31" {
		t.Fatalf("bet date = %s", got)
	}
}

func TestClaimBetTaskIsManualSingleUseAndExpiresAtChinaMidnight(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	userID, configID := uuid.NewString(), uuid.NewString()
	accumulationCurrency := "TASK_TEST_" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", ""))
	rewardCurrency := "TASK_REWARD_" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", ""))
	if _, err = pool.Exec(ctx, `INSERT INTO currencies(code,name,decimals,category) VALUES($1,$1,0,'custom'),($2,$2,0,'custom')`, accumulationCurrency, rewardCurrency); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO users(id,login_name,display_name) VALUES($1,$2,'task claim user')`, userID, "task-claim-"+userID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO bet_task_configs(id,accumulation_currency,threshold_minor,reward_currency,reward_minor,enabled)
		VALUES($1,$2,100,$3,7,true)`, configID, accumulationCurrency, rewardCurrency)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO user_daily_bet_progress(user_id,bet_date,accumulation_currency,total_stake_minor)
		VALUES($1,'2026-08-31',$2,100)`, userID, accumulationCurrency)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM ledger_entries WHERE wallet_id IN (SELECT id FROM wallets WHERE user_id=$1)`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM bet_task_reward_records WHERE user_id=$1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM user_daily_bet_progress WHERE user_id=$1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM bet_task_configs WHERE id=$1`, configID)
		_, _ = pool.Exec(ctx, `DELETE FROM wallets WHERE user_id=$1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM currencies WHERE code IN ($1,$2)`, accumulationCurrency, rewardCurrency)
	})

	service := NewService(pool, credit.NewService(pool))
	service.now = func() time.Time { return time.Date(2026, time.August, 31, 8, 0, 0, 0, chinaTimeZone) }
	claim, err := service.ClaimBetTask(ctx, userID, configID)
	if err != nil {
		t.Fatal(err)
	}
	if claim.BetDate != "2026-08-31" || claim.RewardCurrency != rewardCurrency || claim.RewardMinor != 7 || claim.BalanceAfterMinor != 7 {
		t.Fatalf("claim = %+v", claim)
	}
	if _, err := service.ClaimBetTask(ctx, userID, configID); !errors.Is(err, ErrTaskAlreadyClaimed) {
		t.Fatalf("second claim error = %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE bet_task_configs SET reward_minor=99 WHERE id=$1`, configID); err != nil {
		t.Fatal(err)
	}
	var snapshotReward, snapshotVersion int64
	if err = pool.QueryRow(ctx, `SELECT (config_snapshot->>'reward_minor')::bigint,(config_snapshot->>'version')::bigint FROM bet_task_reward_records WHERE user_id=$1 AND config_id=$2`, userID, configID).Scan(&snapshotReward, &snapshotVersion); err != nil || snapshotReward != 7 || snapshotVersion != 1 {
		t.Fatalf("snapshot %d version %d %v", snapshotReward, snapshotVersion, err)
	}

	service.now = func() time.Time { return time.Date(2026, time.September, 1, 0, 0, 0, 0, chinaTimeZone) }
	if _, err := service.ClaimBetTask(ctx, userID, configID); !errors.Is(err, ErrTaskNotCompleted) {
		t.Fatalf("expired claim error = %v", err)
	}
}
