package wallet

import (
	"context"
	"errors"
	"time"
)

type EntryType string

const (
	EntryBetDebit         EntryType = "bet_debit"
	EntrySettlementCredit EntryType = "settlement_credit"
	EntryRefund           EntryType = "refund"
	EntryDeposit          EntryType = "deposit"
	EntryWithdrawal       EntryType = "withdrawal"
	EntryCommission       EntryType = "commission"
)

type LedgerEntry struct {
	EntryID     string    `json:"entry_id"`
	AccountID   string    `json:"account_id"`
	BusinessID  string    `json:"business_id"`
	Type        EntryType `json:"type"`
	AmountMinor int64     `json:"amount_minor"`
	Currency    string    `json:"currency"`
	OccurredAt  time.Time `json:"occurred_at"`
}

type AccountBalance struct {
	AccountID      string `json:"account_id"`
	Currency       string `json:"currency"`
	AvailableMinor int64  `json:"available_minor"`
	FrozenMinor    int64  `json:"frozen_minor"`
}

type Repository interface {
	DebitForBet(ctx context.Context, accountID string, businessID string, amountMinor int64, currency string) (LedgerEntry, error)
	CreditSettlement(ctx context.Context, accountID string, businessID string, amountMinor int64, currency string) (LedgerEntry, error)
	Balance(ctx context.Context, accountID string, currency string) (AccountBalance, error)
}

var ErrInsufficientFunds = errors.New("insufficient available funds")
var ErrWalletNotFound = errors.New("wallet not found")
var ErrInvalidAmount = errors.New("amount must be positive")

// Every mutation must be idempotent by BusinessID and committed atomically with its ledger entry.
