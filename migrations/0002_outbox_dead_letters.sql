ALTER TABLE outbox_events
    ADD COLUMN failed_at TIMESTAMPTZ,
    ADD COLUMN failure_reason TEXT;

CREATE INDEX outbox_events_failed_idx ON outbox_events(failed_at) WHERE failed_at IS NOT NULL;
