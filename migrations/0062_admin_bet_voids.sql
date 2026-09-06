-- 后台单笔作废保留投注历史，并把退款、审计和幂等结果放在同一事务。
ALTER TABLE bets DROP CONSTRAINT IF EXISTS bets_status_check;
ALTER TABLE bets ADD CONSTRAINT bets_status_check
    CHECK (status IN ('accepted','cancelled','lost','won','refunded','voided'));

CREATE TABLE admin_bet_voids (
    id UUID PRIMARY KEY,
    operator_id UUID NOT NULL REFERENCES users(id),
    request_id TEXT NOT NULL CHECK (length(request_id) BETWEEN 1 AND 128),
    bet_id UUID NOT NULL REFERENCES bets(id),
    user_id UUID NOT NULL REFERENCES users(id),
    wallet_id UUID NOT NULL REFERENCES wallets(id),
    round_id UUID NOT NULL REFERENCES rounds(id),
    round_sequence BIGINT NOT NULL,
    game_type TEXT NOT NULL,
    currency TEXT NOT NULL REFERENCES currencies(code),
    stake_minor BIGINT NOT NULL CHECK (stake_minor > 0),
    refund_minor BIGINT NOT NULL CHECK (refund_minor >= 0),
    is_simulated BOOLEAN NOT NULL,
    reason TEXT NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 2000),
    result JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (operator_id, request_id),
    UNIQUE (bet_id)
);

CREATE INDEX admin_bet_voids_created_idx
    ON admin_bet_voids(created_at DESC, id DESC);
