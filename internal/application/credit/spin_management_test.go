package credit

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
)

func TestSpinIndependentStateAndHistory(t *testing.T) {
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
	s := NewService(p)
	actor, user := uuid.NewString(), uuid.NewString()
	exec := func(q string, a ...any) {
		t.Helper()
		if _, e = p.Exec(ctx, q, a...); e != nil {
			t.Fatal(e)
		}
	}
	exec("INSERT INTO users(id,display_name) VALUES($1,'spin admin'),($2,'spin player')", actor, user)
	exec("INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code='admin'", actor)
	exec("INSERT INTO wallets(id,user_id,currency,available_minor) VALUES($1,$2,'POINTS',10000)", uuid.NewString(), user)
	makeConfig := func() SpinConfig {
		t.Helper()
		c, e := s.SaveSpinConfig(ctx, SpinConfig{Title: "multi spin test", Enabled: true, CostCurrency: "POINTS", CostMinor: 1000, Prizes: []SpinPrize{{Label: "zero visible", Currency: "POINTS", AmountMinor: 999, Weight: 0}, {Label: "disabled", Currency: "POINTS", AmountMinor: 999, Disabled: true, Weight: 0}, {Label: "winner", Currency: "POINTS", AmountMinor: 2000, Weight: 1}}})
		if e != nil {
			t.Fatal(e)
		}
		return c
	}
	a, b := makeConfig(), makeConfig()
	result, e := s.LuckySpin(ctx, user, a.ID, uuid.NewString())
	if e != nil || result.PrizeLabel != "winner" {
		t.Fatal(result, e)
	}
	if e = s.ChangeSpinState(ctx, user, a.ID, nil); !errors.Is(e, ErrSpinManagementForbidden) {
		t.Fatal(e)
	}
	off := false
	if e = s.ChangeSpinState(ctx, actor, a.ID, &off); e != nil {
		t.Fatal(e)
	}
	if _, e = s.LuckySpin(ctx, user, a.ID, uuid.NewString()); !errors.Is(e, ErrActivityUnavailable) {
		t.Fatal(e)
	}
	if _, e = s.LuckySpin(ctx, user, b.ID, uuid.NewString()); e != nil {
		t.Fatalf("other spin disabled: %v", e)
	}
	if e = s.ChangeSpinState(ctx, actor, a.ID, nil); e != nil {
		t.Fatal(e)
	}
	if e = s.ChangeSpinState(ctx, actor, a.ID, nil); e != nil {
		t.Fatal(e)
	}
	page, e := s.ListSpinRecords(ctx, SpinRecordQuery{SpinID: a.ID})
	if e != nil || page.Total != 1 {
		t.Fatal(page, e)
	}
	if _, e = s.SaveSpinConfig(ctx, a); !errors.Is(e, ErrSpinConfigNotFound) {
		t.Fatalf("deleted spin revived: %v", e)
	}
	if e = s.ChangeSpinState(ctx, actor, b.ID, nil); e != nil {
		t.Fatal(e)
	}
}
func TestDisabledSpinPrizeNeverSelected(t *testing.T) {
	prizes := []SpinPrize{{ID: "a", Label: "disabled", Currency: "POINTS", AmountMinor: 1, Weight: 0, Disabled: true}, {ID: "b", Label: "win", Currency: "POINTS", AmountMinor: 1, Weight: 1}}
	for range 100 {
		p, ok := choosePrize(prizes)
		if !ok || p.ID != "b" {
			t.Fatal(p, ok)
		}
	}
	prizes[1].Disabled = true
	if _, ok := choosePrize(prizes); ok {
		t.Fatal("all disabled accepted")
	}
}
