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

// EnsureScheduledRounds keeps three future rounds available for every enabled
// room game. A TRON round sequence is its immutable target block height, selected
// from the next block_interval multiple after tronHeight. K-line games settle on
// minute boundaries.
func (repository *PostgresRepository) EnsureScheduledRounds(ctx context.Context, now time.Time, tronHeight int64) (int, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('game-round-scheduler',0))`); err != nil {
		return 0, err
	}
	rows, err := tx.Query(ctx, `
		SELECT gt.id::text,COALESCE(gt.block_interval,0),gt.rules->>'source',gt.close_before_seconds
		FROM game_types gt
		JOIN game_rooms gr ON gr.id=gt.room_id
		WHERE gt.enabled=true AND gr.enabled=true
		  AND (
		    (gt.rules->>'source'='tron_hash' AND gt.block_interval > 0)
		    OR gt.rules->>'source'='okx_kline'
		  )
		ORDER BY gr.sort_order,gt.code`)
	if err != nil {
		return 0, err
	}
	type scheduledType struct {
		id          string
		interval    int
		source      string
		closeBefore time.Duration
	}
	types := make([]scheduledType, 0)
	for rows.Next() {
		var item scheduledType
		var closeBeforeSeconds int
		if err := rows.Scan(&item.id, &item.interval, &item.source, &closeBeforeSeconds); err != nil {
			rows.Close()
			return 0, err
		}
		item.closeBefore = time.Duration(closeBeforeSeconds) * time.Second
		types = append(types, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	created := 0
	for _, item := range types {
		cycle := time.Minute
		if item.source == "tron_hash" {
			cycle = time.Duration(item.interval*3) * time.Second
		}
		var lastSequence int64
		var lastResult *time.Time
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(max(sequence),0),max(result_at)
			FROM rounds WHERE game_type_id=$1`, item.id).
			Scan(&lastSequence, &lastResult); err != nil {
			return created, err
		}
		nextSequence := lastSequence + 1
		nextResult := now.UTC().Add(cycle)
		if item.source == "tron_hash" {
			if tronHeight <= 0 {
				continue
			}
			interval := int64(item.interval)
			nextSequence = nextTronTarget(tronHeight, interval)
			if lastSequence >= nextSequence {
				nextSequence = lastSequence + interval
			}
			blockDistance := nextSequence - tronHeight
			nextResult = now.UTC().Add(time.Duration(blockDistance*3) * time.Second)
			if !nextResult.Add(-item.closeBefore).After(now.UTC()) {
				nextSequence += interval
				nextResult = nextResult.Add(cycle)
			}
		}
		if item.source == "okx_kline" {
			nextResult = now.UTC().Truncate(time.Minute).Add(time.Minute)
			if !nextResult.Add(-item.closeBefore).After(now.UTC()) {
				nextResult = nextResult.Add(time.Minute)
			}
		}
		if lastResult != nil && item.source == "okx_kline" {
			nextResult = lastResult.UTC().Add(cycle)
			if !nextResult.Add(-item.closeBefore).After(now) {
				skipped := int64(now.Sub(nextResult.Add(-item.closeBefore))/cycle) + 1
				nextResult = nextResult.Add(time.Duration(skipped) * cycle)
				nextSequence += skipped
			}
		}
		var futureCount int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM rounds
			WHERE game_type_id=$1 AND status='open' AND result_at>$2`,
			item.id, now.UTC()).Scan(&futureCount); err != nil {
			return created, err
		}
		for futureCount < 3 {
			betClosesAt := nextResult.Add(-item.closeBefore)
			command, err := tx.Exec(ctx, `
				INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at)
				VALUES($1,$2,$3,'open',$4)
				ON CONFLICT(game_type_id,sequence) DO NOTHING`,
				uuid.NewString(), item.id, nextSequence, betClosesAt)
			if err != nil {
				return created, err
			}
			if command.RowsAffected() == 1 {
				created++
				futureCount++
			}
			if item.source == "tron_hash" {
				nextSequence += int64(item.interval)
			} else {
				nextSequence++
			}
			nextResult = nextResult.Add(cycle)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return created, err
	}
	return created, nil
}

func nextTronTarget(currentHeight, interval int64) int64 {
	if currentHeight <= 0 || interval <= 0 {
		return 0
	}
	return (currentHeight/interval + 1) * interval
}

func (repository *PostgresRepository) Find(ctx context.Context, roundID string) (Round, error) {
	var round Round
	var outcome json.RawMessage
	err := repository.pool.QueryRow(ctx, `
		SELECT rounds.id, game_types.code, rounds.sequence, rounds.status, rounds.bet_closes_at, rounds.result_at, rounds.settled_at, rounds.outcome
		FROM rounds
		JOIN game_types ON game_types.id = rounds.game_type_id
		WHERE rounds.id = $1`, roundID).
		Scan(&round.RoundID, &round.GameType, &round.Sequence, &round.Status, &round.BetClosesAt, &round.ResultAt, &round.SettledAt, &outcome)
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
		SELECT rounds.id, game_types.code, rounds.sequence, rounds.status, rounds.bet_closes_at, rounds.result_at
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
		if err := rows.Scan(&round.RoundID, &round.GameType, &round.Sequence, &round.Status, &round.BetClosesAt, &round.ResultAt); err != nil {
			return nil, err
		}
		rounds = append(rounds, round)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return rounds, nil
}

func (repository *PostgresRepository) State(ctx context.Context, gameType string) (RoundState, error) {
	var state RoundState
	current, err := repository.findGameTypeRound(
		ctx,
		gameType,
		"status IN ('open','closed','settling')",
		"result_at ASC",
	)
	if err != nil && !errors.Is(err, ErrRoundNotFound) {
		return state, err
	}
	if err == nil {
		state.Current = &current
	}
	previous, err := repository.findGameTypeRound(ctx, gameType, "status = 'settled'", "result_at DESC")
	if err != nil && !errors.Is(err, ErrRoundNotFound) {
		return state, err
	}
	if err == nil {
		state.Previous = &previous
	}
	return state, nil
}

func (repository *PostgresRepository) findGameTypeRound(ctx context.Context, gameType, statusClause, orderBy string) (Round, error) {
	query := `
		SELECT rounds.id,game_types.code,rounds.sequence,rounds.status,
			rounds.bet_closes_at,rounds.result_at,rounds.settled_at,rounds.outcome
		FROM rounds
		JOIN game_types ON game_types.id=rounds.game_type_id
		WHERE game_types.code=$1 AND ` + statusClause + `
		ORDER BY ` + orderBy + `,rounds.id
		LIMIT 1`
	var round Round
	var outcome json.RawMessage
	err := repository.pool.QueryRow(ctx, query, gameType).Scan(
		&round.RoundID, &round.GameType, &round.Sequence, &round.Status,
		&round.BetClosesAt, &round.ResultAt, &round.SettledAt, &outcome,
	)
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
