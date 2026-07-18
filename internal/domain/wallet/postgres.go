package wallet

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (repository *PostgresRepository) DebitForBet(ctx context.Context, accountID string, businessID string, amountMinor int64, currency string) (LedgerEntry, error) {
	return repository.mutate(ctx, accountID, businessID, amountMinor, currency, "bet", EntryBetDebit, -amountMinor)
}

func (repository *PostgresRepository) CreditSettlement(ctx context.Context, accountID string, businessID string, amountMinor int64, currency string) (LedgerEntry, error) {
	return repository.mutate(ctx, accountID, businessID, amountMinor, currency, "settlement", EntrySettlementCredit, amountMinor)
}

func (repository *PostgresRepository) Balance(ctx context.Context, accountID string, currency string) (AccountBalance, error) {
	var balance AccountBalance
	err := repository.pool.QueryRow(ctx, `
		SELECT user_id, currency, available_minor, frozen_minor
		FROM wallets
		WHERE user_id = $1 AND currency = $2`, accountID, currency).
		Scan(&balance.AccountID, &balance.Currency, &balance.AvailableMinor, &balance.FrozenMinor)
	if errors.Is(err, pgx.ErrNoRows) {
		return AccountBalance{}, ErrWalletNotFound
	}
	if err != nil {
		return AccountBalance{}, err
	}
	return balance, nil
}

func (repository *PostgresRepository) mutate(ctx context.Context, accountID string, businessID string, amountMinor int64, currency string, businessType string, entryType EntryType, balanceDelta int64) (LedgerEntry, error) {
	if amountMinor <= 0 {
		return LedgerEntry{}, ErrInvalidAmount
	}

	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return LedgerEntry{}, err
	}
	defer tx.Rollback(ctx)

	existing, err := findEntry(ctx, tx, accountID, businessID, currency, businessType, entryType)
	if err == nil {
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return LedgerEntry{}, err
	}

	var walletID string
	var availableMinor int64
	err = tx.QueryRow(ctx, `
		SELECT id, available_minor
		FROM wallets
		WHERE user_id = $1 AND currency = $2
		FOR UPDATE`, accountID, currency).Scan(&walletID, &availableMinor)
	if errors.Is(err, pgx.ErrNoRows) {
		return LedgerEntry{}, ErrWalletNotFound
	}
	if err != nil {
		return LedgerEntry{}, err
	}

	existing, err = findEntry(ctx, tx, accountID, businessID, currency, businessType, entryType)
	if err == nil {
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return LedgerEntry{}, err
	}
	if availableMinor+balanceDelta < 0 {
		return LedgerEntry{}, ErrInsufficientFunds
	}

	availableMinor += balanceDelta
	_, err = tx.Exec(ctx, `
		UPDATE wallets
		SET available_minor = $2, version = version + 1, updated_at = now()
		WHERE id = $1`, walletID, availableMinor)
	if err != nil {
		return LedgerEntry{}, err
	}

	entry := LedgerEntry{
		EntryID:     uuid.NewString(),
		AccountID:   accountID,
		BusinessID:  businessID,
		Type:        entryType,
		AmountMinor: balanceDelta,
		Currency:    currency,
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO ledger_entries (
			id, wallet_id, business_type, business_id, entry_type, amount_minor, balance_after_minor
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING occurred_at`, entry.EntryID, walletID, businessType, businessID, entryType, entry.AmountMinor, availableMinor).
		Scan(&entry.OccurredAt)
	if err != nil {
		return LedgerEntry{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return LedgerEntry{}, err
	}
	return entry, nil
}

func findEntry(ctx context.Context, tx pgx.Tx, accountID string, businessID string, currency string, businessType string, entryType EntryType) (LedgerEntry, error) {
	var entry LedgerEntry
	err := tx.QueryRow(ctx, `
		SELECT ledger_entries.id, wallets.user_id, ledger_entries.business_id, ledger_entries.entry_type,
			ledger_entries.amount_minor, wallets.currency, ledger_entries.occurred_at
		FROM ledger_entries
		JOIN wallets ON wallets.id = ledger_entries.wallet_id
		WHERE wallets.user_id = $1
			AND wallets.currency = $2
			AND ledger_entries.business_type = $3
			AND ledger_entries.business_id = $4
			AND ledger_entries.entry_type = $5`, accountID, currency, businessType, businessID, entryType).
		Scan(&entry.EntryID, &entry.AccountID, &entry.BusinessID, &entry.Type, &entry.AmountMinor, &entry.Currency, &entry.OccurredAt)
	return entry, err
}

var _ Repository = (*PostgresRepository)(nil)
