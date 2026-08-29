DROP TABLE IF EXISTS lucky_spin_records CASCADE;
DROP TABLE IF EXISTS spin_prizes CASCADE;
DROP TABLE IF EXISTS spin_configs CASCADE;
DROP TABLE IF EXISTS bet_task_reward_records CASCADE;
DROP TABLE IF EXISTS user_daily_bet_progress CASCADE;
DROP TABLE IF EXISTS bet_task_configs CASCADE;

UPDATE platform_configs
SET value = jsonb_set(
    value - 'spin_prizes',
    '{items}',
    COALESCE((SELECT jsonb_agg(item) FROM jsonb_array_elements(value->'items') item WHERE item->>'id' <> 'lucky-spin'),'[]'::jsonb)
)
WHERE key='activity.center';

CREATE TABLE bet_task_configs (
    id UUID PRIMARY KEY,
    accumulation_currency TEXT NOT NULL CHECK (length(trim(accumulation_currency)) > 0),
    threshold_minor BIGINT NOT NULL CHECK (threshold_minor > 0),
    reward_currency TEXT NOT NULL CHECK (length(trim(reward_currency)) > 0),
    reward_minor BIGINT NOT NULL CHECK (reward_minor > 0),
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(accumulation_currency,threshold_minor)
);

CREATE TABLE user_daily_bet_progress (
    user_id UUID NOT NULL REFERENCES users(id),
    bet_date DATE NOT NULL,
    accumulation_currency TEXT NOT NULL CHECK (length(trim(accumulation_currency)) > 0),
    total_stake_minor BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(user_id,bet_date,accumulation_currency)
);

CREATE TABLE bet_task_reward_records (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    bet_date DATE NOT NULL,
    config_id UUID NOT NULL REFERENCES bet_task_configs(id),
    accumulation_currency TEXT NOT NULL CHECK (length(trim(accumulation_currency)) > 0),
    reward_currency TEXT NOT NULL CHECK (length(trim(reward_currency)) > 0),
    reward_minor BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(user_id,bet_date,config_id)
);

INSERT INTO bet_task_configs(id,accumulation_currency,threshold_minor,reward_currency,reward_minor)
VALUES
    (gen_random_uuid(),'POINTS',10000,'STAMINA',10),
    (gen_random_uuid(),'POINTS',50000,'STAMINA',60),
    (gen_random_uuid(),'POINTS',200000,'STAMINA',300);

INSERT INTO wallets(id,user_id,currency)
SELECT gen_random_uuid(),u.id,c.currency
FROM users u CROSS JOIN (VALUES('USDT_STAMINA'),('JADE_STAMINA'),('ORIGIN_STONE_STAMINA')) c(currency)
ON CONFLICT(user_id,currency) DO NOTHING;

CREATE TABLE spin_configs (
    id UUID PRIMARY KEY,
    code TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    cost_currency TEXT NOT NULL CHECK (length(trim(cost_currency)) > 0),
    cost_minor BIGINT NOT NULL CHECK (cost_minor > 0),
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE spin_prizes (
    id UUID PRIMARY KEY,
    spin_id UUID NOT NULL REFERENCES spin_configs(id) ON DELETE CASCADE,
    code TEXT NOT NULL,
    label TEXT NOT NULL,
    reward_currency TEXT NOT NULL CHECK (length(trim(reward_currency)) > 0),
    reward_minor BIGINT NOT NULL CHECK (reward_minor > 0),
    weight BIGINT NOT NULL CHECK (weight > 0),
    sort_order INTEGER NOT NULL DEFAULT 0,
    UNIQUE(spin_id,code)
);

CREATE TABLE lucky_spin_records (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    spin_id UUID NOT NULL REFERENCES spin_configs(id),
    client_request_id TEXT NOT NULL,
    prize_id TEXT NOT NULL,
    prize_label TEXT NOT NULL,
    reward_currency TEXT NOT NULL CHECK (length(trim(reward_currency)) > 0),
    reward_minor BIGINT NOT NULL CHECK (reward_minor > 0),
    cost_currency TEXT NOT NULL CHECK (length(trim(cost_currency)) > 0),
    cost_minor BIGINT NOT NULL CHECK (cost_minor > 0),
    cost_balance_after BIGINT NOT NULL,
    reward_balance_after BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(user_id,client_request_id)
);
CREATE INDEX lucky_spin_records_user_created_idx ON lucky_spin_records(user_id,created_at DESC);

WITH spin AS (
    INSERT INTO spin_configs(id,code,title,enabled,cost_currency,cost_minor)
    VALUES(gen_random_uuid(),'lucky-spin','幸运大转盘',true,'STAMINA',10)
    RETURNING id
)
INSERT INTO spin_prizes(id,spin_id,code,label,reward_currency,reward_minor,weight,sort_order)
SELECT gen_random_uuid(),spin.id,v.code,v.label,v.currency,v.amount,v.weight,v.position
FROM spin CROSS JOIN (VALUES
    ('points-8','8 宝石','POINTS',8::bigint,30::bigint,1),
    ('points-28','28 宝石','POINTS',28::bigint,25::bigint,2),
    ('points-68','68 宝石','POINTS',68::bigint,20::bigint,3),
    ('usdt-1','1 USDT','USDT',1::bigint,15::bigint,4),
    ('usdt-6','6 USDT','USDT',6::bigint,8::bigint,5),
    ('usdt-18','18 USDT','USDT',18::bigint,2::bigint,6)
) AS v(code,label,currency,amount,weight,position);
