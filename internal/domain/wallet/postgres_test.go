package wallet

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresRepositoryDebitForBetIsAtomicAndIdempotent(t *testing.T) {
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

	accountID := uuid.NewString()
	walletID := uuid.NewString()
	_, err = pool.Exec(ctx, `INSERT INTO users (id, display_name) VALUES ($1, $2)`, accountID, "wallet test player")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO wallets (id, user_id, currency, available_minor) VALUES ($1, $2, $3, $4)`, walletID, accountID, "USDT", 10_000)
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM ledger_entries WHERE wallet_id = $1`, walletID)
		_, _ = pool.Exec(ctx, `DELETE FROM wallets WHERE id = $1`, walletID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, accountID)
	})

	repository := NewPostgresRepository(pool)
	first, err := repository.DebitForBet(ctx, accountID, "bet-101", 2_500, "USDT")
	if err != nil {
		t.Fatalf("first debit: %v", err)
	}
	second, err := repository.DebitForBet(ctx, accountID, "bet-101", 2_500, "USDT")
	if err != nil {
		t.Fatalf("repeated debit: %v", err)
	}
	if first.EntryID != second.EntryID {
		t.Fatal("same business ID must return the original ledger entry")
	}

	balance, err := repository.Balance(ctx, accountID, "USDT")
	if err != nil {
		t.Fatalf("read balance: %v", err)
	}
	if balance.AvailableMinor != 7_500 {
		t.Fatalf("available balance = %d, want 7500", balance.AvailableMinor)
	}

	var entryCount int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM ledger_entries WHERE wallet_id = $1`, walletID).Scan(&entryCount)
	if err != nil {
		t.Fatalf("count ledger entries: %v", err)
	}
	if entryCount != 1 {
		t.Fatalf("ledger entries = %d, want 1", entryCount)
	}

	start := make(chan struct{})
	results := make(chan LedgerEntry, 2)
	errResults := make(chan error, 2)
	var waitGroup sync.WaitGroup
	for range 2 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			entry, err := repository.DebitForBet(ctx, accountID, "bet-103", 500, "USDT")
			if err != nil {
				errResults <- err
				return
			}
			results <- entry
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)
	close(errResults)
	for err := range errResults {
		t.Fatalf("concurrent debit: %v", err)
	}

	concurrentEntries := make([]LedgerEntry, 0, 2)
	for entry := range results {
		concurrentEntries = append(concurrentEntries, entry)
	}
	if len(concurrentEntries) != 2 {
		t.Fatalf("concurrent entries = %d, want 2", len(concurrentEntries))
	}
	if concurrentEntries[0].EntryID != concurrentEntries[1].EntryID {
		t.Fatal("concurrent requests with the same business ID must return the original ledger entry")
	}

	_, err = repository.DebitForBet(ctx, accountID, "bet-102", 7_501, "USDT")
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("error = %v, want insufficient funds", err)
	}
	balance, err = repository.Balance(ctx, accountID, "USDT")
	if err != nil {
		t.Fatalf("read balance after rejected debit: %v", err)
	}
	if balance.AvailableMinor != 7_000 {
		t.Fatalf("available balance after rejected debit = %d, want 7000", balance.AvailableMinor)
	}
}
