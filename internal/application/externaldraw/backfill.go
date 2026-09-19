package externaldraw

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/block-beast/platform/internal/platform/luludraw"
	"github.com/jackc/pgx/v5"
)

type HistorySource interface {
	History(context.Context, string) ([]luludraw.Event, error)
}

// Backfill uses only existing overdue issues. It never creates betting rounds.
func (s *Service) Backfill(ctx context.Context, history HistorySource) error {
	var enabled bool
	if err := s.pool.QueryRow(ctx, `SELECT enabled FROM lulu_config WHERE singleton=true`).Scan(&enabled); err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	var failures []error
	for _, game := range []string{"lh", "xdy", "race"} {
		events, err := history.History(ctx, game)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s history: %w", game, err))
			continue
		}
		for _, event := range events {
			if event.Game != game {
				failures = append(failures, ErrInvalidEvent)
				continue
			}
			if err := s.confirmBackfill(ctx, event); err != nil {
				failures = append(failures, fmt.Errorf("%s round %s: %w", game, event.Round, err))
			}
		}
	}
	return errors.Join(failures...)
}

func (s *Service) confirmBackfill(ctx context.Context, event luludraw.Event) error {
	names := map[string]string{"lh": GameAngryFeather, "xdy": GameStarSea, "race": GameGreenSprint}
	values := make([]int, len(event.Result))
	for i, v := range event.Result {
		n, err := strconv.Atoi(v)
		if err != nil {
			return ErrInvalidEvent
		}
		values[i] = n
	}
	game, issue, result, err := validateManual(ManualInput{Game: names[event.Game], Issue: event.Round, Result: values, Reason: "LuluAll history"})
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status string
	var saved json.RawMessage
	err = tx.QueryRow(ctx, `SELECT status,outcome FROM external_draw_rounds WHERE source=$1 AND game=$2 AND external_round=$3 FOR UPDATE`, source, game, issue).Scan(&status, &saved)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	// Even a different secondary result must never demote a confirmed primary result.
	if status != "pending" || (len(saved) > 0 && string(saved) != "null") {
		return nil
	}
	var roundStatus string
	var resultAt time.Time
	err = tx.QueryRow(ctx, `SELECT r.status,r.result_at FROM rounds r JOIN game_types g ON g.id=r.game_type_id
 WHERE r.sequence=$1 AND g.rules->>'source'='lulu_ws' AND g.rules->'extras'->>'external_game'=$2 FOR UPDATE OF r`, issue, game).Scan(&roundStatus, &resultAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	// Give the primary WebSocket time to confirm the result before using history.
	if (roundStatus != "open" && roundStatus != "closed") || resultAt.Add(30*time.Second).After(time.Now()) {
		return nil
	}
	encoded, _ := json.Marshal(result)
	_, err = tx.Exec(ctx, `UPDATE external_draw_rounds SET outcome=$4,status='confirmed',result_received_at=now(),updated_at=now()
 WHERE source=$1 AND game=$2 AND external_round=$3`, source, game, issue, encoded)
	if err != nil {
		return err
	}
	event.Kind = "luluall_history"
	event.CloseAt = nil
	event.Result = result
	if err = auditDrawResult(ctx, tx, "lulu_draw.result_backfilled", event, issue, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
