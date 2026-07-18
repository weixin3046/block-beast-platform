package game

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/block-beast/platform/internal/domain/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (repository *PostgresRepository) Find(ctx context.Context, roundID string) (Round, error) {
	var round Round
	var outcome json.RawMessage
	err := repository.pool.QueryRow(ctx, `
		SELECT rounds.id, game_types.code, rounds.sequence, rounds.status, rounds.bet_closes_at, rounds.settled_at, rounds.outcome
		FROM rounds
		JOIN game_types ON game_types.id = rounds.game_type_id
		WHERE rounds.id = $1`, roundID).
		Scan(&round.RoundID, &round.GameType, &round.Sequence, &round.Status, &round.BetClosesAt, &round.SettledAt, &outcome)
	if errors.Is(err, pgx.ErrNoRows) {
		return Round{}, ErrRoundNotFound
	}
	if err != nil {
		return Round{}, err
	}
	if len(outcome) > 0 {
		if err := json.Unmarshal(outcome, &round.Outcome); err != nil {
			return Round{}, err
		}
	}
	return round, nil
}

func (repository *PostgresRepository) ListOpen(ctx context.Context, gameType string, limit int) ([]Round, error) {
	if limit <= 0 {
		return []Round{}, nil
	}
	rows, err := repository.pool.Query(ctx, `
		SELECT rounds.id, game_types.code, rounds.sequence, rounds.status, rounds.bet_closes_at
		FROM rounds
		JOIN game_types ON game_types.id = rounds.game_type_id
		WHERE rounds.status = 'open' AND game_types.code = $1
		ORDER BY rounds.bet_closes_at, rounds.id
		LIMIT $2`, gameType, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rounds := make([]Round, 0)
	for rows.Next() {
		var round Round
		if err := rows.Scan(&round.RoundID, &round.GameType, &round.Sequence, &round.Status, &round.BetClosesAt); err != nil {
			return nil, err
		}
		rounds = append(rounds, round)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return rounds, nil
}

func (repository *PostgresRepository) BeginSettlement(ctx context.Context, roundID string) error {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	result, err := tx.Exec(ctx, `
		UPDATE rounds
		SET status = 'settling', version = version + 1
		WHERE id = $1 AND status = 'closed'`, roundID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		var status RoundStatus
		err = tx.QueryRow(ctx, `SELECT status FROM rounds WHERE id = $1`, roundID).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRoundNotFound
		}
		if err != nil {
			return err
		}
		return ErrInvalidTransition
	}

	payload, err := json.Marshal(struct {
		RoundID string `json:"round_id"`
	}{RoundID: roundID})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload)
		VALUES ($1, 'round', $2, $3, $4)`, uuid.NewString(), roundID, events.RoundSettling, payload)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (repository *PostgresRepository) CloseDue(ctx context.Context, now time.Time, limit int) ([]string, error) {
	if limit <= 0 {
		return []string{}, nil
	}
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		UPDATE rounds
		SET status = 'closed', version = version + 1
		WHERE id IN (
			SELECT id
			FROM rounds
			WHERE status = 'open' AND bet_closes_at <= $1
			ORDER BY bet_closes_at, id
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	roundIDs := make([]string, 0)
	for rows.Next() {
		var roundID string
		if err := rows.Scan(&roundID); err != nil {
			return nil, err
		}
		roundIDs = append(roundIDs, roundID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, roundID := range roundIDs {
		payload, err := json.Marshal(struct {
			RoundID string `json:"round_id"`
		}{RoundID: roundID})
		if err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload, occurred_at)
			VALUES ($1, 'round', $2, $3, $4, $5)`, uuid.NewString(), roundID, events.RoundClosed, payload, now)
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return roundIDs, nil
}
