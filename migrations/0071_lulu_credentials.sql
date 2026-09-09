-- Credentials are AES-GCM encrypted by the application, never plaintext.
ALTER TABLE lulu_config
 ADD COLUMN api_url TEXT NOT NULL DEFAULT '',
 ADD COLUMN token_cipher BYTEA,
 ADD COLUMN protocol_cipher BYTEA,
 ADD COLUMN scan_start_at TIMESTAMPTZ;
-- Existing configuration requires administrator credential initialization.
UPDATE lulu_config SET enabled=false,version=version+1,updated_at=now();
