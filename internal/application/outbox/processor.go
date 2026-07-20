package outbox

import (
	"fmt"
	"time"

	"github.com/block-beast/platform/internal/domain/events"
)

type Processor struct {
	outbox      events.Outbox
	publisher   events.Publisher
	now         func() time.Time
	maxAttempts int
}

func NewProcessor(outbox events.Outbox, publisher events.Publisher) *Processor {
	return &Processor{outbox: outbox, publisher: publisher, now: time.Now, maxAttempts: 5}
}

func (processor *Processor) ProcessPending(limit int) (int, error) {
	published := 0
	for _, event := range processor.outbox.Pending(limit) {
		if err := processor.publisher.Publish(event); err != nil {
			if _, recordErr := processor.outbox.RecordFailure(event.ID, processor.now().UTC(), err.Error(), processor.maxAttempts); recordErr != nil {
				return published, fmt.Errorf("record publish failure: %w", recordErr)
			}
			return published, err
		}
		if err := processor.outbox.MarkPublished(event.ID, processor.now().UTC()); err != nil {
			return published, err
		}
		published++
	}
	return published, nil
}
