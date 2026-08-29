ALTER TABLE users ADD COLUMN IF NOT EXISTS is_virtual BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS user_login_history (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    ip_address INET NOT NULL,
    audience TEXT NOT NULL CHECK (audience IN ('player', 'admin')),
    logged_in_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS user_login_history_user_time_idx
    ON user_login_history(user_id, logged_in_at DESC);
CREATE INDEX IF NOT EXISTS user_login_history_ip_time_idx
    ON user_login_history(ip_address, logged_in_at DESC);

CREATE TABLE IF NOT EXISTS virtual_account_automations (
    user_id UUID PRIMARY KEY REFERENCES users(id),
    enabled BOOLEAN NOT NULL DEFAULT false,
    currency TEXT NOT NULL DEFAULT 'POINTS',
    stake_minor BIGINT NOT NULL DEFAULT 100 CHECK (stake_minor > 0),
    game_type_codes TEXT[] NOT NULL DEFAULT '{}',
    interval_seconds INTEGER NOT NULL DEFAULT 30 CHECK (interval_seconds BETWEEN 3 AND 86400),
    last_run_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS users_virtual_idx ON users(is_virtual) WHERE is_virtual=true;
