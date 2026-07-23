-- PQPA provider metadata is kept separate from the domain order identifiers.
ALTER TABLE chain_addresses
    ADD COLUMN IF NOT EXISTS provider_address_id TEXT,
    ADD COLUMN IF NOT EXISTS provider_metadata JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE UNIQUE INDEX IF NOT EXISTS chain_addresses_provider_address_idx
    ON chain_addresses(provider_address_id)
    WHERE provider_address_id IS NOT NULL;

ALTER TABLE deposits
    ADD COLUMN IF NOT EXISTS provider_metadata JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE withdrawals
    ADD COLUMN IF NOT EXISTS provider_metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS reviewed_by UUID REFERENCES users(id),
    ADD COLUMN IF NOT EXISTS reviewed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS failure_reason TEXT;

CREATE TABLE IF NOT EXISTS provider_webhook_events (
    id UUID PRIMARY KEY,
    provider TEXT NOT NULL,
    provider_event_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ,
    processing_error TEXT,
    UNIQUE (provider, provider_event_id)
);
