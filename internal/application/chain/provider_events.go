package chain

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

func (service *Service) beginProviderEvent(ctx context.Context, eventID, eventType string, payload any) (alreadyProcessed bool, err error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}
	command, err := service.pool.Exec(ctx, `
		INSERT INTO provider_webhook_events (id, provider, provider_event_id, event_type, payload)
		VALUES ($1, 'pqpa', $2, $3, $4)
		ON CONFLICT (provider, provider_event_id) DO NOTHING`,
		uuid.NewString(), eventID, eventType, raw)
	if err != nil {
		return false, err
	}
	if command.RowsAffected() > 0 {
		return false, nil
	}
	err = service.pool.QueryRow(ctx, `
		SELECT processed_at IS NOT NULL
		FROM provider_webhook_events
		WHERE provider='pqpa' AND provider_event_id=$1`, eventID).Scan(&alreadyProcessed)
	return alreadyProcessed, err
}

func (service *Service) finishProviderEvent(ctx context.Context, eventID string, processingErr error) {
	if processingErr == nil {
		_, _ = service.pool.Exec(ctx, `
			UPDATE provider_webhook_events
			SET processed_at=$2, processing_error=NULL
			WHERE provider='pqpa' AND provider_event_id=$1`,
			eventID, time.Now().UTC())
		return
	}
	_, _ = service.pool.Exec(ctx, `
		UPDATE provider_webhook_events
		SET processing_error=$2
		WHERE provider='pqpa' AND provider_event_id=$1`,
		eventID, processingErr.Error())
}
