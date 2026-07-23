package chain

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/block-beast/platform/internal/domain/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DepositInput 是链上服务商推送的一笔已确认充值。
type DepositInput struct {
	ProviderEventID string `json:"provider_event_id"`
	TxHash          string `json:"tx_hash"`
	ChainCode       string `json:"chain_code"`
	TokenCode       string `json:"token_code"`
	Address         string `json:"address"`
	AmountMinor     int64  `json:"amount_minor"`
}

type DepositResult struct {
	DepositID string `json:"deposit_id"`
	Status    string `json:"status"`
	// Credited 为 false 表示本次是重复回调，未重复入账。
	Credited bool `json:"credited"`
}

type Deposit struct {
	DepositID   string     `json:"deposit_id"`
	TxHash      string     `json:"tx_hash"`
	ChainCode   string     `json:"chain_code"`
	TokenCode   string     `json:"token_code"`
	Address     string     `json:"address"`
	AmountMinor int64      `json:"amount_minor"`
	Status      string     `json:"status"`
	ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
}

func (service *Service) ListDeposits(ctx context.Context, userID string, limit int) ([]Deposit, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := service.pool.Query(ctx, `
		SELECT deposits.id::text,deposits.tx_hash,chain_addresses.chain_code,chain_addresses.token_code,
			chain_addresses.address,deposits.amount_minor,deposits.status,deposits.confirmed_at
		FROM deposits
		JOIN chain_addresses ON chain_addresses.id=deposits.chain_address_id
		WHERE chain_addresses.user_id=$1
		ORDER BY deposits.confirmed_at DESC NULLS LAST,deposits.id DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Deposit, 0)
	for rows.Next() {
		var item Deposit
		if err := rows.Scan(&item.DepositID, &item.TxHash, &item.ChainCode, &item.TokenCode, &item.Address, &item.AmountMinor, &item.Status, &item.ConfirmedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// CreditDeposit 幂等处理充值回调：按服务商事件 ID 与交易哈希去重，
// 首次回调在同一事务中创建充值记录、锁钱包入账、写账本和 outbox 事件。
// 用户尚无该币种钱包时随充值创建。
func (service *Service) CreditDeposit(ctx context.Context, input DepositInput) (DepositResult, error) {
	if input.ProviderEventID == "" || input.TxHash == "" || input.ChainCode == "" || input.TokenCode == "" || input.Address == "" {
		return DepositResult{}, ErrMissingFields
	}
	if input.AmountMinor <= 0 {
		return DepositResult{}, ErrInvalidAmount
	}

	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return DepositResult{}, err
	}
	defer tx.Rollback(ctx)

	var chainAddressID string
	var userID string
	err = tx.QueryRow(ctx, `
		SELECT id, user_id FROM chain_addresses
		WHERE chain_code = $1 AND token_code = $2 AND address = $3`, input.ChainCode, input.TokenCode, input.Address).
		Scan(&chainAddressID, &userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return DepositResult{}, ErrUnknownDepositAddress
	}
	if err != nil {
		return DepositResult{}, err
	}

	depositID := uuid.NewString()
	creditedAt := time.Now().UTC()
	err = tx.QueryRow(ctx, `
		INSERT INTO deposits (id, chain_address_id, provider_event_id, tx_hash, amount_minor, status, confirmed_at)
		VALUES ($1, $2, $3, $4, $5, 'credited', $6)
		ON CONFLICT DO NOTHING
		RETURNING id`, depositID, chainAddressID, input.ProviderEventID, input.TxHash, input.AmountMinor, creditedAt).Scan(&depositID)
	if errors.Is(err, pgx.ErrNoRows) {
		// 服务商事件 ID 或交易哈希已存在：重复回调，直接返回既有记录，不重复入账。
		var existingID string
		var status string
		if err := tx.QueryRow(ctx, `
			SELECT id, status FROM deposits WHERE provider_event_id = $1 OR tx_hash = $2`, input.ProviderEventID, input.TxHash).
			Scan(&existingID, &status); err != nil {
			return DepositResult{}, err
		}
		return DepositResult{DepositID: existingID, Status: status, Credited: false}, tx.Commit(ctx)
	}
	if err != nil {
		return DepositResult{}, err
	}

	walletID, err := ensureWallet(ctx, tx, userID, input.TokenCode)
	if err != nil {
		return DepositResult{}, err
	}
	var availableMinor int64
	if err := tx.QueryRow(ctx, `SELECT available_minor FROM wallets WHERE id = $1 FOR UPDATE`, walletID).Scan(&availableMinor); err != nil {
		return DepositResult{}, err
	}
	availableMinor += input.AmountMinor
	if _, err := tx.Exec(ctx, `UPDATE wallets SET available_minor = $2, version = version + 1, updated_at = $3 WHERE id = $1`, walletID, availableMinor, creditedAt); err != nil {
		return DepositResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger_entries (id, wallet_id, business_type, business_id, entry_type, amount_minor, balance_after_minor)
		VALUES ($1, $2, 'deposit', $3, 'deposit_credit', $4, $5)`, uuid.NewString(), walletID, depositID, input.AmountMinor, availableMinor); err != nil {
		return DepositResult{}, err
	}
	payload, err := json.Marshal(struct {
		DepositID string `json:"deposit_id"`
		UserID    string `json:"user_id"`
		TokenCode string `json:"token_code"`
		TxHash    string `json:"tx_hash"`
	}{DepositID: depositID, UserID: userID, TokenCode: input.TokenCode, TxHash: input.TxHash})
	if err != nil {
		return DepositResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload, occurred_at)
		VALUES ($1, 'deposit', $2, $3, $4, $5)`, uuid.NewString(), depositID, events.DepositCredited, payload, creditedAt); err != nil {
		return DepositResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DepositResult{}, err
	}
	return DepositResult{DepositID: depositID, Status: "credited", Credited: true}, nil
}

// ensureWallet 返回用户指定币种的钱包 ID，不存在时创建零余额钱包。
func ensureWallet(ctx context.Context, tx pgx.Tx, userID string, currency string) (string, error) {
	walletID := uuid.NewString()
	err := tx.QueryRow(ctx, `
		INSERT INTO wallets (id, user_id, currency) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, currency) DO NOTHING
		RETURNING id`, walletID, userID, currency).Scan(&walletID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT id FROM wallets WHERE user_id = $1 AND currency = $2`, userID, currency).Scan(&walletID)
	}
	return walletID, err
}
