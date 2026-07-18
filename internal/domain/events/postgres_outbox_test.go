package events

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresOutboxPublishesEachEventOnce(t *testing.T) {
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

	event := Event{
		ID:            uuid.NewString(),
		Type:          BetPlaced,
		AggregateType: "bet",
		AggregateID:   uuid.NewString(),
		OccurredAt:    time.Now().UTC(),
		Payload:       []byte(`{"bet_id":"test"}`),
	}
	outbox := NewPostgresOutbox(pool)
	if err := outbox.Append(event); err != nil {
		t.Fatalf("append event: %v", err)
	}
	if err := outbox.Append(event); err != nil {
		t.Fatalf("append duplicate event: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM outbox_events WHERE id = $1`, event.ID)
	})

	var eventCount int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE id = $1`, event.ID).Scan(&eventCount)
	if err != nil {
		t.Fatalf("count appended event: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("event count = %d, want 1", eventCount)
	}
	if !containsEvent(outbox.Pending(100), event.ID) {
		t.Fatal("pending events must include the appended event")
	}

	publishedAt := time.Now().UTC()
	if err := outbox.MarkPublished(event.ID, publishedAt); err != nil {
		t.Fatalf("mark published: %v", err)
	}
	if err := outbox.MarkPublished(event.ID, publishedAt); err != nil {
		t.Fatalf("repeat mark published: %v", err)
	}
	if containsEvent(outbox.Pending(100), event.ID) {
		t.Fatal("published event must not be pending")
	}

	var attempts int
	err = pool.QueryRow(ctx, `SELECT attempts FROM outbox_events WHERE id = $1`, event.ID).Scan(&attempts)
	if err != nil {
		t.Fatalf("read attempts: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("publish attempts = %d, want 1", attempts)
	}

	if err := outbox.MarkPublished(uuid.NewString(), publishedAt); !errors.Is(err, ErrEventNotFound) {
		t.Fatalf("unknown event error = %v, want event not found", err)
	}
}

func containsEvent(events []Event, eventID string) bool {
	for _, event := range events {
		if event.ID == eventID {
			return true
		}
	}
	return false
}
