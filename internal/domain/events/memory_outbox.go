package events

import (
	"errors"
	"sort"
	"sync"
	"time"
)

var ErrEventNotFound = errors.New("outbox event not found")

type MemoryOutbox struct {
	mu        sync.Mutex
	events    map[string]Event
	published map[string]time.Time
}

func NewMemoryOutbox() *MemoryOutbox {
	return &MemoryOutbox{events: make(map[string]Event), published: make(map[string]time.Time)}
}

func (outbox *MemoryOutbox) Append(event Event) error {
	outbox.mu.Lock()
	defer outbox.mu.Unlock()
	if _, exists := outbox.events[event.ID]; exists {
		return nil
	}
	outbox.events[event.ID] = event
	return nil
}

func (outbox *MemoryOutbox) Pending(limit int) []Event {
	outbox.mu.Lock()
	defer outbox.mu.Unlock()
	pending := make([]Event, 0)
	for id, event := range outbox.events {
		if _, published := outbox.published[id]; !published {
			pending = append(pending, event)
		}
	}
	sort.Slice(pending, func(left int, right int) bool { return pending[left].OccurredAt.Before(pending[right].OccurredAt) })
	if limit > 0 && len(pending) > limit {
		return pending[:limit]
	}
	return pending
}

func (outbox *MemoryOutbox) MarkPublished(eventID string, publishedAt time.Time) error {
	outbox.mu.Lock()
	defer outbox.mu.Unlock()
	if _, exists := outbox.events[eventID]; !exists {
		return ErrEventNotFound
	}
	if _, alreadyPublished := outbox.published[eventID]; !alreadyPublished {
		outbox.published[eventID] = publishedAt
	}
	return nil
}
