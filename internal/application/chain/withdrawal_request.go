package chain

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/block-beast/platform/internal/domain/events"
	"github.com/block-beast/platform/internal/domain/wallet"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RequestWithdrawal 幂等创建提现申请：同一事务中将申请金额从可用余额
// 移入冻结余额、创建提现记录、写账本和 outbox 事件。重复请求返回既有申请。
func (service *Service) RequestWithdrawal(ctx context.Context, input WithdrawalInput) (Withdrawal, error) {
	input.ChainCode = strings.ToUpper(strings.TrimSpace(input.ChainCode))
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	input.DestinationAddress = strings.TrimSpace(input.DestinationAddress)
	input.DestinationMemo = strings.TrimSpace(input.DestinationMemo)
	if input.UserID == "" || input.ClientRequestID == "" || input.DestinationAddress == "" || input.ChainCode == "" || input.Currency == "" {
		return Withdrawal{}, ErrMissingFields
	}
	if input.AmountMinor <= 0 {
		return Withdrawal{}, ErrInvalidAmount
	}
	if err := validateWithdrawalDestination(input.ChainCode, input.DestinationAddress, input.DestinationMemo); err != nil {
		return Withdrawal{}, err
	}

	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Withdrawal{}, err
	}
	defer tx.Rollback(ctx)

	existing, err := findWithdrawalByRequestID(ctx, tx, input.UserID, input.ClientRequestID)
	if err == nil {
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Withdrawal{}, err
	}

	var chainTokenID int64
	var tokenDecimals int
	err = tx.QueryRow(ctx, `
		SELECT provider_chain_token_id, decimals
		FROM provider_supported_assets
		WHERE provider='pqpa' AND chain_code=$1 AND token_code=$2
			AND enabled=true AND support_withdraw=true AND provider_chain_token_id IS NOT NULL`,
		input.ChainCode, input.Currency).Scan(&chainTokenID, &tokenDecimals)
	if errors.Is(err, pgx.ErrNoRows) {
		return Withdrawal{}, ErrUnsupportedAsset
	}
	if err != nil {
		return Withdrawal{}, err
	}
	if err := service.withdrawalPolicy.validateAmount(input.AmountMinor); err != nil {
		return Withdrawal{}, err
	}

	var walletID string
	var availableMinor int64
	var frozenMinor int64
	err = tx.QueryRow(ctx, `
		SELECT id, available_minor, frozen_minor FROM wallets
		WHERE user_id = $1 AND currency = $2
		FOR UPDATE`, input.UserID, input.Currency).Scan(&walletID, &availableMinor, &frozenMinor)
	if errors.Is(err, pgx.ErrNoRows) {
		return Withdrawal{}, wallet.ErrWalletNotFound
	}
	if err != nil {
		return Withdrawal{}, err
	}
	if availableMinor < input.AmountMinor {
		return Withdrawal{}, wallet.ErrInsufficientFunds
	}
	if service.withdrawalPolicy.DailyLimitMinor > 0 {
		startOfDay := time.Now().UTC().Truncate(24 * time.Hour)
		var withdrawnToday int64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(sum(amount_minor), 0)
			FROM withdrawals
			WHERE user_id=$1 AND token_code=$2 AND created_at >= $3
				AND status IN ('requested', 'approved', 'broadcasted', 'confirmed')`,
			input.UserID, input.Currency, startOfDay).Scan(&withdrawnToday); err != nil {
			return Withdrawal{}, err
		}
		if withdrawnToday > service.withdrawalPolicy.DailyLimitMinor-input.AmountMinor {
			return Withdrawal{}, ErrWithdrawalDailyLimit
		}
	}
	availableMinor -= input.AmountMinor
	frozenMinor += input.AmountMinor
	if _, err := tx.Exec(ctx, `UPDATE wallets SET available_minor = $2, frozen_minor = $3, version = version + 1, updated_at = now() WHERE id = $1`, walletID, availableMinor, frozenMinor); err != nil {
		return Withdrawal{}, err
	}

	withdrawal := Withdrawal{
		WithdrawalID:       uuid.NewString(),
		UserID:             input.UserID,
		ClientRequestID:    input.ClientRequestID,
		DestinationAddress: input.DestinationAddress,
		DestinationMemo:    input.DestinationMemo,
		ChainCode:          input.ChainCode,
		Currency:           input.Currency,
		AmountMinor:        input.AmountMinor,
		Status:             "requested",
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO withdrawals (
			id, user_id, wallet_id, client_request_id, destination_address,
			destination_memo, chain_code, token_code, provider_chain_token_id,
			token_decimals, amount_minor, status
		)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, $9, $10, $11, 'requested')
		RETURNING created_at`,
		withdrawal.WithdrawalID, input.UserID, walletID, input.ClientRequestID, input.DestinationAddress,
		input.DestinationMemo, input.ChainCode, input.Currency, chainTokenID, tokenDecimals, input.AmountMinor).
		Scan(&withdrawal.CreatedAt)
	if err != nil {
		return Withdrawal{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger_entries (id, wallet_id, business_type, business_id, entry_type, amount_minor, balance_after_minor)
		VALUES ($1, $2, 'withdrawal', $3, 'withdrawal_freeze', $4, $5)`, uuid.NewString(), walletID, withdrawal.WithdrawalID, -input.AmountMinor, availableMinor); err != nil {
		return Withdrawal{}, err
	}
	payload, err := json.Marshal(struct {
		WithdrawalID string `json:"withdrawal_id"`
		UserID       string `json:"user_id"`
		Currency     string `json:"currency"`
	}{WithdrawalID: withdrawal.WithdrawalID, UserID: input.UserID, Currency: input.Currency})
	if err != nil {
		return Withdrawal{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload)
		VALUES ($1, 'withdrawal', $2, $3, $4)`, uuid.NewString(), withdrawal.WithdrawalID, events.WithdrawalRequested, payload); err != nil {
		return Withdrawal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Withdrawal{}, err
	}
	return withdrawal, nil
}
