ALTER TABLE provider_supported_assets
    ADD COLUMN provider_chain_token_id BIGINT,
    ADD COLUMN support_withdraw BOOLEAN NOT NULL DEFAULT false;

CREATE UNIQUE INDEX provider_supported_assets_chain_token_idx
    ON provider_supported_assets(provider, provider_chain_token_id)
    WHERE provider_chain_token_id IS NOT NULL;

ALTER TABLE chain_addresses
    ADD COLUMN memo TEXT;

ALTER TABLE withdrawals
    ADD COLUMN chain_code TEXT,
    ADD COLUMN token_code TEXT,
    ADD COLUMN destination_memo TEXT,
    ADD COLUMN provider_chain_token_id BIGINT,
    ADD COLUMN token_decimals INTEGER CHECK (token_decimals BETWEEN 0 AND 18),
    ADD COLUMN provider_fee_minor BIGINT CHECK (provider_fee_minor IS NULL OR provider_fee_minor >= 0);

CREATE INDEX withdrawals_provider_reconcile_idx
    ON withdrawals(status, created_at)
    WHERE status = 'broadcasted';
