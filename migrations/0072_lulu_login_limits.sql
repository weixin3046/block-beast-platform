-- Shared across API replicas; no SMS code or token is stored here.
CREATE TABLE lulu_login_limits (
 action TEXT PRIMARY KEY CHECK(action IN ('send','login')),
 next_allowed_at TIMESTAMPTZ NOT NULL
);
