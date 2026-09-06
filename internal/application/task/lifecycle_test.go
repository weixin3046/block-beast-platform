package task

import (
	"context"
	"errors"
	"github.com/block-beast/platform/internal/application/credit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"sort"
	"sync"
	"testing"
	"time"
)

func TestIndependentTaskCyclesAndMultipleRewards(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	p, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	user := uuid.NewString()
	if _, e = p.Exec(ctx, "INSERT INTO users(id,display_name) VALUES($1,'task lifecycle test')", user); e != nil {
		t.Fatal(e)
	}
	s := NewService(p, credit.NewService(p))
	now := time.Now().In(chinaTimeZone)
	s.now = func() time.Time { return now }
	c, e := s.SaveBetTaskConfig(ctx, BetTaskConfig{Title: "daily cycle", PeriodType: "daily", MaxCompleteCount: 2, AccumulationCurrency: "POINTS", ThresholdMinor: 1000, Enabled: true, Rewards: []TaskReward{{Currency: "POINTS", AmountMinor: 100}, {Currency: "JADE", AmountMinor: 200}}})
	if e != nil {
		t.Fatal(e)
	}
	add := func(at time.Time, n int64) {
		t.Helper()
		tx, e := p.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback(ctx)
		if e = s.OnBetSettled(ctx, tx, user, "POINTS", n, at); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(ctx); e != nil {
			t.Fatal(e)
		}
	}
	add(now, 1500)
	got, e := s.ClaimBetTask(ctx, user, c.ID)
	if e != nil || got.CompleteCount != 1 || len(got.Rewards) != 2 {
		t.Fatalf("claim: %+v %v", got, e)
	}
	if _, e = s.ClaimBetTask(ctx, user, c.ID); !errors.Is(e, ErrTaskNotCompleted) {
		t.Fatalf("repeat claim: %v", e)
	}
	var progress int64
	if e = p.QueryRow(ctx, "SELECT progress_minor FROM task_progress WHERE config_id=$1 AND user_id=$2", c.ID, user).Scan(&progress); e != nil || progress != 0 {
		t.Fatalf("excess carried: %d %v", progress, e)
	}
	add(now, 1000)
	got, e = s.ClaimBetTask(ctx, user, c.ID)
	if e != nil || got.CompleteCount != 2 {
		t.Fatalf("cycle2: %+v %v", got, e)
	}
	add(now, 1000)
	if _, e = s.ClaimBetTask(ctx, user, c.ID); !errors.Is(e, ErrTaskAlreadyClaimed) {
		t.Fatalf("limit: %v", e)
	}
	c.MaxCompleteCount = 3
	if _, e = s.SaveBetTaskConfig(ctx, c); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ClaimBetTask(ctx, user, c.ID); !errors.Is(e, ErrTaskNotCompleted) {
		t.Fatalf("paid progress reused after cap increase: %v", e)
	}
	// Newly created tasks must not inherit already accumulated currency totals.
	fresh, e := s.SaveBetTaskConfig(ctx, BetTaskConfig{PeriodType: "daily", MaxCompleteCount: 1, AccumulationCurrency: "POINTS", ThresholdMinor: 1000, Enabled: true, Rewards: []TaskReward{{Currency: "JADE", AmountMinor: 10}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ClaimBetTask(ctx, user, fresh.ID); !errors.Is(e, ErrTaskNotCompleted) {
		t.Fatalf("new task inherited progress: %v", e)
	}
	add(now, 1000)
	now = now.AddDate(0, 0, 1)
	if _, e = s.ClaimBetTask(ctx, user, fresh.ID); !errors.Is(e, ErrTaskNotCompleted) {
		t.Fatalf("expired claim: %v", e)
	}
	// Late settlements update the bet's old day, not today's entitlement.
	add(now.AddDate(0, 0, -1), 1000)
	if _, e = s.ClaimBetTask(ctx, user, fresh.ID); !errors.Is(e, ErrTaskNotCompleted) {
		t.Fatalf("late settlement shifted date: %v", e)
	}
	global, e := s.SaveBetTaskConfig(ctx, BetTaskConfig{PeriodType: "global", MaxCompleteCount: 1, AccumulationCurrency: "POINTS", ThresholdMinor: 1000, Enabled: true, Rewards: []TaskReward{{Currency: "JADE", AmountMinor: 10}}})
	if e != nil {
		t.Fatal(e)
	}
	add(now, 1000)
	now = now.AddDate(0, 0, 1)
	if _, e = s.ClaimBetTask(ctx, user, global.ID); e != nil {
		t.Fatalf("global expired: %v", e)
	}
	// Test data is disabled, preserving immutable reward audit/ledger history.
	for _, id := range []string{c.ID, fresh.ID, global.ID} {
		if _, e = p.Exec(ctx, "UPDATE bet_task_configs SET enabled=false,deleted=true WHERE id=$1", id); e != nil {
			t.Fatal(e)
		}
	}
}
func TestNormalizeTask(t *testing.T) {
	c := BetTaskConfig{AccumulationCurrency: "POINTS", ThresholdMinor: 1, RewardCurrency: "JADE", RewardMinor: 2}
	if e := normalizeConfig(&c); e != nil || c.PeriodType != "daily" || c.MaxCompleteCount != 0 {
		t.Fatal(c, e)
	}
	c.Rewards = append(c.Rewards, c.Rewards[0])
	if normalizeConfig(&c) == nil {
		t.Fatal("duplicate reward currency accepted")
	}
}

func TestConcurrentTaskClaimsCreditOnce(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	user := uuid.NewString()
	if _, e = p.Exec(ctx, "INSERT INTO users(id,display_name) VALUES($1,'concurrent task test')", user); e != nil {
		t.Fatal(e)
	}
	s := NewService(p, credit.NewService(p))
	c, e := s.SaveBetTaskConfig(ctx, BetTaskConfig{PeriodType: "daily", MaxCompleteCount: 1, AccumulationCurrency: "POINTS", ThresholdMinor: 1000, Enabled: true, Rewards: []TaskReward{{Currency: "POINTS", AmountMinor: 1500}, {Currency: "JADE", AmountMinor: 200}}})
	if e != nil {
		t.Fatal(e)
	}
	walletIDs := []string{uuid.NewString(), uuid.NewString()}
	sort.Strings(walletIDs)
	if _, e = p.Exec(ctx, "INSERT INTO wallets(id,user_id,currency,available_minor) VALUES($1,$3,'JADE',0),($2,$3,'POINTS',10000)", walletIDs[0], walletIDs[1], user); e != nil {
		t.Fatal(e)
	}
	spins := credit.NewService(p)
	spin, e := spins.SaveSpinConfig(ctx, credit.SpinConfig{Title: "concurrent cross currency", Enabled: true, CostCurrency: "POINTS", CostMinor: 100, Prizes: []credit.SpinPrize{{Label: "JADE", Currency: "JADE", AmountMinor: 50, Weight: 1}}})
	if e != nil {
		t.Fatal(e)
	}
	defer p.Exec(context.Background(), "UPDATE spin_configs SET enabled=false,deleted=true WHERE id=$1", spin.ID)
	defer p.Exec(context.Background(), "UPDATE bet_task_configs SET enabled=false,deleted=true WHERE id=$1", c.ID)
	tx, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.OnBetSettled(ctx, tx, user, "POINTS", 1000, time.Now()); e != nil {
		tx.Rollback(ctx)
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	start := make(chan struct{})
	results := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; _, e := s.ClaimBetTask(ctx, user, c.ID); results <- e }()
	}
	spinResults := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, e := spins.LuckySpin(ctx, user, spin.ID, uuid.NewString())
			spinResults <- e
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(spinResults)
	for e := range spinResults {
		if e != nil {
			t.Fatalf("concurrent draw: %v", e)
		}
	}
	successes := 0
	for e := range results {
		if e == nil {
			successes++
		} else if !errors.Is(e, ErrTaskAlreadyClaimed) {
			t.Fatal(e)
		}
	}
	if successes != 1 {
		t.Fatalf("successful claims=%d", successes)
	}
	var balance, claims int64
	if e = p.QueryRow(ctx, "SELECT available_minor FROM wallets WHERE user_id=$1 AND currency='POINTS'", user).Scan(&balance); e != nil || balance != 10700 {
		t.Fatal(balance, e)
	}
	if e = p.QueryRow(ctx, "SELECT available_minor FROM wallets WHERE user_id=$1 AND currency='JADE'", user).Scan(&balance); e != nil || balance != 600 {
		t.Fatal(balance, e)
	}
	if e = p.QueryRow(ctx, "SELECT count(*) FROM task_reward_claims WHERE config_id=$1 AND user_id=$2", c.ID, user).Scan(&claims); e != nil || claims != 1 {
		t.Fatal(claims, e)
	}
}
