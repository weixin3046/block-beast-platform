ALTER TABLE provider_supported_assets
    ADD COLUMN support_deposit BOOLEAN NOT NULL DEFAULT false;

UPDATE provider_supported_assets
SET support_deposit = enabled;
