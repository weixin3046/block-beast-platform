ALTER TABLE sessions
    ADD COLUMN audience TEXT NOT NULL DEFAULT 'player'
        CHECK (audience IN ('player', 'admin'));

CREATE INDEX sessions_user_audience_active_idx
    ON sessions(user_id, audience, expires_at)
    WHERE revoked_at IS NULL;
