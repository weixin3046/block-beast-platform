package events

import "time"

type Event struct {
	ID            string
	Type          string
	AggregateType string
	AggregateID   string
	OccurredAt    time.Time
	Payload       []byte
}

const (
	BetPlaced       = "game.bet.placed"
	RoundClosed     = "game.round.closed"
	RoundSettling   = "game.round.settling"
	RoundSettled    = "game.round.settled"
	LedgerCommitted = "wallet.ledger.committed"
	DepositCredited = "chain.deposit.credited"
	WithdrawalSent  = "chain.withdrawal.sent"
)

// Publisher is backed by a transactional outbox before events reach NATS JetStream.
type Publisher interface {
	Publish(event Event) error
}

type Outbox interface {
	Append(event Event) error
	Pending(limit int) []Event
	MarkPublished(eventID string, publishedAt time.Time) error
}
