-- 人工资金操作的幂等结果；与钱包、流水、审计在同一事务提交。
CREATE TABLE admin_wallet_adjustments (
    id UUID PRIMARY KEY,
    operator_id UUID NOT NULL REFERENCES users(id),
    request_id TEXT NOT NULL CHECK (length(request_id) BETWEEN 1 AND 128),
    user_id UUID NOT NULL REFERENCES users(id),
    currency TEXT NOT NULL REFERENCES currencies(code),
    action TEXT NOT NULL CHECK (action IN ('credit','debit','reward','penalty')),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    remark TEXT NOT NULL DEFAULT '',
    result JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(operator_id, request_id)
);

-- 升级前旧上分请求的重试也不能重复入账。
CREATE INDEX ledger_admin_credit_request_idx ON ledger_entries(operator_id,business_id)
    WHERE business_type='admin_credit';
