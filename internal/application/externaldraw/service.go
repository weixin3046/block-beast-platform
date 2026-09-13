// Package externaldraw persists trusted upstream draw metadata and creates platform rounds.
package externaldraw

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/block-beast/platform/internal/platform/luludraw"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInvalidEvent = errors.New("invalid external draw event")

const source = "lulu_ws"

type Service struct {
	pool               *pgxpool.Pool
	closeBeforeSeconds int
}

func NewService(pool *pgxpool.Pool, closeBeforeSeconds int) *Service {
	if closeBeforeSeconds < 0 {
		closeBeforeSeconds = 0
	}
	return &Service{pool: pool, closeBeforeSeconds: closeBeforeSeconds}
}

func (service *Service) Handle(ctx context.Context, event luludraw.Event) error {
	round, err := strconv.ParseInt(event.Round, 10, 64)
	if err != nil || round <= 0 || !validGame(event.Game) || service.pool == nil {
		return ErrInvalidEvent
	}
	if event.CloseAt == nil && len(event.Result) == 0 {
		return nil
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO external_draw_rounds(source,game,external_round,close_at)
		VALUES($1,$2,$3,$4) ON CONFLICT(source,game,external_round) DO UPDATE
		SET close_at=COALESCE(EXCLUDED.close_at,external_draw_rounds.close_at),updated_at=now()`,
		source, event.Game, round, event.CloseAt); err != nil {
		return err
	}
	if event.CloseAt != nil {
		if err = service.createRounds(ctx, tx, event.Game, round, *event.CloseAt); err != nil {
			return err
		}
	}
	if len(event.Result) > 0 {
		if err = service.confirmResult(ctx, tx, event.Game, round, event.Result); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (service *Service) createRounds(ctx context.Context, tx pgx.Tx, game string, sequence int64, closeAt time.Time) error {
	betClosesAt := closeAt.UTC().Add(-time.Duration(service.closeBeforeSeconds) * time.Second)
	if !betClosesAt.After(time.Now().UTC()) {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT id::text FROM game_types
		WHERE enabled=true AND rules->>'source'='lulu_ws' AND rules->'extras'->>'external_game'=$1`, game)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var gameTypeID string
		if err = rows.Scan(&gameTypeID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at,result_at)
			VALUES($1,$2,$3,'open',$4,$5) ON CONFLICT(game_type_id,sequence) DO NOTHING`,
			uuid.NewString(), gameTypeID, sequence, betClosesAt, closeAt.UTC()); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (service *Service) confirmResult(ctx context.Context, tx pgx.Tx, game string, sequence int64, result []string) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode external draw result: %w", err)
	}
	var saved json.RawMessage
	var status string
	err = tx.QueryRow(ctx, `SELECT COALESCE(outcome,'null'::jsonb),status FROM external_draw_rounds
		WHERE source=$1 AND game=$2 AND external_round=$3 FOR UPDATE`, source, game, sequence).Scan(&saved, &status)
	if err != nil {
		return err
	}
	if status == "confirmed" {
		if string(saved) != string(encoded) {
			_, err = tx.Exec(ctx, `UPDATE external_draw_rounds SET status='conflict',conflict_outcome=$4,updated_at=now()
				WHERE source=$1 AND game=$2 AND external_round=$3`, source, game, sequence, encoded)
		}
		return err
	}
	if status == "conflict" {
		return nil
	}
	_, err = tx.Exec(ctx, `UPDATE external_draw_rounds SET outcome=$4,status='confirmed',result_received_at=now(),updated_at=now()
		WHERE source=$1 AND game=$2 AND external_round=$3`, source, game, sequence, encoded)
	return err
}

func validGame(game string) bool { return game == "lh" || game == "xdy" || game == "race" }
