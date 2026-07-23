package chain

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/block-beast/platform/internal/domain/events"
	"github.com/block-beast/platform/internal/domain/wallet"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrUnknownDepositAddress = errors.New("deposit address is not registered")
var ErrInvalidAmount = errors.New("amount must be positive")
var ErrMissingFields = errors.New("missing required fields")
var ErrWithdrawalNotFound = errors.New("withdrawal not found")
var ErrDepositAddressNotFound = errors.New("deposit address not found")
var ErrWithdrawalState = errors.New("withdrawal cannot transition from its current status")

type Service struct {
	pool               *pgxpool.Pool
	addressProvider    DepositAddressProvider
	withdrawalProvider WithdrawalProvider
}

type DepositAddressProvider interface {
	CreateDepositAddress(ctx context.Context, userID, chainCode, tokenCode string) (providerID, address string, err error)
}

type WithdrawalProvider interface {
	CreateProviderWithdrawal(ctx context.Context, requestID, chainCode, tokenCode, address string, amountMinor int64) (providerOrderID, txHash, status string, err error)
}

type DepositAddress struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	ChainCode string `json:"chain_code"`
	TokenCode string `json:"token_code"`
	Address   string `json:"address"`
}

func (service *Service) GetDepositAddress(ctx context.Context, userID, chainCode, tokenCode string) (DepositAddress, error) {
	var output DepositAddress
	err := service.pool.QueryRow(ctx, `SELECT id, user_id, chain_code, token_code, address FROM chain_addresses WHERE user_id=$1 AND chain_code=$2 AND token_code=$3`, userID, chainCode, tokenCode).Scan(&output.ID, &output.UserID, &output.ChainCode, &output.TokenCode, &output.Address)
	if errors.Is(err, pgx.ErrNoRows) {
		return DepositAddress{}, ErrDepositAddressNotFound
	}
	return output, err
}

func (service *Service) CreateDepositAddress(ctx context.Context, userID, chainCode, tokenCode string) (DepositAddress, error) {
	if userID == "" || chainCode == "" || tokenCode == "" {
		return DepositAddress{}, ErrMissingFields
	}
	if existing, err := service.GetDepositAddress(ctx, userID, chainCode, tokenCode); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrDepositAddressNotFound) {
		return DepositAddress{}, err
	}
	if service.addressProvider == nil {
		return DepositAddress{}, errors.New("deposit address provider is unavailable")
	}
	providerID, address, err := service.addressProvider.CreateDepositAddress(ctx, userID, chainCode, tokenCode)
	if err != nil {
		return DepositAddress{}, err
	}
	output := DepositAddress{ID: uuid.NewString(), UserID: userID, ChainCode: chainCode, TokenCode: tokenCode, Address: address}
	if err := service.pool.QueryRow(ctx, `
		INSERT INTO chain_addresses (id, user_id, chain_code, token_code, address, provider_address_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (chain_code, token_code, address) DO UPDATE SET user_id = EXCLUDED.user_id
		RETURNING id, user_id, chain_code, token_code, address`, output.ID, userID, chainCode, tokenCode, address, providerID).
		Scan(&output.ID, &output.UserID, &output.ChainCode, &output.TokenCode, &output.Address); err != nil {
		return DepositAddress{}, err
	}
	return output, nil
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

func (service *Service) WithDepositAddressProvider(provider DepositAddressProvider) *Service {
	service.addressProvider = provider
	return service
}

func (service *Service) WithWithdrawalProvider(provider WithdrawalProvider) *Service {
	service.withdrawalProvider = provider
	return service
}

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

// WithdrawalInput 是一次提现申请；UserID 由服务端从访问令牌注入，不信任请求体。
type WithdrawalInput struct {
	UserID             string `json:"-"`
	ClientRequestID    string `json:"client_request_id"`
	DestinationAddress string `json:"destination_address"`
	Currency           string `json:"currency"`
	AmountMinor        int64  `json:"amount_minor"`
}

type Withdrawal struct {
	WithdrawalID       string    `json:"withdrawal_id"`
	UserID             string    `json:"-"`
	ClientRequestID    string    `json:"client_request_id"`
	DestinationAddress string    `json:"destination_address"`
	Currency           string    `json:"currency"`
	AmountMinor        int64     `json:"amount_minor"`
	Status             string    `json:"status"`
	CreatedAt          time.Time `json:"created_at"`
}

type WithdrawalStatusInput struct {
	ProviderOrderID string `json:"provider_order_id"`
	TxHash          string `json:"tx_hash"`
	Status          string `json:"status"`
	FailureReason   string `json:"failure_reason"`
}

// RequestWithdrawal 幂等创建提现申请：同一事务中将申请金额从可用余额
// 移入冻结余额、创建提现记录、写账本和 outbox 事件。重复请求返回既有申请。
func (service *Service) RequestWithdrawal(ctx context.Context, input WithdrawalInput) (Withdrawal, error) {
	if input.UserID == "" || input.ClientRequestID == "" || input.DestinationAddress == "" || input.Currency == "" {
		return Withdrawal{}, ErrMissingFields
	}
	if input.AmountMinor <= 0 {
		return Withdrawal{}, ErrInvalidAmount
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
		Currency:           input.Currency,
		AmountMinor:        input.AmountMinor,
		Status:             "requested",
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO withdrawals (id, user_id, wallet_id, client_request_id, destination_address, amount_minor, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'requested')
		RETURNING created_at`,
		withdrawal.WithdrawalID, input.UserID, walletID, input.ClientRequestID, input.DestinationAddress, input.AmountMinor).
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

// FindWithdrawal 按 ID 查询提现申请。
func (service *Service) FindWithdrawal(ctx context.Context, withdrawalID string) (Withdrawal, error) {
	var withdrawal Withdrawal
	err := service.pool.QueryRow(ctx, `
		SELECT withdrawals.id, withdrawals.user_id, withdrawals.client_request_id, withdrawals.destination_address,
			wallets.currency, withdrawals.amount_minor, withdrawals.status, withdrawals.created_at
		FROM withdrawals
		JOIN wallets ON wallets.id = withdrawals.wallet_id
		WHERE withdrawals.id = $1`, withdrawalID).
		Scan(&withdrawal.WithdrawalID, &withdrawal.UserID, &withdrawal.ClientRequestID, &withdrawal.DestinationAddress, &withdrawal.Currency, &withdrawal.AmountMinor, &withdrawal.Status, &withdrawal.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Withdrawal{}, ErrWithdrawalNotFound
	}
	if err != nil {
		return Withdrawal{}, err
	}
	return withdrawal, nil
}

func (service *Service) ListWithdrawals(ctx context.Context, status string, limit int) ([]Withdrawal, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := `SELECT withdrawals.id,withdrawals.user_id,withdrawals.client_request_id,withdrawals.destination_address,wallets.currency,withdrawals.amount_minor,withdrawals.status,withdrawals.created_at FROM withdrawals JOIN wallets ON wallets.id=withdrawals.wallet_id`
	args := []any{limit}
	if status != "" {
		query += ` WHERE withdrawals.status=$2`
	}
	query += ` ORDER BY withdrawals.created_at DESC LIMIT $1`
	if status != "" {
		args = append(args, status)
	}
	rows, err := service.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	output := make([]Withdrawal, 0)
	for rows.Next() {
		var item Withdrawal
		if err := rows.Scan(&item.WithdrawalID, &item.UserID, &item.ClientRequestID, &item.DestinationAddress, &item.Currency, &item.AmountMinor, &item.Status, &item.CreatedAt); err != nil {
			return nil, err
		}
		output = append(output, item)
	}
	return output, rows.Err()
}

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

// SendApprovedWithdrawal performs the external call after approval has been
// committed. The platform withdrawal ID is reused as PQPA's idempotency key.
func (service *Service) SendApprovedWithdrawal(ctx context.Context, withdrawalID, chainCode string) error {
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
	providerOrderID, txHash, _, err := service.withdrawalProvider.CreateProviderWithdrawal(ctx, withdrawal.WithdrawalID, chainCode, withdrawal.Currency, withdrawal.DestinationAddress, withdrawal.AmountMinor)
	if err != nil {
		return err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `UPDATE withdrawals SET status='broadcasted', provider_order_id=$2, tx_hash=NULLIF($3, '') WHERE id=$1 AND status='approved'`, withdrawalID, providerOrderID, txHash)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	payload, err := json.Marshal(struct {
		WithdrawalID    string `json:"withdrawal_id"`
		ProviderOrderID string `json:"provider_order_id"`
		TxHash          string `json:"tx_hash"`
	}{withdrawalID, providerOrderID, txHash})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload) VALUES ($1, 'withdrawal', $2, $3, $4)`, uuid.NewString(), withdrawalID, events.WithdrawalSent, payload); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

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
	err = tx.QueryRow(ctx, `SELECT w.id, w.wallet_id, w.status, w.amount_minor, wallets.available_minor, wallets.frozen_minor FROM withdrawals w JOIN wallets ON wallets.id=w.wallet_id WHERE w.provider_order_id=$1 FOR UPDATE`, input.ProviderOrderID).Scan(&withdrawalID, &walletID, &currentStatus, &amount, &available, &frozen)
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
		if _, err := tx.Exec(ctx, `UPDATE wallets SET frozen_minor=$2, version=version+1, updated_at=now() WHERE id=$1`, walletID, frozen-amount); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE withdrawals SET status='confirmed', tx_hash=NULLIF($2, ''), failure_reason=NULL WHERE id=$1`, withdrawalID, input.TxHash); err != nil {
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

func findWithdrawalByRequestID(ctx context.Context, tx pgx.Tx, userID string, clientRequestID string) (Withdrawal, error) {
	var withdrawal Withdrawal
	err := tx.QueryRow(ctx, `
		SELECT withdrawals.id, withdrawals.user_id, withdrawals.client_request_id, withdrawals.destination_address,
			wallets.currency, withdrawals.amount_minor, withdrawals.status, withdrawals.created_at
		FROM withdrawals
		JOIN wallets ON wallets.id = withdrawals.wallet_id
		WHERE withdrawals.user_id = $1 AND withdrawals.client_request_id = $2`, userID, clientRequestID).
		Scan(&withdrawal.WithdrawalID, &withdrawal.UserID, &withdrawal.ClientRequestID, &withdrawal.DestinationAddress, &withdrawal.Currency, &withdrawal.AmountMinor, &withdrawal.Status, &withdrawal.CreatedAt)
	return withdrawal, err
}

func findWithdrawalForUpdate(ctx context.Context, tx pgx.Tx, withdrawalID string) (Withdrawal, error) {
	var withdrawal Withdrawal
	err := tx.QueryRow(ctx, `SELECT withdrawals.id, withdrawals.user_id, withdrawals.client_request_id, withdrawals.destination_address, wallets.currency, withdrawals.amount_minor, withdrawals.status, withdrawals.created_at FROM withdrawals JOIN wallets ON wallets.id=withdrawals.wallet_id WHERE withdrawals.id=$1 FOR UPDATE`, withdrawalID).Scan(&withdrawal.WithdrawalID, &withdrawal.UserID, &withdrawal.ClientRequestID, &withdrawal.DestinationAddress, &withdrawal.Currency, &withdrawal.AmountMinor, &withdrawal.Status, &withdrawal.CreatedAt)
	return withdrawal, err
}
