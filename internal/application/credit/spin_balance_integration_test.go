package credit

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
)

func TestSpinSameCurrencyFinalBalance(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	user, w, spin, prize := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO users(id,display_name) VALUES($1,'spin precision')`, user)
	defer func() {
		pool.Exec(ctx, `DELETE FROM outbox_events WHERE payload->>'user_id'=$1`, user)
		pool.Exec(ctx, `DELETE FROM lucky_spin_records WHERE user_id=$1`, user)
		pool.Exec(ctx, `DELETE FROM ledger_entries WHERE wallet_id=$1`, w)
		pool.Exec(ctx, `DELETE FROM wallets WHERE id=$1`, w)
		pool.Exec(ctx, `DELETE FROM spin_prizes WHERE id=$1`, prize)
		pool.Exec(ctx, `DELETE FROM spin_configs WHERE id=$1`, spin)
		pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, user)
	}()
	exec(`INSERT INTO wallets(id,user_id,currency,available_minor) VALUES($1,$2,'USDT',2000123456)`, w, user)
	exec(`INSERT INTO spin_configs(id,code,title,enabled,cost_currency,cost_minor,sort_order) VALUES($1,$2,'precision',true,'USDT',1000000000,0)`, spin, "precision-"+spin[:8])
	exec(`INSERT INTO spin_prizes(id,spin_id,code,label,reward_currency,reward_minor,weight,sort_order) VALUES($1,$2,'only','1985 USDT','USDT',1985000000,1,0)`, prize, spin)
	s := NewService(pool)
	key := uuid.NewString()
	for range 2 {
		r, e := s.LuckySpin(ctx, user, spin, key)
		if e != nil {
			t.Fatal(e)
		}
		if r.CostBalance != 1000123456 || r.RewardBalance != 2985123456 || r.CostBalanceAfterSpin != r.RewardBalance || r.CostAvailable != "2985.123456" || r.Reward != "1985.000000" {
			t.Fatalf("result %+v", r)
		}
	}
	var balance int64
	if err = pool.QueryRow(ctx, `SELECT available_minor FROM wallets WHERE id=$1`, w).Scan(&balance); err != nil || balance != 2985123456 {
		t.Fatal(balance, err)
	}
}
