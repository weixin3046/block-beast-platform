package game

import (
	"errors"
	"time"
)

type RoundStatus string

const (
	RoundOpen      RoundStatus = "open"
	RoundClosed    RoundStatus = "closed"
	RoundSettling  RoundStatus = "settling"
	RoundSettled   RoundStatus = "settled"
	RoundCancelled RoundStatus = "cancelled"
)

type Round struct {
	RoundID      string
	GameType     string
	Sequence     int64
	Status       RoundStatus
	BetClosesAt  time.Time
	SettledAt    *time.Time
	Outcome      []string
}

type Bet struct {
	BetID       string
	RoundID     string
	AccountID   string
	Selection   string
	StakeMinor  int64
	Currency    string
	PlacedAt    time.Time
}

var ErrInvalidTransition = errors.New("invalid round state transition")
var ErrBettingClosed = errors.New("betting is closed")
var ErrInvalidStake = errors.New("stake must be positive")

func (round *Round) Close(now time.Time) error {
	if round.Status != RoundOpen || now.Before(round.BetClosesAt) {
		return ErrInvalidTransition
	}
	round.Status = RoundClosed
	return nil
}

func (round *Round) BeginSettlement() error {
	if round.Status != RoundClosed {
		return ErrInvalidTransition
	}
	round.Status = RoundSettling
	return nil
}

func (round *Round) CompleteSettlement(outcome []string, settledAt time.Time) error {
	if round.Status != RoundSettling || len(outcome) == 0 {
		return ErrInvalidTransition
	}
	round.Status = RoundSettled
	round.Outcome = outcome
	round.SettledAt = &settledAt
	return nil
}

func (round Round) CanAcceptBet(now time.Time) bool {
	return round.Status == RoundOpen && now.Before(round.BetClosesAt)
}

func (round Round) ValidateBet(stakeMinor int64, now time.Time) error {
	if stakeMinor <= 0 {
		return ErrInvalidStake
	}
	if !round.CanAcceptBet(now) {
		return ErrBettingClosed
	}
	return nil
}

// A round can only transition forward; settlement uses an idempotency key of round ID plus version.