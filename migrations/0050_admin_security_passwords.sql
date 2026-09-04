-- 后台全局操作密码，与 users.secondary_password_hash 完全独立；无默认密码。
CREATE TABLE admin_security_passwords (
    level TEXT PRIMARY KEY CHECK (level IN ('first','second')),
    password_hash TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 0,
    updated_by UUID REFERENCES users(id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO admin_security_passwords(level) VALUES ('first'),('second');

-- 按操作账号与验证用途限流，避免单个账号锁死整个后台。
CREATE TABLE admin_security_attempts (
    actor_id UUID NOT NULL REFERENCES users(id),
    scope TEXT NOT NULL CHECK (scope IN ('first','second','manage')),
    failures INTEGER NOT NULL DEFAULT 0,
    window_started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    blocked_until TIMESTAMPTZ,
    PRIMARY KEY(actor_id,scope)
);
