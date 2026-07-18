package game

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/domain/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresRepositoryCloseDue(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)

	gameTypeID := uuid.NewString()
	dueRoundID := uuid.NewString()
	futureRoundID := uuid.NewString()
	_, err = pool.Exec(ctx, `INSERT INTO game_types (id, code, name, rules) VALUES ($1, $2, $3, '{}')`, gameTypeID, "test-"+gameTypeID, "test game")
	if err != nil {
		t.Fatalf("create game type: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO rounds (id, game_type_id, sequence, status, bet_closes_at) VALUES ($1, $2, 1, 'open', $3), ($4, $2, 2, 'open', $5)`, dueRoundID, gameTypeID, time.Now().UTC().Add(-time.Second), futureRoundID, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("create rounds: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM outbox_events WHERE aggregate_id IN ($1, $2)`, dueRoundID, futureRoundID)
		_, _ = pool.Exec(ctx, `DELETE FROM rounds WHERE game_type_id = $1`, gameTypeID)
		_, _ = pool.Exec(ctx, `DELETE FROM game_types WHERE id = $1`, gameTypeID)
	})

	repository := NewPostgresRepository(pool)
	found, err := repository.Find(ctx, dueRoundID)
	if err != nil {
		t.Fatalf("find due round: %v", err)
	}
	if found.RoundID != dueRoundID || found.GameType != "test-"+gameTypeID || found.Status != RoundOpen {
		t.Fatalf("found round = %#v", found)
	}

	closed, err := repository.CloseDue(ctx, time.Now().UTC(), 10)
	if err != nil {
		t.Fatalf("close due rounds: %v", err)
	}
	if len(closed) != 1 || closed[0] != dueRoundID {
		t.Fatalf("closed rounds = %#v, want %q", closed, dueRoundID)
	}

	var dueStatus, futureStatus string
	var dueVersion int64
	err = pool.QueryRow(ctx, `SELECT status, version FROM rounds WHERE id = $1`, dueRoundID).Scan(&dueStatus, &dueVersion)
	if err != nil {
		t.Fatalf("read due round: %v", err)
	}
	err = pool.QueryRow(ctx, `SELECT status FROM rounds WHERE id = $1`, futureRoundID).Scan(&futureStatus)
	if err != nil {
		t.Fatalf("read future round: %v", err)
	}
	if dueStatus != string(RoundClosed) || dueVersion != 1 {
		t.Fatalf("due round = status %q, version %d; want closed, 1", dueStatus, dueVersion)
	}
	if futureStatus != string(RoundOpen) {
		t.Fatalf("future round status = %q, want open", futureStatus)
	}
	openRounds, err := repository.ListOpen(ctx, "test-"+gameTypeID, 10)
	if err != nil {
		t.Fatalf("list open rounds: %v", err)
	}
	if len(openRounds) != 1 || openRounds[0].RoundID != futureRoundID {
		t.Fatalf("open rounds = %#v, want future round %q", openRounds, futureRoundID)
	}
	var eventCount int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id = $1 AND event_type = $2`, dueRoundID, events.RoundClosed).Scan(&eventCount)
	if err != nil {
		t.Fatalf("count round closed events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("round closed events = %d, want 1", eventCount)
	}
	if err := repository.BeginSettlement(ctx, dueRoundID); err != nil {
		t.Fatalf("begin settlement: %v", err)
	}
	err = pool.QueryRow(ctx, `SELECT status, version FROM rounds WHERE id = $1`, dueRoundID).Scan(&dueStatus, &dueVersion)
	if err != nil {
		t.Fatalf("read settling round: %v", err)
	}
	if dueStatus != string(RoundSettling) || dueVersion != 2 {
		t.Fatalf("settling round = status %q, version %d; want settling, 2", dueStatus, dueVersion)
	}
	err = pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id = $1 AND event_type = $2`, dueRoundID, events.RoundSettling).Scan(&eventCount)
	if err != nil {
		t.Fatalf("count round settling events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("round settling events = %d, want 1", eventCount)
	}
	if err := repository.BeginSettlement(ctx, dueRoundID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("repeat begin settlement error = %v, want invalid transition", err)
	}
}
