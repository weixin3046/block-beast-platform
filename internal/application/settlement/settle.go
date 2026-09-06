package settlement

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"time"

	"github.com/block-beast/platform/internal/application/rebate"
	"github.com/block-beast/platform/internal/domain/events"
	"github.com/block-beast/platform/internal/domain/game"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrInvalidOutcome = errors.New("outcome must contain at least one value")
var ErrOutcomeOutsidePool = errors.New("outcome contains values outside the game rules pool")
var ErrPayoutOverflow = errors.New("payout would overflow the wallet balance")
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
// 赔率与中奖判定由玩法规则 game.Rules 决定；开奖结果必须全部落在规则的结果池内。
func (service *Service) SettleRound(ctx context.Context, roundID string, outcome []string, rules game.Rules) (SettlementResult, error) {
	if len(outcome) == 0 || containsEmpty(outcome) {
		return SettlementResult{}, ErrInvalidOutcome
	}
	if err := rules.Validate(); err != nil {
		return SettlementResult{}, err
	}
	if !withinPool(outcome, rules.Outcomes) {
		return SettlementResult{}, ErrOutcomeOutsidePool
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
		legacyAgentID                     string
		rebateVersion                     int
		allocations                       []rebate.Allocation
		simulated                         bool
		betID, walletID, userID, currency string
		playMode                          string
		placedAt                          time.Time
		selection                         json.RawMessage
		stake                             int64
		payoutMultiplier, payoutDivisor   *int64
	}
	rows, err := tx.Query(ctx, `
		SELECT bets.id, bets.wallet_id, bets.user_id, wallets.currency,COALESCE(bets.play_mode,''),
			bets.selection,bets.stake_minor,bets.payout_multiplier_snapshot,bets.payout_divisor_snapshot,bets.is_simulated,bets.created_at,bets.rebate_version
		FROM bets JOIN wallets ON wallets.id=bets.wallet_id
		WHERE round_id = $1 AND status = 'accepted'
		ORDER BY wallet_id, id
		FOR UPDATE OF bets`, roundID)
	if err != nil {
		return SettlementResult{}, err
	}
	bets := make([]acceptedBet, 0)
	for rows.Next() {
		var bet acceptedBet
		if err := rows.Scan(&bet.betID, &bet.walletID, &bet.userID, &bet.currency, &bet.playMode,
			&bet.selection, &bet.stake, &bet.payoutMultiplier, &bet.payoutDivisor, &bet.simulated, &bet.placedAt, &bet.rebateVersion); err != nil {
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

	// Gather every source and recipient before taking any wallet lock. Task and
	// spin transactions lock all of a user's currencies in the same UUID order.
	users := map[string]bool{}
	type recipient struct{ user, currency string }
	recipients := map[recipient]bool{}
	for i := range bets {
		b := &bets[i]
		users[b.userID] = true
		if b.simulated {
			continue
		}
		if b.rebateVersion == 1 {
			var parent string
			e := tx.QueryRow(ctx, `SELECT parent_user_id::text FROM agent_relations WHERE user_id=$1 AND parent_user_id IS NOT NULL`, b.userID).Scan(&parent)
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				return SettlementResult{}, e
			}
			if e == nil {
				b.legacyAgentID = parent
				users[parent] = true
				recipients[recipient{parent, b.currency}] = true
			}
			continue
		}
		chain, e := rebate.LoadTx(ctx, tx, b.betID)
		if e != nil {
			return SettlementResult{}, e
		}
		var payout int64
		if hashSelectionWins(b.playMode, b.selection, outcome) {
			m, d := rules.PayoutMultiplier, rules.PayoutScale()
			if b.payoutMultiplier != nil && b.payoutDivisor != nil {
				m, d = *b.payoutMultiplier, *b.payoutDivisor
			}
			if m <= 0 || d <= 0 || b.stake > math.MaxInt64/m {
				return SettlementResult{}, ErrPayoutOverflow
			}
			payout = b.stake * m / d
		}
		b.allocations, e = rebate.Calculate(b.stake, payout, b.playMode, chain)
		if e != nil {
			return SettlementResult{}, e
		}
		for _, a := range b.allocations {
			users[a.UserID] = true
			recipients[recipient{a.UserID, b.currency}] = true
		}
	}
	ordered := make([]recipient, 0, len(recipients))
	for r := range recipients {
		ordered = append(ordered, r)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].user == ordered[j].user {
			return ordered[i].currency < ordered[j].currency
		}
		return ordered[i].user < ordered[j].user
	})
	for _, r := range ordered {
		if _, err = tx.Exec(ctx, `INSERT INTO wallets(id,user_id,currency) SELECT gen_random_uuid(),$1,$2 WHERE NOT EXISTS(SELECT 1 FROM wallets WHERE user_id=$1 AND currency=$2) ON CONFLICT(user_id,currency) DO NOTHING`, r.user, r.currency); err != nil {
			return SettlementResult{}, err
		}
	}
	ids := make([]string, 0, len(users))
	for id := range users {
		ids = append(ids, id)
	}
	locked, e := tx.Query(ctx, `SELECT id FROM wallets WHERE user_id=ANY($1::uuid[]) ORDER BY id FOR UPDATE`, ids)
	if e != nil {
		return SettlementResult{}, e
	}
	for locked.Next() {
	}
	e = locked.Err()
	locked.Close()
	if e != nil {
		return SettlementResult{}, e
	}

	result := SettlementResult{RoundID: roundID, Outcome: append([]string(nil), outcome...), SettledAt: time.Now().UTC()}
	for _, bet := range bets {
		if !bet.simulated && bet.rebateVersion == 1 {
			if err := applyCommission(ctx, tx, bet.betID, bet.legacyAgentID, bet.currency, bet.stake, result.SettledAt); err != nil {
				return SettlementResult{}, err
			}
		}
		if !bet.simulated && bet.rebateVersion == 2 {
			for _, a := range bet.allocations {
				if err := rebate.PayTx(ctx, tx, bet.betID, bet.currency, a); err != nil {
					return SettlementResult{}, err
				}
			}
		}
		var availableMinor int64
		if err := tx.QueryRow(ctx, `SELECT available_minor FROM wallets WHERE id = $1 FOR UPDATE`, bet.walletID).Scan(&availableMinor); err != nil {
			return SettlementResult{}, err
		}
		won := rules.SelectionWins(bet.selection, outcome)
		if bet.playMode != "" {
			won = hashSelectionWins(bet.playMode, bet.selection, outcome)
		}
		// 只有实际结算为输或赢的投注才属于活动任务有效流水。取消和退款投注
		// 不会进入 SettleRound，因此从源头上不会累计，也不存在领取后再回退的问题。
		if service.taskHook != nil && !bet.simulated && bet.playMode != "dodge" {
			if err := service.taskHook.OnBetSettled(ctx, tx, bet.userID, bet.currency, bet.stake, bet.placedAt); err != nil {
				return SettlementResult{}, err
			}
		}
		if !won {
			if _, err := tx.Exec(ctx, `UPDATE bets SET status = 'lost', settled_at = $2, balance_after_settlement_minor = $3 WHERE id = $1`, bet.betID, result.SettledAt, availableMinor); err != nil {
				return SettlementResult{}, err
			}
			result.LostBetCount++
			continue
		}
		payoutMultiplier, payoutDivisor := rules.PayoutMultiplier, rules.PayoutScale()
		if bet.payoutMultiplier != nil && bet.payoutDivisor != nil {
			payoutMultiplier, payoutDivisor = *bet.payoutMultiplier, *bet.payoutDivisor
		}
		if payoutMultiplier <= 0 || payoutDivisor <= 0 || bet.stake > math.MaxInt64/payoutMultiplier {
			return SettlementResult{}, ErrPayoutOverflow
		}
		payout := bet.stake * payoutMultiplier / payoutDivisor
		if bet.simulated {
			if _, err := tx.Exec(ctx, `UPDATE bets SET status='won',payout_minor=$2,settled_at=$3,balance_after_settlement_minor=$4 WHERE id=$1`, bet.betID, payout, result.SettledAt, availableMinor); err != nil {
				return SettlementResult{}, err
			}
			result.WonBetCount++
			result.PayoutMinor += payout
			continue
		}
		if availableMinor > math.MaxInt64-payout {
			return SettlementResult{}, ErrPayoutOverflow
		}
		availableMinor += payout
		if _, err := tx.Exec(ctx, `UPDATE wallets SET available_minor = $2, version = version + 1, updated_at = $3 WHERE id = $1`, bet.walletID, availableMinor, result.SettledAt); err != nil {
			return SettlementResult{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE bets SET status = 'won', payout_minor = $2, settled_at = $3, balance_after_settlement_minor = $4 WHERE id = $1`, bet.betID, payout, result.SettledAt, availableMinor); err != nil {
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

func hashSelectionWins(mode string, raw json.RawMessage, outcome []string) bool {
	var selection struct {
		Pick string `json:"pick"`
	}
	if json.Unmarshal(raw, &selection) != nil || selection.Pick == "" {
		return false
	}
	winningDigit := ""
	for _, value := range outcome {
		if len(value) == 1 && value[0] >= '0' && value[0] <= '9' {
			winningDigit = value
			break
		}
	}
	switch mode {
	case "guess":
		return winningDigit != "" && selection.Pick == winningDigit
	case "dodge":
		return winningDigit != "" && selection.Pick != winningDigit
	case "road":
		return containsOutcome(outcome, selection.Pick)
	default:
		return false
	}
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

// withinPool 校验开奖结果的每个值都属于规则定义的结果池。
func withinPool(outcome []string, pool []string) bool {
	allowed := make(map[string]struct{}, len(pool))
	for _, value := range pool {
		allowed[value] = struct{}{}
	}
	for _, value := range outcome {
		if _, ok := allowed[value]; !ok {
			return false
		}
	}
	return true
}
