CREATE TABLE lucky_spin_records (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    client_request_id TEXT NOT NULL,
    prize_id TEXT NOT NULL,
    prize_label TEXT NOT NULL,
    reward_currency TEXT NOT NULL CHECK (reward_currency IN ('POINTS', 'USDT', 'STAMINA')),
    reward_minor BIGINT NOT NULL CHECK (reward_minor > 0),
    stamina_cost BIGINT NOT NULL CHECK (stamina_cost > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, client_request_id)
);

CREATE INDEX lucky_spin_records_user_created_idx
    ON lucky_spin_records(user_id, created_at DESC);

INSERT INTO platform_configs(key,value,visibility,version)
VALUES (
    'activity.center',
    '{
      "enabled": true,
      "items": [
        {"id":"lucky-spin","title":"幸运大转盘","description":"消耗体力参与幸运抽奖","enabled":true,"stamina_cost":10},
        {"id":"daily-mission","title":"活动任务","description":"完成任务获得抽奖体力","enabled":true,"stamina_cost":1}
      ],
      "spin_prizes": [
        {"id":"points-8","label":"8 宝石","currency":"POINTS","amount_minor":8,"weight":30},
        {"id":"points-28","label":"28 宝石","currency":"POINTS","amount_minor":28,"weight":25},
        {"id":"points-68","label":"68 宝石","currency":"POINTS","amount_minor":68,"weight":20},
        {"id":"usdt-1","label":"1 USDT","currency":"USDT","amount_minor":1,"weight":15},
        {"id":"usdt-6","label":"6 USDT","currency":"USDT","amount_minor":6,"weight":8},
        {"id":"usdt-18","label":"18 USDT","currency":"USDT","amount_minor":18,"weight":2}
      ]
    }'::jsonb,
    'public',
    1
)
ON CONFLICT (key) DO UPDATE
SET value = jsonb_set(
        platform_configs.value,
        '{spin_prizes}',
        EXCLUDED.value->'spin_prizes'
    ),
    visibility = 'public',
    version = platform_configs.version + 1,
    updated_at = now()
WHERE NOT (platform_configs.value ? 'spin_prizes');
