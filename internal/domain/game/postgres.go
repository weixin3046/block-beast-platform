package game

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
func (repository *PostgresRepository) EnsureScheduledRounds(ctx context.Context, now time.Time, tronHeight int64, tronBlockAt time.Time) (int, error) {
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
		WHERE gt.enabled=true
		  AND (
		    EXISTS(SELECT 1 FROM game_rooms gr WHERE gr.id=gt.room_id AND gr.enabled=true)
		    OR EXISTS(SELECT 1 FROM game_room_types grt JOIN game_rooms gr ON gr.id=grt.room_id WHERE grt.game_type_id=gt.id AND gr.enabled=true)
		  )
		  AND (
		    (gt.rules->>'source'='tron_hash' AND gt.block_interval > 0)
		    OR gt.rules->>'source'='okx_kline'
		  )
		ORDER BY gt.code`)
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
			referenceTime := now.UTC()
			if !tronBlockAt.IsZero() {
				referenceTime = tronBlockAt.UTC()
			}
			nextResult = referenceTime.Add(time.Duration(blockDistance*3) * time.Second)
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
				INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at,result_at)
				VALUES($1,$2,$3,'open',$4,$5)
				ON CONFLICT(game_type_id,sequence) DO NOTHING`,
				uuid.NewString(), item.id, nextSequence, betClosesAt, nextResult)
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

// HashTrend 返回共享哈希玩法的最近开奖结果。六个赔率房间共用同一期结果，
// 因此走势图只按 hash_5/hash_9/hash_13/hash_17/hash_19 查询。
func (repository *PostgresRepository) HashTrend(ctx context.Context, gameType string, limit int) (HashTrend, error) {
	if limit <= 0 || limit > 200 {
		return HashTrend{}, ErrInvalidTrendLimit
	}
	var exists bool
	if err := repository.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM game_types WHERE code=$1 AND enabled=true AND rules->>'source'='tron_hash')`, gameType).Scan(&exists); err != nil {
		return HashTrend{}, err
	}
	if !exists {
		return HashTrend{}, ErrRoundNotFound
	}
	rows, err := repository.pool.Query(ctx, `
		SELECT sequence,outcome,settled_at
		FROM rounds r JOIN game_types gt ON gt.id=r.game_type_id
		WHERE gt.code=$1 AND r.status='settled' AND r.outcome IS NOT NULL AND r.settled_at IS NOT NULL
		ORDER BY r.sequence DESC LIMIT $2`, gameType, limit)
	if err != nil {
		return HashTrend{}, err
	}
	defer rows.Close()
	result := HashTrend{GameType: gameType, ServerTime: time.Now().UTC(), Items: make([]HashTrendItem, 0), Summary: HashTrendSummary{DigitOmissions: make(map[string]int, 10)}}
	for rows.Next() {
		var sequence int64
		var raw json.RawMessage
		var settledAt time.Time
		if err := rows.Scan(&sequence, &raw, &settledAt); err != nil {
			return HashTrend{}, err
		}
		item, err := parseHashTrendOutcome(sequence, raw, settledAt)
		if err != nil {
			return HashTrend{}, err
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return HashTrend{}, err
	}
	result.Summary = summarizeHashTrend(result.Items)
	return result, nil
}

func parseHashTrendOutcome(sequence int64, raw json.RawMessage, settledAt time.Time) (HashTrendItem, error) {
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return HashTrendItem{}, err
	}
	item := HashTrendItem{Sequence: sequence, Digit: -1, SettledAt: settledAt}
	for _, value := range values {
		switch {
		case len(value) == 1 && value[0] >= '0' && value[0] <= '9':
			item.Digit = int(value[0] - '0')
		case value == "big" || value == "small":
			item.Size = value
		case value == "odd" || value == "even":
			item.Parity = value
		}
	}
	if item.Digit < 0 || item.Size == "" || item.Parity == "" {
		return HashTrendItem{}, errors.New("settled hash round has invalid outcome")
	}
	return item, nil
}

func summarizeHashTrend(items []HashTrendItem) HashTrendSummary {
	summary := HashTrendSummary{DigitOmissions: make(map[string]int, 10)}
	for digit := 0; digit <= 9; digit++ {
		omission := len(items)
		for index, item := range items {
			if item.Digit == digit {
				omission = index
				break
			}
		}
		summary.DigitOmissions[fmt.Sprintf("%d", digit)] = omission
	}
	if len(items) == 0 {
		return summary
	}
	summary.SizeStreak = HashTrendStreak{Value: items[0].Size}
	summary.ParityStreak = HashTrendStreak{Value: items[0].Parity}
	for _, item := range items {
		if item.Size == summary.SizeStreak.Value {
			summary.SizeStreak.Count++
		} else {
			break
		}
	}
	for _, item := range items {
		if item.Parity == summary.ParityStreak.Value {
			summary.ParityStreak.Count++
		} else {
			break
		}
	}
	return summary
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
