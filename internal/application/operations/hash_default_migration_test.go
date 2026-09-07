package operations

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestHashDefaultLimitMigration(t *testing.T) {
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
	tx, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := tx.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	const room = "94000000-0000-4000-8000-000000000001"
	exec(`UPDATE hash_room_currency_configs SET guess_max_stake_minor=500,dodge_max_stake_minor=1000,road_max_stake_minor=1000 WHERE room_id=$1 AND currency IN ('POINTS','ORIGIN_STONE')`, room)
	exec(`UPDATE hash_room_currency_configs SET guess_max_stake_minor=50,dodge_max_stake_minor=100,road_max_stake_minor=50 WHERE room_id=$1 AND currency='JADE'`, room)
	// Partially customized row must not be scaled.
	exec(`UPDATE hash_room_currency_configs SET guess_max_stake_minor=777 WHERE room_id=$1 AND currency='ORIGIN_STONE'`, room)
	var version int64
	if e = tx.QueryRow(ctx, `SELECT version FROM hash_game_settings`).Scan(&version); e != nil {
		t.Fatal(e)
	}
	migration, e := os.ReadFile("../../../migrations/0065_fix_hash_default_stake_units.sql")
	if e != nil {
		t.Fatal(e)
	}
	exec(string(migration))
	for _, c := range []struct {
		code               string
		guess, dodge, road int64
	}{{"POINTS", 500000, 1000000, 1000000}, {"JADE", 50000, 100000, 50000}, {"ORIGIN_STONE", 777, 1000, 1000}, {"USDT", 50000000, 100000000, 50000000}} {
		var g, d, r int64
		if e = tx.QueryRow(ctx, `SELECT guess_max_stake_minor,dodge_max_stake_minor,road_max_stake_minor FROM hash_room_currency_configs WHERE room_id=$1 AND currency=$2`, room, c.code).Scan(&g, &d, &r); e != nil {
			t.Fatal(e)
		}
		if g != c.guess || d != c.dodge || r != c.road {
			t.Fatalf("%s: %d %d %d", c.code, g, d, r)
		}
	}
	exec(string(migration))
	var after int64
	if e = tx.QueryRow(ctx, `SELECT version FROM hash_game_settings`).Scan(&after); e != nil {
		t.Fatal(e)
	}
	if after != version+1 {
		t.Fatalf("version=%d want=%d", after, version+1)
	}
}
