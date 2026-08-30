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
	RoundID     string      `json:"round_id"`
	GameType    string      `json:"game_type"`
	Sequence    int64       `json:"sequence"`
	Status      RoundStatus `json:"status"`
	BetClosesAt time.Time   `json:"bet_closes_at"`
	ResultAt    time.Time   `json:"result_at"`
	SettledAt   *time.Time  `json:"settled_at,omitempty"`
	Outcome     []string    `json:"outcome,omitempty"`
}

type RoundState struct {
	Current  *Round `json:"current"`
	Previous *Round `json:"previous"`
}

type HashTrendItem struct {
	Sequence  int64     `json:"sequence"`
	Digit     int       `json:"digit"`
	Size      string    `json:"size"`
	Parity    string    `json:"parity"`
	SettledAt time.Time `json:"settled_at"`
}

type HashTrendStreak struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

type HashTrendSummary struct {
	DigitOmissions map[string]int  `json:"digit_omissions"`
	SizeStreak     HashTrendStreak `json:"size_streak"`
	ParityStreak   HashTrendStreak `json:"parity_streak"`
}

type HashTrend struct {
	GameType   string           `json:"game_type"`
	ServerTime time.Time        `json:"server_time"`
	Items      []HashTrendItem  `json:"items"`
	Summary    HashTrendSummary `json:"summary"`
}

type Bet struct {
	BetID      string
	RoundID    string
	AccountID  string
	Selection  string
	StakeMinor int64
	Currency   string
	PlacedAt   time.Time
}

var ErrInvalidTransition = errors.New("invalid round state transition")
var ErrRoundNotFound = errors.New("round not found")
var ErrBettingClosed = errors.New("betting is closed")
var ErrInvalidStake = errors.New("stake must be positive")
var ErrInvalidTrendLimit = errors.New("trend limit must be between 1 and 200")

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
