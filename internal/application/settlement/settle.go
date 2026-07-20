package settlement

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/block-beast/platform/internal/domain/events"
	"github.com/block-beast/platform/internal/domain/game"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrInvalidOutcome = errors.New("outcome must contain at least one value")
var ErrInvalidPayoutMultiplier = errors.New("payout multiplier must be positive")
var ErrSettlementOutcomeMismatch = errors.New("settlement outcome does not match the finalized round")

// SettlementResult summarizes a completed (or idempotently repeated) settlement.
type SettlementResult struct {
	RoundID      string    `json:"round_id"`
	Outcome      []string  `json:"outcome"`
	WonBetCount  int       `json:"won_bet_count"`
	LostBetCount int       `json:"lost_bet_count"`
	PayoutMinor  int64     `json:"payout_minor"`
	SettledAt    time.Time `json:"settled_at"`
}

// SettleRound atomically marks all accepted bets as won or lost, credits winning
// wallets, writes ledger entries, finalizes the round, and records an outbox event.
// A selection wins when one of its JSON string values is present in outcome. This
// intentionally supports simple selections such as {"color":"red"} while game
// specific settlement code can still store richer selection objects.
func (service *Service) SettleRound(ctx context.Context, roundID string, outcome []string, payoutMultiplier int64) (SettlementResult, error) {
	if len(outcome) == 0 || containsEmpty(outcome) {
		return SettlementResult{}, ErrInvalidOutcome
	}
	if payoutMultiplier <= 0 {
		return SettlementResult{}, ErrInvalidPayoutMultiplier
	}

	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return SettlementResult{}, err
	}
	defer tx.Rollback(ctx)

	var status game.RoundStatus
	var savedOutcome json.RawMessage
	var settledAt *time.Time
	err = tx.QueryRow(ctx, `SELECT status, outcome, settled_at FROM rounds WHERE id = $1 FOR UPDATE`, roundID).Scan(&status, &savedOutcome, &settledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SettlementResult{}, game.ErrRoundNotFound
	}
	if err != nil {
		return SettlementResult{}, err
	}
	if status == game.RoundSettled {
		var existingOutcome []string
		if err := json.Unmarshal(savedOutcome, &existingOutcome); err != nil {
			return SettlementResult{}, err
		}
		if !sameStrings(existingOutcome, outcome) {
			return SettlementResult{}, ErrSettlementOutcomeMismatch
		}
		return settledResult(ctx, tx, roundID, savedOutcome, settledAt)
	}
	if status == game.RoundClosed {
		if _, err := tx.Exec(ctx, `UPDATE rounds SET status = 'settling', version = version + 1 WHERE id = $1`, roundID); err != nil {
			return SettlementResult{}, err
		}
	} else if status != game.RoundSettling {
		return SettlementResult{}, game.ErrInvalidTransition
	}

	type acceptedBet struct {
		betID, walletID string
		selection       json.RawMessage
		stake           int64
	}
	rows, err := tx.Query(ctx, `
		SELECT id, wallet_id, selection, stake_minor
		FROM bets
		WHERE round_id = $1 AND status = 'accepted'
		ORDER BY wallet_id, id
		FOR UPDATE`, roundID)
	if err != nil {
		return SettlementResult{}, err
	}
	bets := make([]acceptedBet, 0)
	for rows.Next() {
		var bet acceptedBet
		if err := rows.Scan(&bet.betID, &bet.walletID, &bet.selection, &bet.stake); err != nil {
			rows.Close()
			return SettlementResult{}, err
		}
		bets = append(bets, bet)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return SettlementResult{}, err
	}
	rows.Close()

	winningValues := make(map[string]struct{}, len(outcome))
	for _, value := range outcome {
		winningValues[value] = struct{}{}
	}
	result := SettlementResult{RoundID: roundID, Outcome: append([]string(nil), outcome...), SettledAt: time.Now().UTC()}
	for _, bet := range bets {
		won := selectionWins(bet.selection, winningValues)
		if !won {
			if _, err := tx.Exec(ctx, `UPDATE bets SET status = 'lost', settled_at = $2 WHERE id = $1`, bet.betID, result.SettledAt); err != nil {
				return SettlementResult{}, err
			}
			result.LostBetCount++
			continue
		}
		if bet.stake > math.MaxInt64/payoutMultiplier {
			return SettlementResult{}, ErrInvalidPayoutMultiplier
		}
		payout := bet.stake * payoutMultiplier
		var availableMinor int64
		if err := tx.QueryRow(ctx, `SELECT available_minor FROM wallets WHERE id = $1 FOR UPDATE`, bet.walletID).Scan(&availableMinor); err != nil {
			return SettlementResult{}, err
		}
		if availableMinor > math.MaxInt64-payout {
			return SettlementResult{}, ErrInvalidPayoutMultiplier
		}
		availableMinor += payout
		if _, err := tx.Exec(ctx, `UPDATE wallets SET available_minor = $2, version = version + 1, updated_at = $3 WHERE id = $1`, bet.walletID, availableMinor, result.SettledAt); err != nil {
			return SettlementResult{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE bets SET status = 'won', payout_minor = $2, settled_at = $3 WHERE id = $1`, bet.betID, payout, result.SettledAt); err != nil {
			return SettlementResult{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ledger_entries (id, wallet_id, business_type, business_id, entry_type, amount_minor, balance_after_minor) VALUES ($1, $2, 'settlement', $3, 'settlement_credit', $4, $5)`, uuid.NewString(), bet.walletID, bet.betID, payout, availableMinor); err != nil {
			return SettlementResult{}, err
		}
		result.WonBetCount++
		result.PayoutMinor += payout
	}

	encodedOutcome, err := json.Marshal(outcome)
	if err != nil {
		return SettlementResult{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE rounds SET status = 'settled', outcome = $2, settled_at = $3, version = version + 1 WHERE id = $1`, roundID, encodedOutcome, result.SettledAt); err != nil {
		return SettlementResult{}, err
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return SettlementResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload, occurred_at) VALUES ($1, 'round', $2, $3, $4, $5)`, uuid.NewString(), roundID, events.RoundSettled, payload, result.SettledAt); err != nil {
		return SettlementResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SettlementResult{}, err
	}
	return result, nil
}

func settledResult(ctx context.Context, tx pgx.Tx, roundID string, rawOutcome json.RawMessage, settledAt *time.Time) (SettlementResult, error) {
	var outcome []string
	if err := json.Unmarshal(rawOutcome, &outcome); err != nil {
		return SettlementResult{}, err
	}
	result := SettlementResult{RoundID: roundID, Outcome: outcome}
	if settledAt != nil {
		result.SettledAt = *settledAt
	}
	var wonCount, lostCount int64
	err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status = 'won'), count(*) FILTER (WHERE status = 'lost'), coalesce(sum(payout_minor) FILTER (WHERE status = 'won'), 0) FROM bets WHERE round_id = $1`, roundID).Scan(&wonCount, &lostCount, &result.PayoutMinor)
	if err != nil {
		return SettlementResult{}, err
	}
	result.WonBetCount = int(wonCount)
	result.LostBetCount = int(lostCount)
	if err := tx.Commit(ctx); err != nil {
		return SettlementResult{}, err
	}
	return result, nil
}

func containsEmpty(values []string) bool {
	for _, value := range values {
		if value == "" {
			return true
		}
	}
	return false
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func selectionWins(selection json.RawMessage, winningValues map[string]struct{}) bool {
	var value any
	if json.Unmarshal(selection, &value) != nil {
		return false
	}
	values := make([]string, 0)
	collectStrings(value, &values)
	for _, value := range values {
		if _, ok := winningValues[value]; ok {
			return true
		}
	}
	return false
}

func collectStrings(value any, values *[]string) {
	switch typed := value.(type) {
	case string:
		*values = append(*values, typed)
	case []any:
		for _, item := range typed {
			collectStrings(item, values)
		}
	case map[string]any:
		for _, item := range typed {
			collectStrings(item, values)
		}
	}
}
