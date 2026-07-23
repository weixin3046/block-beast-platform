CREATE TABLE commission_adjustments (
    id UUID PRIMARY KEY,
    request_id TEXT NOT NULL UNIQUE,
    agent_user_id UUID NOT NULL REFERENCES users(id),
    currency TEXT NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    remark TEXT NOT NULL DEFAULT '',
    operator_id UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX commission_adjustments_agent_created_idx
    ON commission_adjustments(agent_user_id, created_at DESC);
