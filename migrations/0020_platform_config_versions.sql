ALTER TABLE platform_configs
    ADD COLUMN visibility TEXT NOT NULL DEFAULT 'internal'
        CHECK (visibility IN ('public', 'internal')),
    ADD COLUMN version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0);

CREATE INDEX platform_configs_visibility_key_idx ON platform_configs (visibility, key);
