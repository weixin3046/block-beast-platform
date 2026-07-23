CREATE TABLE point_withdrawals (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    wallet_id UUID NOT NULL REFERENCES wallets(id),
    client_request_id TEXT NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    status TEXT NOT NULL CHECK (status IN ('requested', 'approved', 'rejected')),
    remark TEXT NOT NULL DEFAULT '',
    reviewed_by UUID REFERENCES users(id),
    reviewed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, client_request_id)
);

CREATE INDEX point_withdrawals_status_created_idx ON point_withdrawals(status, created_at);
