CREATE TABLE IF NOT EXISTS provider_supported_assets (
    id UUID PRIMARY KEY,
    provider TEXT NOT NULL,
    chain_code TEXT NOT NULL,
    token_code TEXT NOT NULL,
    token_name TEXT,
    decimals INTEGER NOT NULL DEFAULT 0 CHECK (decimals >= 0),
    enabled BOOLEAN NOT NULL DEFAULT true,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    synced_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, chain_code, token_code)
);

CREATE INDEX IF NOT EXISTS provider_supported_assets_enabled_idx
    ON provider_supported_assets(provider, enabled, chain_code, token_code);
