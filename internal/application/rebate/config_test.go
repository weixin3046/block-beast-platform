package rebate

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
)

func TestConfigValidation(t *testing.T) {
	good := []Level{{1, 14}, {2, 16}, {3, 20}, {4, 22}, {5, 25}, {6, 26}}
	if err := validateLevels(good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]Level{nil, {{1, 14}}, {{1, 14}, {2, 13}, {3, 20}, {4, 22}, {5, 25}, {6, 26}}, {{1, 14}, {1, 16}, {3, 20}, {4, 22}, {5, 25}, {6, 26}}, {{1, 14}, {2, 16}, {3, 20}, {4, 22}, {5, 25}, {6, 1001}}} {
		if validateLevels(bad) == nil {
			t.Fatal("invalid levels accepted", bad)
		}
	}
}

func TestConfigPersistenceAndPermissions(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set")
	}
	ctx := context.Background()
	p, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	s := NewService(p)
	all, e := s.ListConfigs(ctx, ConfigQuery{})
	if e != nil || len(all) != 144 {
		t.Fatalf("configs=%d err=%v", len(all), e)
	}
	one, e := s.ListConfigs(ctx, ConfigQuery{GameType: "hash_9", RoomID: "94000000-0000-4000-8000-000000000001", Currency: "POINTS"})
	if e != nil || len(one) != 1 {
		t.Fatal(one, e)
	}
	v := one[0]
	actor := uuid.NewString()
	player := uuid.NewString()
	if _, e = p.Exec(ctx, `INSERT INTO users(id,display_name) VALUES($1,'rebate admin'),($2,'rebate player')`, actor, player); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code='admin'`, actor); e != nil {
		t.Fatal(e)
	}
	in := ConfigUpdate{Version: v.Version, Enabled: v.Enabled, Levels: v.Levels}
	if _, e = s.UpdateConfig(ctx, player, v.ID, in); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	got, e := s.UpdateConfig(ctx, actor, v.ID, in)
	if e != nil || got.Version != v.Version+1 {
		t.Fatal(got, e)
	}
	if _, e = s.UpdateConfig(ctx, actor, v.ID, in); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	var n int
	if e = p.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE actor_user_id=$1 AND action='rebate.config.update'`, actor).Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
}
