-- 投注任务配置：当日投注积分累计达到 threshold_minor 时奖励 reward_minor 体力。
-- 多档配置可叠加（每档每日只能领一次），运营直接 INSERT/UPDATE 即可调整。
CREATE TABLE bet_task_configs (
    id UUID PRIMARY KEY,
    threshold_minor BIGINT NOT NULL CHECK (threshold_minor > 0),
    reward_minor BIGINT NOT NULL CHECK (reward_minor > 0),
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (threshold_minor)
);

-- 用户每日投注积分累计进度，按 (user_id, bet_date) 聚合。
CREATE TABLE user_daily_bet_progress (
    user_id UUID NOT NULL REFERENCES users(id),
    bet_date DATE NOT NULL,
    total_stake_minor BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, bet_date)
);

-- 已发放的投注任务奖励记录，UNIQUE(user_id, bet_date, config_id) 保证幂等。
CREATE TABLE bet_task_reward_records (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    bet_date DATE NOT NULL,
    config_id UUID NOT NULL REFERENCES bet_task_configs(id),
    reward_minor BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, bet_date, config_id)
);

-- 默认档位示例，可按需调整。
INSERT INTO bet_task_configs (id, threshold_minor, reward_minor) VALUES
    (gen_random_uuid(), 10000, 10),
    (gen_random_uuid(), 50000, 60),
    (gen_random_uuid(), 200000, 300);
