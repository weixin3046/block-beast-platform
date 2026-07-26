ALTER TABLE uploads
    ADD COLUMN status TEXT NOT NULL DEFAULT 'confirmed'
        CHECK (status IN ('pending', 'confirmed', 'expired')),
    ADD COLUMN expires_at TIMESTAMPTZ;

CREATE INDEX uploads_owner_created_idx ON uploads (owner_user_id, created_at DESC);
CREATE INDEX uploads_pending_expiry_idx ON uploads (expires_at)
    WHERE status = 'pending';
