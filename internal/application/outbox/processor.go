package outbox

import (
	"time"

	"github.com/block-beast/platform/internal/domain/events"
)

type Processor struct {
	outbox    events.Outbox
	publisher events.Publisher
	now       func() time.Time
}

func NewProcessor(outbox events.Outbox, publisher events.Publisher) *Processor {
	return &Processor{outbox: outbox, publisher: publisher, now: time.Now}
}

func (processor *Processor) ProcessPending(limit int) (int, error) {
	published := 0
	for _, event := range processor.outbox.Pending(limit) {
		if err := processor.publisher.Publish(event); err != nil {
			return published, err
		}
		if err := processor.outbox.MarkPublished(event.ID, processor.now().UTC()); err != nil {
			return published, err
		}
		published++
	}
	return published, nil
}
