-- Existing tokens have no session binding. Require a fresh login on upgrade.
UPDATE sessions SET revoked_at=now() WHERE revoked_at IS NULL;
CREATE UNIQUE INDEX sessions_one_active_per_user ON sessions(user_id) WHERE revoked_at IS NULL;
