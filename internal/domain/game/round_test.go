package game

import (
	"errors"
	"testing"
	"time"
)

func TestRoundTransitionsInOrder(t *testing.T) {
	now := time.Now().UTC()
	round := Round{RoundID: "round-1", Status: RoundOpen, BetClosesAt: now.Add(-time.Second)}
	if err := round.Close(now); err != nil {
		t.Fatalf("close round: %v", err)
	}
	if err := round.BeginSettlement(); err != nil {
		t.Fatalf("begin settlement: %v", err)
	}
	if err := round.CompleteSettlement([]string{"red"}, now); err != nil {
		t.Fatalf("complete settlement: %v", err)
	}
	if round.Status != RoundSettled || len(round.Outcome) != 1 {
		t.Fatalf("round = %#v, want settled outcome", round)
	}
}

func TestRoundRejectsBetAtOrAfterClose(t *testing.T) {
	now := time.Now().UTC()
	round := Round{RoundID: "round-1", Status: RoundOpen, BetClosesAt: now}
	err := round.ValidateBet(100, now)
	if !errors.Is(err, ErrBettingClosed) {
		t.Fatalf("error = %v, want betting closed", err)
	}
}

func TestRoundRejectsSkippedTransition(t *testing.T) {
	round := Round{RoundID: "round-1", Status: RoundOpen, BetClosesAt: time.Now().Add(time.Hour)}
	if err := round.BeginSettlement(); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("error = %v, want invalid transition", err)
	}
}
