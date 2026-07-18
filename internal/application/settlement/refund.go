package settlement

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/block-beast/platform/internal/domain/events"
	"github.com/block-beast/platform/internal/domain/game"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

func (service *Service) CancelRound(ctx context.Context, roundID string) (int, error) {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var status game.RoundStatus
	err = tx.QueryRow(ctx, `SELECT status FROM rounds WHERE id = $1 FOR UPDATE`, roundID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, game.ErrRoundNotFound
	}
	if err != nil {
		return 0, err
	}
	if status == game.RoundCancelled {
		return 0, tx.Commit(ctx)
	}
	if status != game.RoundOpen && status != game.RoundClosed {
		return 0, game.ErrInvalidTransition
	}

	type refundableBet struct {
		betID    string
		walletID string
		stake    int64
		currency string
	}
	rows, err := tx.Query(ctx, `
		SELECT bets.id, bets.wallet_id, bets.stake_minor, wallets.currency
		FROM bets
		JOIN wallets ON wallets.id = bets.wallet_id
		WHERE bets.round_id = $1 AND bets.status = 'accepted'
		ORDER BY bets.wallet_id, bets.id
		FOR UPDATE OF bets`, roundID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	bets := make([]refundableBet, 0)
	for rows.Next() {
		var bet refundableBet
		if err := rows.Scan(&bet.betID, &bet.walletID, &bet.stake, &bet.currency); err != nil {
			return 0, err
		}
		bets = append(bets, bet)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, bet := range bets {
		var availableMinor int64
		err := tx.QueryRow(ctx, `SELECT available_minor FROM wallets WHERE id = $1 FOR UPDATE`, bet.walletID).Scan(&availableMinor)
		if err != nil {
			return 0, err
		}
		availableMinor += bet.stake
		_, err = tx.Exec(ctx, `UPDATE wallets SET available_minor = $2, version = version + 1, updated_at = now() WHERE id = $1`, bet.walletID, availableMinor)
		if err != nil {
			return 0, err
		}
		_, err = tx.Exec(ctx, `UPDATE bets SET status = 'refunded', settled_at = now() WHERE id = $1`, bet.betID)
		if err != nil {
			return 0, err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO ledger_entries (id, wallet_id, business_type, business_id, entry_type, amount_minor, balance_after_minor)
			VALUES ($1, $2, 'refund', $3, 'refund', $4, $5)`, uuid.NewString(), bet.walletID, bet.betID, bet.stake, availableMinor)
		if err != nil {
			return 0, err
		}
	}

	_, err = tx.Exec(ctx, `UPDATE rounds SET status = 'cancelled', version = version + 1 WHERE id = $1`, roundID)
	if err != nil {
		return 0, err
	}
	payload, err := json.Marshal(struct {
		RoundID          string `json:"round_id"`
		RefundedBetCount int    `json:"refunded_bet_count"`
	}{RoundID: roundID, RefundedBetCount: len(bets)})
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload)
		VALUES ($1, 'round', $2, $3, $4)`, uuid.NewString(), roundID, events.RoundCancelled, payload)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(bets), nil
}
