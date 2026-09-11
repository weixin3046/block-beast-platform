package game

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestScheduledRoundRejectsBet(t *testing.T) {
	r := Round{Status: RoundScheduled, BetClosesAt: time.Now().Add(time.Hour)}
	if err := r.ValidateBet(100, time.Now()); err != ErrBettingClosed {
		t.Fatalf("got %v", err)
	}
}

func TestActivateOnlyFirstUnfinishedRound(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set")
	}
	ctx := context.Background()
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	gt := uuid.NewString()
	exec(`INSERT INTO game_types(id,code,name,rules) VALUES($1,$2,'activation test','{}')`, gt, "activation-"+gt)
	exec(`INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at,result_at)
 SELECT gen_random_uuid(),$1,n,'scheduled',now()+n*interval '1 hour',now()+n*interval '1 hour'+interval '5 seconds' FROM generate_series(1,3) n`, gt)
	check := func(want int64) {
		t.Helper()
		if err := activateScheduledRounds(ctx, tx, time.Now()); err != nil {
			t.Fatal(err)
		}
		var n, seq int64
		if err := tx.QueryRow(ctx, `SELECT count(*),COALESCE(max(sequence),0) FROM rounds WHERE game_type_id=$1 AND status='open'`, gt).Scan(&n, &seq); err != nil {
			t.Fatal(err)
		}
		if (want == 0 && n != 0) || (want != 0 && (n != 1 || seq != want)) {
			t.Fatalf("open count=%d sequence=%d want=%d", n, seq, want)
		}
	}
	check(1)
	check(1)
	exec(`UPDATE rounds SET status='closed' WHERE game_type_id=$1 AND sequence=1`, gt)
	check(0)
	exec(`UPDATE rounds SET status='settling' WHERE game_type_id=$1 AND sequence=1`, gt)
	check(0)
	exec(`UPDATE rounds SET status='settled' WHERE game_type_id=$1 AND sequence=1`, gt)
	check(2)
	exec(`UPDATE rounds SET status='cancelled' WHERE game_type_id=$1 AND sequence=2`, gt)
	check(3)
}
