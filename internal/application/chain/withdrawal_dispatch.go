package chain

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/block-beast/platform/internal/domain/events"
	"github.com/google/uuid"
)

// SendApprovedWithdrawal performs the external call after approval has been
// committed. The platform withdrawal ID is reused as PQPA's idempotency key.
func (service *Service) SendApprovedWithdrawal(ctx context.Context, withdrawalID string) error {
	if service.withdrawalProvider == nil {
		return errors.New("withdrawal provider is unavailable")
	}
	withdrawal, err := service.FindWithdrawal(ctx, withdrawalID)
	if err != nil {
		return err
	}
	if withdrawal.Status == "broadcasted" || withdrawal.Status == "confirmed" {
		return nil
	}
	if withdrawal.Status != "approved" {
		return ErrWithdrawalState
	}
	var chainTokenID int64
	var decimals int
	if err := service.pool.QueryRow(ctx, `
		SELECT provider_chain_token_id, token_decimals
		FROM withdrawals WHERE id=$1`, withdrawalID).Scan(&chainTokenID, &decimals); err != nil {
		return err
	}
	providerOrderID, _, err := service.withdrawalProvider.CreateProviderWithdrawal(ctx, ProviderWithdrawalRequest{
		RequestID: withdrawal.WithdrawalID, ChainCode: withdrawal.ChainCode,
		ChainTokenID: chainTokenID, Address: withdrawal.DestinationAddress,
		Memo: withdrawal.DestinationMemo, AmountMinor: withdrawal.AmountMinor, Decimals: decimals,
	})
	if err != nil {
		return err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `UPDATE withdrawals SET status='broadcasted', provider_order_id=$2 WHERE id=$1 AND status='approved'`, withdrawalID, providerOrderID)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	payload, err := json.Marshal(struct {
		WithdrawalID    string `json:"withdrawal_id"`
		ProviderOrderID string `json:"provider_order_id"`
	}{withdrawalID, providerOrderID})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload) VALUES ($1, 'withdrawal', $2, $3, $4)`, uuid.NewString(), withdrawalID, events.WithdrawalSent, payload); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
