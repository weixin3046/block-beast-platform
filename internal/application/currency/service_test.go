package currency

import (
	"context"
	"errors"
	"github.com/block-beast/platform/internal/domain/wallet"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"strings"
	"testing"
)

func TestCurrencyCatalog(t *testing.T) {
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
	s := NewService(pool)
	code := "TEST_" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", ""))
	defer pool.Exec(ctx, `DELETE FROM currencies WHERE code=$1`, code)
	c, err := s.Create(ctx, Currency{Code: code, Name: "测试", Decimals: 3, Category: "custom", Enabled: true})
	if err != nil || c.Version != 1 {
		t.Fatalf("create %+v %v", c, err)
	}
	if _, err = s.Create(ctx, c); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate %v", err)
	}
	if v, err := s.Parse(ctx, code, "1.5"); err != nil || v != 1500 {
		t.Fatalf("parse %d %v", v, err)
	}
	if _, err = s.Parse(ctx, code, "1.0001"); !errors.Is(err, wallet.ErrInvalidDisplayAmount) {
		t.Fatalf("precision %v", err)
	}
	if _, err = s.Parse(ctx, "NOT_REGISTERED", "1"); !errors.Is(err, wallet.ErrUnknownCurrency) {
		t.Fatalf("unknown %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE currencies SET decimals=6 WHERE code=$1`, code); err == nil {
		t.Fatal("allowed precision mutation")
	}
	c, err = s.Update(ctx, code, Update{Name: "停用", Version: c.Version})
	if err != nil || c.Version != 2 || c.Enabled {
		t.Fatalf("update %+v %v", c, err)
	}
	if _, err = s.Update(ctx, code, Update{Name: "stale", Version: 1}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale %v", err)
	}
	if _, err = s.Parse(ctx, code, "1"); !errors.Is(err, wallet.ErrCurrencyDisabled) {
		t.Fatalf("disabled %v", err)
	}
	if d, err := wallet.ResolveDecimals(ctx, pool, code, false); err != nil || d != 3 {
		t.Fatalf("historical %d %v", d, err)
	}
}

func TestInvalidCurrencyDefinitions(t *testing.T) {
	for _, v := range []Currency{{Code: "BAD-X", Name: "x", Category: "custom"}, {Code: "X", Name: "x", Decimals: 19, Category: "custom"}, {Code: "X", Name: "x", Category: "other"}} {
		if _, err := NewService(nil).Create(context.Background(), v); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: %v", v, err)
		}
	}
}
