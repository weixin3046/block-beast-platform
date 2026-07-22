-- 积分独立流水表：记录管理员上分、任务奖励等积分变动。
-- 余额仍存于 wallets 表（currency='POINTS'），此表仅记录管理端与任务端流水，便于独立查询。
CREATE TABLE points_ledger (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    business_type TEXT NOT NULL,
    business_id TEXT NOT NULL,
    amount_minor BIGINT NOT NULL,
    balance_after_minor BIGINT NOT NULL,
    remark TEXT NOT NULL DEFAULT '',
    operator_id UUID REFERENCES users(id),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, business_type, business_id)
);

CREATE INDEX points_ledger_user_occurred_idx ON points_ledger(user_id, occurred_at DESC);

-- 体力独立流水表：记录管理员充值、签到奖励、活动消耗等体力变动。
-- 余额存于 wallets 表（currency='STAMINA'）。
CREATE TABLE stamina_ledger (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    business_type TEXT NOT NULL,
    business_id TEXT NOT NULL,
    amount_minor BIGINT NOT NULL,
    balance_after_minor BIGINT NOT NULL,
    remark TEXT NOT NULL DEFAULT '',
    operator_id UUID REFERENCES users(id),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, business_type, business_id)
);

CREATE INDEX stamina_ledger_user_occurred_idx ON stamina_ledger(user_id, occurred_at DESC);

-- 每日签到记录：按 (user_id, checkin_date) 唯一保证幂等。
CREATE TABLE checkin_records (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    checkin_date DATE NOT NULL,
    reward_minor BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, checkin_date)
);
