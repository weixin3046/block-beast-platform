package chain

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/block-beast/platform/internal/domain/events"
	"github.com/block-beast/platform/internal/domain/wallet"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, ctx
}

type fixture struct {
	userID    string
	walletID  string
	addressID string
	address   string
}

func seedChainUser(t *testing.T, pool *pgxpool.Pool, ctx context.Context, tokenCode string) fixture {
	t.Helper()
	f := fixture{
		userID:    uuid.NewString(),
		walletID:  uuid.NewString(),
		addressID: uuid.NewString(),
		address:   "T" + uuid.NewString()[:8],
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, display_name) VALUES ($1, 'chain test player')`, f.userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wallets (id, user_id, currency, available_minor) VALUES ($1, $2, 'USDT', 5000)`, f.walletID, f.userID); err != nil {
		t.Fatalf("create wallet: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO chain_addresses (id, user_id, chain_code, token_code, address) VALUES ($1, $2, 'TRON', $3, $4)`, f.addressID, f.userID, tokenCode, f.address); err != nil {
		t.Fatalf("create chain address: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM outbox_events WHERE aggregate_type IN ('deposit', 'withdrawal') AND aggregate_id IN (SELECT id::text FROM deposits WHERE chain_address_id = $1 UNION SELECT id::text FROM withdrawals WHERE user_id = $2)`, f.addressID, f.userID)
		_, _ = pool.Exec(ctx, `DELETE FROM ledger_entries WHERE wallet_id IN (SELECT id FROM wallets WHERE user_id = $1)`, f.userID)
		_, _ = pool.Exec(ctx, `DELETE FROM deposits WHERE chain_address_id = $1`, f.addressID)
		_, _ = pool.Exec(ctx, `DELETE FROM withdrawals WHERE user_id = $1`, f.userID)
		_, _ = pool.Exec(ctx, `DELETE FROM chain_addresses WHERE id = $1`, f.addressID)
		_, _ = pool.Exec(ctx, `DELETE FROM wallets WHERE user_id = $1`, f.userID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, f.userID)
	})
	return f
}

func balanceOf(t *testing.T, pool *pgxpool.Pool, ctx context.Context, userID string, currency string) (int64, int64) {
	t.Helper()
	var available, frozen int64
	err := pool.QueryRow(ctx, `SELECT available_minor, frozen_minor FROM wallets WHERE user_id = $1 AND currency = $2`, userID, currency).Scan(&available, &frozen)
	if err != nil {
		t.Fatalf("read balance: %v", err)
	}
	return available, frozen
}

func TestCreditDepositIsIdempotentByEventAndTxHash(t *testing.T) {
	pool, ctx := testPool(t)
	f := seedChainUser(t, pool, ctx, "USDT")
	service := NewService(pool)

	input := DepositInput{
		ProviderEventID: "evt-" + uuid.NewString(),
		TxHash:          "tx-" + uuid.NewString(),
		ChainCode:       "TRON",
		TokenCode:       "USDT",
		Address:         f.address,
		AmountMinor:     3000,
	}
	result, err := service.CreditDeposit(ctx, input)
	if err != nil {
		t.Fatalf("credit deposit: %v", err)
	}
	if !result.Credited || result.Status != "credited" {
		t.Fatalf("result = %+v, want credited", result)
	}
	available, _ := balanceOf(t, pool, ctx, f.userID, "USDT")
	if available != 8000 {
		t.Fatalf("available = %d, want 8000", available)
	}

	// 相同服务商事件 ID 的重复回调不得重复入账。
	again, err := service.CreditDeposit(ctx, input)
	if err != nil {
		t.Fatalf("repeat credit: %v", err)
	}
	if again.Credited || again.DepositID != result.DepositID {
		t.Fatalf("repeat result = %+v, want duplicate of %s", again, result.DepositID)
	}
	// 不同事件 ID 但相同交易哈希同样不得重复入账。
	input.ProviderEventID = "evt-" + uuid.NewString()
	third, err := service.CreditDeposit(ctx, input)
	if err != nil {
		t.Fatalf("tx hash repeat credit: %v", err)
	}
	if third.Credited || third.DepositID != result.DepositID {
		t.Fatalf("tx hash repeat result = %+v, want duplicate", third)
	}
	available, _ = balanceOf(t, pool, ctx, f.userID, "USDT")
	if available != 8000 {
		t.Fatalf("available after repeats = %d, want 8000", available)
	}

	var ledgerCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ledger_entries WHERE wallet_id = $1 AND entry_type = 'deposit_credit'`, f.walletID).Scan(&ledgerCount); err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	if ledgerCount != 1 {
		t.Fatalf("deposit ledger entries = %d, want 1", ledgerCount)
	}
	var eventCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id = $1 AND event_type = $2`, result.DepositID, events.DepositCredited).Scan(&eventCount); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("deposit events = %d, want 1", eventCount)
	}

	if _, err := service.CreditDeposit(ctx, DepositInput{ProviderEventID: "e", TxHash: "t", ChainCode: "TRON", TokenCode: "USDT", Address: "unknown-address", AmountMinor: 1}); !errors.Is(err, ErrUnknownDepositAddress) {
		t.Fatalf("unknown address error = %v, want ErrUnknownDepositAddress", err)
	}
	if _, err := service.CreditDeposit(ctx, DepositInput{ProviderEventID: "e", TxHash: "t", ChainCode: "TRON", TokenCode: "USDT", Address: f.address, AmountMinor: 0}); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("zero amount error = %v, want ErrInvalidAmount", err)
	}
}

func TestCreditDepositCreatesWalletForNewToken(t *testing.T) {
	pool, ctx := testPool(t)
	f := seedChainUser(t, pool, ctx, "TRX")
	service := NewService(pool)

	result, err := service.CreditDeposit(ctx, DepositInput{
		ProviderEventID: "evt-" + uuid.NewString(),
		TxHash:          "tx-" + uuid.NewString(),
		ChainCode:       "TRON",
		TokenCode:       "TRX",
		Address:         f.address,
		AmountMinor:     700,
	})
	if err != nil {
		t.Fatalf("credit deposit: %v", err)
	}
	if !result.Credited {
		t.Fatalf("result = %+v, want credited", result)
	}
	available, _ := balanceOf(t, pool, ctx, f.userID, "TRX")
	if available != 700 {
		t.Fatalf("TRX available = %d, want 700", available)
	}
}

func TestRequestWithdrawalFreezesFundsIdempotently(t *testing.T) {
	pool, ctx := testPool(t)
	f := seedChainUser(t, pool, ctx, "USDT")
	service := NewService(pool)

	input := WithdrawalInput{
		UserID:             f.userID,
		ClientRequestID:    "wd-" + uuid.NewString(),
		DestinationAddress: "TDestination",
		Currency:           "USDT",
		AmountMinor:        2000,
	}
	withdrawal, err := service.RequestWithdrawal(ctx, input)
	if err != nil {
		t.Fatalf("request withdrawal: %v", err)
	}
	if withdrawal.Status != "requested" || withdrawal.AmountMinor != 2000 {
		t.Fatalf("withdrawal = %+v", withdrawal)
	}
	available, frozen := balanceOf(t, pool, ctx, f.userID, "USDT")
	if available != 3000 || frozen != 2000 {
		t.Fatalf("balance = %d/%d, want 3000/2000", available, frozen)
	}

	// 相同幂等键重复申请返回既有记录，不重复冻结。
	again, err := service.RequestWithdrawal(ctx, input)
	if err != nil {
		t.Fatalf("repeat withdrawal: %v", err)
	}
	if again.WithdrawalID != withdrawal.WithdrawalID {
		t.Fatalf("repeat id = %s, want %s", again.WithdrawalID, withdrawal.WithdrawalID)
	}
	available, frozen = balanceOf(t, pool, ctx, f.userID, "USDT")
	if available != 3000 || frozen != 2000 {
		t.Fatalf("balance after repeat = %d/%d, want 3000/2000", available, frozen)
	}

	var ledgerCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ledger_entries WHERE wallet_id = $1 AND entry_type = 'withdrawal_freeze'`, f.walletID).Scan(&ledgerCount); err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	if ledgerCount != 1 {
		t.Fatalf("withdrawal ledger entries = %d, want 1", ledgerCount)
	}
	var eventCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id = $1 AND event_type = $2`, withdrawal.WithdrawalID, events.WithdrawalRequested).Scan(&eventCount); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("withdrawal events = %d, want 1", eventCount)
	}

	// 余额不足必须拒绝。
	_, err = service.RequestWithdrawal(ctx, WithdrawalInput{UserID: f.userID, ClientRequestID: "wd-" + uuid.NewString(), DestinationAddress: "T", Currency: "USDT", AmountMinor: 99999})
	if !errors.Is(err, wallet.ErrInsufficientFunds) {
		t.Fatalf("insufficient funds error = %v, want ErrInsufficientFunds", err)
	}

	// 查询与归属信息。
	found, err := service.FindWithdrawal(ctx, withdrawal.WithdrawalID)
	if err != nil {
		t.Fatalf("find withdrawal: %v", err)
	}
	if found.UserID != f.userID || found.Currency != "USDT" || found.Status != "requested" {
		t.Fatalf("found = %+v", found)
	}
	if _, err := service.FindWithdrawal(ctx, uuid.NewString()); !errors.Is(err, ErrWithdrawalNotFound) {
		t.Fatalf("missing withdrawal error = %v, want ErrWithdrawalNotFound", err)
	}
}
