package rebate

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
)

func TestSnapshotAndIdempotentPayment(t *testing.T) {
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
	user, parent, round, bet, walletID, pwallet := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO users(id,display_name,agent_level) VALUES($1,'snapshot player',NULL),($2,'snapshot parent',1)`, user, parent)
	exec(`INSERT INTO agent_relations(user_id,parent_user_id) VALUES($1,$2)`, user, parent)
	exec(`INSERT INTO wallets(id,user_id,currency) VALUES($1,$2,'POINTS'),($3,$4,'POINTS')`, walletID, user, pwallet, parent)
	exec(`INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at,result_at) VALUES($1,'09000000-0000-4000-8000-000000000001',987612345,'open',now(),now())`, round)
	exec(`INSERT INTO bets(id,client_request_id,user_id,wallet_id,round_id,game_room_id,play_mode,selection,stake_minor,status) VALUES($1::uuid,$1::text,$2,$3,$4,'94000000-0000-4000-8000-000000000001','road','{"pick":"odd"}',1000000,'accepted')`, bet, user, walletID, round)
	if e = SnapshotTx(ctx, tx, bet); e != nil {
		t.Fatal(e)
	}
	var legacyConfigID, roomConfigID *string
	if e = tx.QueryRow(ctx, `SELECT config_id::text,room_config_id::text FROM bet_rebate_snapshots WHERE bet_id=$1`, bet).Scan(&legacyConfigID, &roomConfigID); e != nil || legacyConfigID != nil || roomConfigID == nil {
		t.Fatalf("snapshot config ids legacy=%v room=%v err=%v", legacyConfigID, roomConfigID, e)
	}
	exec(`UPDATE users SET agent_level=6 WHERE id=$1`, parent)
	chain, e := LoadTx(ctx, tx, bet)
	if e != nil || len(chain) != 1 || chain[0].Level != 1 || chain[0].RatePerMille != 14 {
		t.Fatal(chain, e)
	}
	allocations, e := Calculate(1000000, 0, "road", chain)
	if e != nil || len(allocations) != 1 {
		t.Fatal(allocations, e)
	}
	// An overflow after reserving the commission key must roll back both the
	// reservation and balance, leaving a later legitimate retry possible.
	failed, e := tx.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = failed.Exec(ctx, `UPDATE wallets SET available_minor=9223372036854775807 WHERE id=$1`, pwallet); e != nil {
		t.Fatal(e)
	}
	if e = PayTx(ctx, failed, bet, "POINTS", allocations[0]); !errors.Is(e, ErrBalanceOverflow) {
		t.Fatalf("expected overflow, got %v", e)
	}
	if e = failed.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	for range 2 {
		if e = PayTx(ctx, tx, bet, "POINTS", allocations[0]); e != nil {
			t.Fatal(e)
		}
	}
	var balance int64
	var count int
	if e = tx.QueryRow(ctx, `SELECT available_minor FROM wallets WHERE id=$1`, pwallet).Scan(&balance); e != nil || balance != 14000 {
		t.Fatal(balance, e)
	}
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM rebate_allocations WHERE bet_id=$1`, bet).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
}
