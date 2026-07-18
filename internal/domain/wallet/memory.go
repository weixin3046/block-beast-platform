package wallet

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

type MemoryRepository struct {
	mu       sync.Mutex
	balances map[string]AccountBalance
	entries  map[string]LedgerEntry
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		balances: make(map[string]AccountBalance),
		entries:  make(map[string]LedgerEntry),
	}
}

func (repository *MemoryRepository) Seed(accountID string, currency string, availableMinor int64) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.balances[balanceKey(accountID, currency)] = AccountBalance{
		AccountID: accountID, Currency: currency, AvailableMinor: availableMinor,
	}
}

func (repository *MemoryRepository) DebitForBet(_ context.Context, accountID string, businessID string, amountMinor int64, currency string) (LedgerEntry, error) {
	if amountMinor <= 0 {
		return LedgerEntry{}, ErrInvalidAmount
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()

	entryKey := idempotencyKey(accountID, businessID, EntryBetDebit)
	if entry, ok := repository.entries[entryKey]; ok {
		return entry, nil
	}
	balance, ok := repository.balances[balanceKey(accountID, currency)]
	if !ok {
		return LedgerEntry{}, ErrWalletNotFound
	}
	if balance.AvailableMinor < amountMinor {
		return LedgerEntry{}, ErrInsufficientFunds
	}
	balance.AvailableMinor -= amountMinor
	repository.balances[balanceKey(accountID, currency)] = balance
	entry := LedgerEntry{EntryID: newID(), AccountID: accountID, BusinessID: businessID, Type: EntryBetDebit, AmountMinor: -amountMinor, Currency: currency, OccurredAt: time.Now().UTC()}
	repository.entries[entryKey] = entry
	return entry, nil
}

func (repository *MemoryRepository) CreditSettlement(_ context.Context, accountID string, businessID string, amountMinor int64, currency string) (LedgerEntry, error) {
	if amountMinor <= 0 {
		return LedgerEntry{}, ErrInvalidAmount
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()

	entryKey := idempotencyKey(accountID, businessID, EntrySettlementCredit)
	if entry, ok := repository.entries[entryKey]; ok {
		return entry, nil
	}
	balance, ok := repository.balances[balanceKey(accountID, currency)]
	if !ok {
		return LedgerEntry{}, ErrWalletNotFound
	}
	balance.AvailableMinor += amountMinor
	repository.balances[balanceKey(accountID, currency)] = balance
	entry := LedgerEntry{EntryID: newID(), AccountID: accountID, BusinessID: businessID, Type: EntrySettlementCredit, AmountMinor: amountMinor, Currency: currency, OccurredAt: time.Now().UTC()}
	repository.entries[entryKey] = entry
	return entry, nil
}

func (repository *MemoryRepository) Balance(_ context.Context, accountID string, currency string) (AccountBalance, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	balance, ok := repository.balances[balanceKey(accountID, currency)]
	if !ok {
		return AccountBalance{}, ErrWalletNotFound
	}
	return balance, nil
}

func balanceKey(accountID string, currency string) string { return accountID + ":" + currency }
func idempotencyKey(accountID string, businessID string, entryType EntryType) string {
	return accountID + ":" + businessID + ":" + string(entryType)
}

func newID() string {
	buffer := make([]byte, 16)
	_, _ = rand.Read(buffer)
	return hex.EncodeToString(buffer)
}