package wallet

import (
	"context"
	"errors"
	"testing"
)

func TestDebitForBetIsIdempotent(t *testing.T) {
	repository := NewMemoryRepository()
	repository.Seed("player-1", "USDT", 10_000)

	first, err := repository.DebitForBet(context.Background(), "player-1", "bet-101", 2_500, "USDT")
	if err != nil {
		t.Fatalf("first debit: %v", err)
	}
	second, err := repository.DebitForBet(context.Background(), "player-1", "bet-101", 2_500, "USDT")
	if err != nil {
		t.Fatalf("repeated debit: %v", err)
	}
	if first.EntryID != second.EntryID {
		t.Fatal("same business ID must return the original ledger entry")
	}
	balance, err := repository.Balance(context.Background(), "player-1", "USDT")
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if balance.AvailableMinor != 7_500 {
		t.Fatalf("available balance = %d, want 7500", balance.AvailableMinor)
	}
}

func TestDebitForBetRejectsInsufficientFundsWithoutMutation(t *testing.T) {
	repository := NewMemoryRepository()
	repository.Seed("player-1", "USDT", 100)

	_, err := repository.DebitForBet(context.Background(), "player-1", "bet-102", 101, "USDT")
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("error = %v, want insufficient funds", err)
	}
	balance, err := repository.Balance(context.Background(), "player-1", "USDT")
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if balance.AvailableMinor != 100 {
		t.Fatalf("available balance = %d, want 100", balance.AvailableMinor)
	}
}