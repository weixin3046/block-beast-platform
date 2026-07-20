package events

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresOutbox struct {
	pool *pgxpool.Pool
}

func NewPostgresOutbox(pool *pgxpool.Pool) *PostgresOutbox {
	return &PostgresOutbox{pool: pool}
}

func (outbox *PostgresOutbox) Append(event Event) error {
	_, err := outbox.pool.Exec(context.Background(), `
		INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO NOTHING`, event.ID, event.AggregateType, event.AggregateID, event.Type, event.Payload, event.OccurredAt)
	return err
}

func (outbox *PostgresOutbox) Pending(limit int) []Event {
	ctx := context.Background()
	query := `
		SELECT id, event_type, aggregate_type, aggregate_id, occurred_at, payload
		FROM outbox_events
		WHERE published_at IS NULL AND failed_at IS NULL
		ORDER BY occurred_at, id`
	var rows pgx.Rows
	var err error
	if limit > 0 {
		rows, err = outbox.pool.Query(ctx, query+` LIMIT $1`, limit)
	} else {
		rows, err = outbox.pool.Query(ctx, query)
	}
	if err != nil {
		return nil
	}
	defer rows.Close()

	events := make([]Event, 0)
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.ID, &event.Type, &event.AggregateType, &event.AggregateID, &event.OccurredAt, &event.Payload); err != nil {
			return nil
		}
		events = append(events, event)
	}
	if rows.Err() != nil {
		return nil
	}
	return events
}

func (outbox *PostgresOutbox) MarkPublished(eventID string, publishedAt time.Time) error {
	ctx := context.Background()
	result, err := outbox.pool.Exec(ctx, `
		UPDATE outbox_events
		SET published_at = $2, attempts = attempts + 1
		WHERE id = $1 AND published_at IS NULL`, eventID, publishedAt)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 1 {
		return nil
	}

	err = outbox.pool.QueryRow(ctx, `SELECT 1 FROM outbox_events WHERE id = $1`, eventID).Scan(new(int))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrEventNotFound
	}
	return err
}

func (outbox *PostgresOutbox) RecordFailure(eventID string, failedAt time.Time, reason string, maxAttempts int) (bool, error) {
	if maxAttempts <= 0 {
		return false, errors.New("max attempts must be positive")
	}
	if len(reason) > 1024 {
		reason = reason[:1024]
	}
	ctx := context.Background()
	var deadLettered bool
	err := outbox.pool.QueryRow(ctx, `
		UPDATE outbox_events
		SET attempts = attempts + 1,
			failure_reason = $2,
			failed_at = CASE WHEN attempts + 1 >= $3 THEN $4 ELSE NULL END
		WHERE id = $1 AND published_at IS NULL AND failed_at IS NULL
		RETURNING failed_at IS NOT NULL`, eventID, reason, maxAttempts, failedAt).Scan(&deadLettered)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		err = outbox.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM outbox_events WHERE id = $1)`, eventID).Scan(&exists)
		if err != nil {
			return false, err
		}
		if !exists {
			return false, ErrEventNotFound
		}
		return false, nil
	}
	return deadLettered, err
}

var _ Outbox = (*PostgresOutbox)(nil)
