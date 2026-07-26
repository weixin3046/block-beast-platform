CREATE TABLE auth_login_guards (
    login_name TEXT PRIMARY KEY,
    failed_attempts INTEGER NOT NULL CHECK (failed_attempts >= 0),
    window_started_at TIMESTAMPTZ NOT NULL,
    locked_until TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX auth_login_guards_locked_until_idx
    ON auth_login_guards (locked_until)
    WHERE locked_until IS NOT NULL;

CREATE INDEX auth_login_guards_updated_at_idx
    ON auth_login_guards (updated_at);
