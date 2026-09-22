package credit

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestBalancesIncludesEnabledCurrenciesWithoutWallets(t *testing.T) {
	dsn := os.Getenv("CREDIT_TEST_DSN")
	if dsn == "" {
		t.Skip("CREDIT_TEST_DSN not set")
	}
	ctx := context.Background()
	base, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := "credit_" + uuid.New().String()[:8]
	if _, err := base.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer base.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, err = pool.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY);
	CREATE TABLE currencies(code text PRIMARY KEY, decimals integer, enabled boolean);
	CREATE TABLE wallets(user_id uuid, currency text, available_minor bigint, frozen_minor bigint, PRIMARY KEY(user_id,currency));
	INSERT INTO currencies VALUES ('RUBY',3,true),('RUBY_STAMINA',0,true),('EXISTING',3,true),('DISABLED',3,false),('HIDDEN',3,false);`)
	if err != nil {
		t.Fatal(err)
	}
	user, other := uuid.NewString(), uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO users VALUES($1),($2)`, user, other); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO wallets VALUES($1,'EXISTING',1234,56),($1,'DISABLED',7000,0),($2,'RUBY',99000,0)`, user, other); err != nil {
		t.Fatal(err)
	}
	service := NewService(pool)
	for attempt := 0; attempt < 2; attempt++ {
		balances, err := service.Balances(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		if len(balances) != 4 {
			t.Fatalf("balances = %+v, want 4 enabled/existing currencies", balances)
		}
		want := []BalanceInfo{
			{user, "DISABLED", 7000, 0, 3, "7.000", "0.000"},
			{user, "EXISTING", 1234, 56, 3, "1.234", "0.056"},
			{user, "RUBY", 0, 0, 3, "0.000", "0.000"},
			{user, "RUBY_STAMINA", 0, 0, 0, "0", "0"},
		}
		for i := range want {
			if balances[i] != want[i] {
				t.Errorf("balance = %+v, want %+v", balances[i], want[i])
			}
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM wallets`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("GET changed wallets: count=%d err=%v", count, err)
	}
}
