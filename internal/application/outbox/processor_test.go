package outbox

import (
	"errors"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/domain/events"
)

func TestProcessorMarksEventsPublishedAfterDelivery(t *testing.T) {
	memoryOutbox := events.NewMemoryOutbox()
	first := events.Event{ID: "event-1", Type: events.BetPlaced, OccurredAt: time.Now().UTC()}
	second := events.Event{ID: "event-2", Type: events.RoundSettled, OccurredAt: time.Now().UTC().Add(time.Second)}
	if err := memoryOutbox.Append(first); err != nil {
		t.Fatalf("append first event: %v", err)
	}
	if err := memoryOutbox.Append(second); err != nil {
		t.Fatalf("append second event: %v", err)
	}
	publisher := &recordingPublisher{}
	processor := NewProcessor(memoryOutbox, publisher)
	processor.now = func() time.Time { return time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC) }

	published, err := processor.ProcessPending(10)
	if err != nil {
		t.Fatalf("process pending events: %v", err)
	}
	if published != 2 {
		t.Fatalf("published = %d, want 2", published)
	}
	if len(publisher.events) != 2 {
		t.Fatalf("published events = %d, want 2", len(publisher.events))
	}
	if pending := memoryOutbox.Pending(10); len(pending) != 0 {
		t.Fatalf("pending events = %#v, want none", pending)
	}
}

func TestProcessorLeavesFailedEventPending(t *testing.T) {
	memoryOutbox := events.NewMemoryOutbox()
	event := events.Event{ID: "event-1", Type: events.BetPlaced, OccurredAt: time.Now().UTC()}
	if err := memoryOutbox.Append(event); err != nil {
		t.Fatalf("append event: %v", err)
	}
	publisher := &recordingPublisher{err: errors.New("NATS unavailable")}

	published, err := NewProcessor(memoryOutbox, publisher).ProcessPending(10)
	if !errors.Is(err, publisher.err) {
		t.Fatalf("error = %v, want publisher error", err)
	}
	if published != 0 {
		t.Fatalf("published = %d, want 0", published)
	}
	if pending := memoryOutbox.Pending(10); len(pending) != 1 || pending[0].ID != event.ID {
		t.Fatalf("pending events = %#v, want event-1", pending)
	}
}

type recordingPublisher struct {
	events []events.Event
	err    error
}

func (publisher *recordingPublisher) Publish(event events.Event) error {
	if publisher.err != nil {
		return publisher.err
	}
	publisher.events = append(publisher.events, event)
	return nil
}
