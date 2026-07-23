package chain

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/block-beast/platform/internal/domain/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ApproveWithdrawal records the back-office decision. The actual provider call
// is dispatched from the transactional outbox after this transaction commits.
func (service *Service) ApproveWithdrawal(ctx context.Context, withdrawalID, reviewerID string) (Withdrawal, error) {
	if withdrawalID == "" || reviewerID == "" {
		return Withdrawal{}, ErrMissingFields
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Withdrawal{}, err
	}
	defer tx.Rollback(ctx)
	withdrawal, err := findWithdrawalForUpdate(ctx, tx, withdrawalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Withdrawal{}, ErrWithdrawalNotFound
	}
	if err != nil {
		return Withdrawal{}, err
	}
	if withdrawal.Status != "requested" {
		return Withdrawal{}, ErrWithdrawalState
	}
	if _, err := tx.Exec(ctx, `UPDATE withdrawals SET status='approved', reviewed_by=$2, reviewed_at=now() WHERE id=$1`, withdrawalID, reviewerID); err != nil {
		return Withdrawal{}, err
	}
	withdrawal.Status = "approved"
	payload, err := json.Marshal(struct {
		WithdrawalID string `json:"withdrawal_id"`
		Currency     string `json:"currency"`
	}{withdrawalID, withdrawal.Currency})
	if err != nil {
		return Withdrawal{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload) VALUES ($1, 'withdrawal', $2, $3, $4)`, uuid.NewString(), withdrawalID, events.WithdrawalApproved, payload); err != nil {
		return Withdrawal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Withdrawal{}, err
	}
	return withdrawal, nil
}

func (service *Service) RejectWithdrawal(ctx context.Context, withdrawalID, reviewerID, reason string) (Withdrawal, error) {
	if withdrawalID == "" || reviewerID == "" {
		return Withdrawal{}, ErrMissingFields
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Withdrawal{}, err
	}
	defer tx.Rollback(ctx)
	withdrawal, err := findWithdrawalForUpdate(ctx, tx, withdrawalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Withdrawal{}, ErrWithdrawalNotFound
	}
	if err != nil {
		return Withdrawal{}, err
	}
	if withdrawal.Status != "requested" {
		return Withdrawal{}, ErrWithdrawalState
	}
	var walletID string
	var available, frozen int64
	if err := tx.QueryRow(ctx, `SELECT id,available_minor,frozen_minor FROM wallets WHERE user_id=$1 AND currency=$2 FOR UPDATE`, withdrawal.UserID, withdrawal.Currency).Scan(&walletID, &available, &frozen); err != nil {
		return Withdrawal{}, err
	}
	if frozen < withdrawal.AmountMinor {
		return Withdrawal{}, ErrWithdrawalState
	}
	available += withdrawal.AmountMinor
	if _, err := tx.Exec(ctx, `UPDATE wallets SET available_minor=$2,frozen_minor=$3,version=version+1,updated_at=now() WHERE id=$1`, walletID, available, frozen-withdrawal.AmountMinor); err != nil {
		return Withdrawal{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE withdrawals SET status='cancelled',reviewed_by=$2,reviewed_at=now(),failure_reason=$3 WHERE id=$1`, withdrawalID, reviewerID, reason); err != nil {
		return Withdrawal{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ledger_entries(id,wallet_id,business_type,business_id,entry_type,amount_minor,balance_after_minor) VALUES($1,$2,'withdrawal',$3,'withdrawal_unfreeze',$4,$5)`, uuid.NewString(), walletID, withdrawalID, withdrawal.AmountMinor, available); err != nil {
		return Withdrawal{}, err
	}
	withdrawal.Status = "cancelled"
	if err := tx.Commit(ctx); err != nil {
		return Withdrawal{}, err
	}
	return withdrawal, nil
}
