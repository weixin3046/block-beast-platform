package chain

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ApplyWithdrawalStatus applies PQPA's terminal withdrawal callback. Failed
// withdrawals release their frozen funds; confirmed withdrawals consume them.
func (service *Service) ApplyWithdrawalStatus(ctx context.Context, input WithdrawalStatusInput) error {
	if input.ProviderOrderID == "" || (input.Status != "confirmed" && input.Status != "failed") {
		return ErrMissingFields
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var withdrawalID, walletID, currentStatus string
	var amount, available, frozen int64
	var decimals int
	err = tx.QueryRow(ctx, `
		SELECT w.id, w.wallet_id, w.status, w.amount_minor, wallets.available_minor,
			wallets.frozen_minor, COALESCE(w.token_decimals, 0)
		FROM withdrawals w
		JOIN wallets ON wallets.id=w.wallet_id
		WHERE w.id::text=$1 OR w.provider_order_id=$1
		FOR UPDATE`, input.ProviderOrderID).
		Scan(&withdrawalID, &walletID, &currentStatus, &amount, &available, &frozen, &decimals)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrWithdrawalNotFound
	}
	if err != nil {
		return err
	}
	if currentStatus == input.Status {
		return tx.Commit(ctx)
	}
	if currentStatus != "broadcasted" {
		return ErrWithdrawalState
	}
	if frozen < amount {
		return ErrWithdrawalState
	}
	if input.Status == "confirmed" {
		var feeMinor any
		if input.Fee != "" {
			parsedFee, err := parseDecimalMinor(input.Fee, decimals)
			if err != nil {
				return err
			}
			feeMinor = parsedFee
		}
		if _, err := tx.Exec(ctx, `UPDATE wallets SET frozen_minor=$2, version=version+1, updated_at=now() WHERE id=$1`, walletID, frozen-amount); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE withdrawals SET status='confirmed', tx_hash=NULLIF($2, ''), failure_reason=NULL, provider_fee_minor=$3 WHERE id=$1`, withdrawalID, input.TxHash, feeMinor); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ledger_entries (id, wallet_id, business_type, business_id, entry_type, amount_minor, balance_after_minor) VALUES ($1,$2,'withdrawal',$3,'withdrawal_debit',$4,$5)`, uuid.NewString(), walletID, withdrawalID, -amount, available); err != nil {
			return err
		}
	} else {
		available += amount
		if _, err := tx.Exec(ctx, `UPDATE wallets SET available_minor=$2, frozen_minor=$3, version=version+1, updated_at=now() WHERE id=$1`, walletID, available, frozen-amount); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE withdrawals SET status='failed', tx_hash=NULLIF($2, ''), failure_reason=$3 WHERE id=$1`, withdrawalID, input.TxHash, input.FailureReason); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ledger_entries (id, wallet_id, business_type, business_id, entry_type, amount_minor, balance_after_minor) VALUES ($1,$2,'withdrawal',$3,'withdrawal_unfreeze',$4,$5)`, uuid.NewString(), walletID, withdrawalID, amount, available); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
