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
		if err = service.confirmResult(ctx, tx, event, round); err != nil {
			return err
		}
		// Some upstream game messages contain only the official result and no
		// advance countdown. Preserve those issues as closed, non-bettable
		// rounds so an unavailable countdown frame cannot make history jump.
		if err = service.createResultRounds(ctx, tx, event.Game, round, event.CloseAt); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (service *Service) createResultRounds(ctx context.Context, tx pgx.Tx, game string, sequence int64, closeAt *time.Time) error {
	closedAt, needed := resultRoundClosedAt(time.Now().UTC(), closeAt)
	if !needed {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT id::text FROM game_types
		WHERE enabled=true AND rules->>'source'='lulu_ws' AND rules->'extras'->>'external_game'=$1`, game)
	if err != nil {
		return err
	}
	gameTypeIDs := make([]string, 0)
	for rows.Next() {
		var gameTypeID string
		if err = rows.Scan(&gameTypeID); err != nil {
			rows.Close()
			return err
		}
		gameTypeIDs = append(gameTypeIDs, gameTypeID)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, gameTypeID := range gameTypeIDs {
		if _, err = tx.Exec(ctx, `INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at,result_at)
			VALUES($1,$2,$3,'closed',$4,$4) ON CONFLICT(game_type_id,sequence) DO NOTHING`,
			uuid.NewString(), gameTypeID, sequence, closedAt); err != nil {
			return err
		}
	}
	return nil
}

func resultRoundClosedAt(now time.Time, closeAt *time.Time) (time.Time, bool) {
	if closeAt == nil {
		return now, true
	}
	if closeAt.After(now) {
		return time.Time{}, false
	}
	return closeAt.UTC(), true
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
	gameTypeIDs := make([]string, 0)
	for rows.Next() {
		var gameTypeID string
		if err = rows.Scan(&gameTypeID); err != nil {
			rows.Close()
			return err
		}
		gameTypeIDs = append(gameTypeIDs, gameTypeID)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	// PostgreSQL does not permit another statement on the transaction while the
	// result set is open. Close the query before inserting each game round.
	rows.Close()
	for _, gameTypeID := range gameTypeIDs {
		if _, err = tx.Exec(ctx, `INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at,result_at)
			VALUES($1,$2,$3,'open',$4,$5) ON CONFLICT(game_type_id,sequence) DO NOTHING`,
			uuid.NewString(), gameTypeID, sequence, betClosesAt, closeAt.UTC()); err != nil {
			return err
		}
	}
	return nil
}

func (service *Service) confirmResult(ctx context.Context, tx pgx.Tx, event luludraw.Event, sequence int64) error {
	game, result := event.Game, event.Result
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode external draw result: %w", err)
	}
	var saved json.RawMessage
	var status string
	var closedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT COALESCE(outcome,'null'::jsonb),status,close_at FROM external_draw_rounds
		WHERE source=$1 AND game=$2 AND external_round=$3 FOR UPDATE`, source, game, sequence).Scan(&saved, &status, &closedAt)
	if err != nil {
		return err
	}
	if status == "confirmed" || status == "conflict" {
		if !sameDrawResult(saved, result) {
			if isStarSeaMultiKillWindow(event, closedAt) {
				if isLowerPriorityStarSeaResult(saved, event) {
					return auditDrawResult(ctx, tx, "lulu_draw.result_ignored_lower_priority", event, sequence, saved)
				}
				if isAuthoritativeStarSeaMultiKill(saved, event) {
					_, err = tx.Exec(ctx, `UPDATE external_draw_rounds
						SET outcome=$4,status='confirmed',conflict_outcome=NULL,result_received_at=now(),updated_at=now()
						WHERE source=$1 AND game=$2 AND external_round=$3`, source, game, sequence, encoded)
					if err != nil {
						return err
					}
					return auditDrawResult(ctx, tx, "lulu_draw.result_multikill_override", event, sequence, saved)
				}
			}
			if status == "confirmed" {
				_, err = tx.Exec(ctx, `UPDATE external_draw_rounds SET status='conflict',conflict_outcome=$4,updated_at=now()
					WHERE source=$1 AND game=$2 AND external_round=$3`, source, game, sequence, encoded)
				if err != nil {
					return err
				}
			}
			return auditDrawResult(ctx, tx, "lulu_draw.result_conflict", event, sequence, saved)
		}
		return nil
	}
	_, err = tx.Exec(ctx, `UPDATE external_draw_rounds SET outcome=$4,status='confirmed',result_received_at=now(),updated_at=now()
		WHERE source=$1 AND game=$2 AND external_round=$3`, source, game, sequence, encoded)
	if err != nil {
		return err
	}
	return auditDrawResult(ctx, tx, "lulu_draw.result_confirmed", event, sequence, nil)
}

var chinaLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

// Star Sea uses the full killedRooms result as the official outcome from
// 20:00 through 20:59 China time. A later failedRoomId is a single-room event,
// not a contradictory draw result.
func isStarSeaMultiKillWindow(event luludraw.Event, closedAt *time.Time) bool {
	if event.Game != "xdy" {
		return false
	}
	when := time.Now()
	if closedAt != nil {
		when = *closedAt
	} else if event.CloseAt != nil {
		when = *event.CloseAt
	}
	hour := when.In(chinaLocation).Hour()
	return hour == 20
}

func isLowerPriorityStarSeaResult(saved json.RawMessage, event luludraw.Event) bool {
	if event.ResultField != "failedRoomId" || len(event.Result) != 1 {
		return false
	}
	var savedRooms []string
	if json.Unmarshal(saved, &savedRooms) != nil || len(savedRooms) < 2 {
		return false
	}
	for _, room := range savedRooms {
		if room == event.Result[0] {
			return true
		}
	}
	return false
}

func isAuthoritativeStarSeaMultiKill(saved json.RawMessage, event luludraw.Event) bool {
	if event.ResultField != "killedRooms" && event.ResultField != "result.list[].fail" {
		return false
	}
	if len(event.Result) < 2 {
		return false
	}
	var savedRooms []string
	if json.Unmarshal(saved, &savedRooms) != nil || len(savedRooms) != 1 {
		return false
	}
	for _, room := range event.Result {
		if room == savedRooms[0] {
			return true
		}
	}
	return false
}

// Persist only result metadata, never credentials or raw provider messages.
// The draw row is already locked, so identical retries cannot duplicate audits.
func auditDrawResult(ctx context.Context, tx pgx.Tx, action string, event luludraw.Event, sequence int64, saved json.RawMessage) error {
	transport := "websocket"
	if event.Kind == "luluall_history" {
		transport = "luluall_http"
	}
	payload, err := json.Marshal(map[string]any{
		"game": event.Game, "external_round": sequence,
		"transport": transport, "event_type": event.Kind, "result_field": event.ResultField,
		"incoming_outcome": event.Result, "saved_outcome": saved,
	})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,action,target_type,target_id,payload)
		SELECT $1,$2,'external_draw_round',$3,$4::jsonb
		WHERE NOT EXISTS (SELECT 1 FROM audit_logs WHERE action=$2 AND target_id=$3 AND payload=$4::jsonb)`,
		uuid.NewString(), action, source+":"+event.Game+":"+strconv.FormatInt(sequence, 10), payload)
	return err
}

func validGame(game string) bool { return game == "lh" || game == "xdy" || game == "race" }

func sameDrawResult(saved json.RawMessage, result []string) bool {
	var values []string
	if json.Unmarshal(saved, &values) != nil || len(values) == 0 || len(values) != len(result) {
		return false
	}
	counts := make(map[string]int, len(values))
	for _, value := range values {
		counts[value]++
	}
	for _, value := range result {
		counts[value]--
		if counts[value] < 0 {
			return false
		}
	}
	return true
}
