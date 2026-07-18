package events

import (
	"testing"
	"time"
)

func TestMemoryOutboxPublishesEachEventOnce(t *testing.T) {
	outbox := NewMemoryOutbox()
	event := Event{ID: "event-1", Type: BetPlaced, AggregateType: "bet", AggregateID: "bet-1", OccurredAt: time.Now().UTC()}
	if err := outbox.Append(event); err != nil {
		t.Fatalf("append: %v", err)
	}
	if got := outbox.Pending(10); len(got) != 1 || got[0].ID != event.ID {
		t.Fatalf("pending = %#v, want event-1", got)
	}
	if err := outbox.MarkPublished(event.ID, time.Now().UTC()); err != nil {
		t.Fatalf("mark published: %v", err)
	}
	if err := outbox.MarkPublished(event.ID, time.Now().UTC()); err != nil {
		t.Fatalf("repeated publish confirmation: %v", err)
	}
	if got := outbox.Pending(10); len(got) != 0 {
		t.Fatalf("pending = %#v, want none", got)
	}
}
