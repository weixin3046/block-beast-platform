-- 参考哈希游戏使用五套共享区块轮次，六个赔率房间只是投注菜单和参数维度。
-- 项目尚未上线，本迁移直接清理旧哈希玩法数据，不保留旧结构兼容数据。

CREATE TABLE game_room_types (
    room_id UUID NOT NULL REFERENCES game_rooms(id) ON DELETE CASCADE,
    game_type_id UUID NOT NULL REFERENCES game_types(id) ON DELETE CASCADE,
    sort_order INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (room_id, game_type_id)
);

CREATE INDEX game_room_types_type_idx
    ON game_room_types (game_type_id, sort_order, room_id);

ALTER TABLE game_types
    ALTER COLUMN close_before_seconds SET DEFAULT 5;

-- 未显式指定 result_at 的人工轮次按玩法自己的封盘秒数计算；Worker 为哈希
-- 轮次显式传入目标区块预计时间，因此不会被触发器覆盖。
CREATE OR REPLACE FUNCTION set_round_result_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    close_seconds INTEGER;
BEGIN
    IF NEW.result_at IS NULL THEN
        SELECT COALESCE(gt.close_before_seconds, 5)
        INTO close_seconds
        FROM game_types gt
        WHERE gt.id = NEW.game_type_id;
        NEW.result_at := NEW.bet_closes_at + make_interval(secs => COALESCE(close_seconds, 5));
    END IF;
    RETURN NEW;
END;
$$;

CREATE TABLE hash_game_settings (
    singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO hash_game_settings(singleton, version) VALUES (true, 1);

CREATE TABLE hash_room_currency_configs (
    room_id UUID NOT NULL REFERENCES game_rooms(id) ON DELETE CASCADE,
    currency TEXT NOT NULL CHECK (btrim(currency) <> ''),
    guess_multiplier BIGINT NOT NULL CHECK (guess_multiplier > 0),
    guess_divisor BIGINT NOT NULL DEFAULT 1000 CHECK (guess_divisor > 0),
    dodge_multiplier BIGINT NOT NULL CHECK (dodge_multiplier > 0),
    dodge_divisor BIGINT NOT NULL DEFAULT 1000 CHECK (dodge_divisor > 0),
    road_multiplier BIGINT NOT NULL CHECK (road_multiplier > 0),
    road_divisor BIGINT NOT NULL DEFAULT 1000 CHECK (road_divisor > 0),
    guess_max_stake_minor BIGINT NOT NULL CHECK (guess_max_stake_minor > 0),
    dodge_max_stake_minor BIGINT NOT NULL CHECK (dodge_max_stake_minor > 0),
    road_max_stake_minor BIGINT NOT NULL CHECK (road_max_stake_minor > 0),
    min_stake_minor BIGINT NOT NULL CHECK (min_stake_minor > 0),
    water_multiplier BIGINT NOT NULL DEFAULT 50 CHECK (water_multiplier >= 0),
    water_divisor BIGINT NOT NULL DEFAULT 1000 CHECK (water_divisor > 0),
    PRIMARY KEY (room_id, currency)
);

ALTER TABLE bets
    ADD COLUMN game_room_id UUID REFERENCES game_rooms(id),
    ADD COLUMN play_mode TEXT CHECK (play_mode IS NULL OR play_mode IN ('guess', 'dodge', 'road')),
    ADD COLUMN payout_multiplier_snapshot BIGINT CHECK (payout_multiplier_snapshot IS NULL OR payout_multiplier_snapshot > 0),
    ADD COLUMN payout_divisor_snapshot BIGINT CHECK (payout_divisor_snapshot IS NULL OR payout_divisor_snapshot > 0);

CREATE INDEX bets_hash_room_round_user_idx
    ON bets (round_id, user_id, game_room_id, play_mode)
    WHERE game_room_id IS NOT NULL;

-- 项目尚未上线且本期只保留哈希游戏，直接清理旧哈希、K 线和通用游戏数据；
-- 钱包资金账本继续作为不可变历史保留，K 线源码保留但不初始化、不展示。
DELETE FROM commission_entries;
DELETE FROM bets;
UPDATE chat_rooms SET game_type_id = NULL WHERE game_type_id IS NOT NULL;
DELETE FROM rounds;
DELETE FROM game_types;
DELETE FROM game_rooms;

INSERT INTO game_rooms(id, code, name, game_kind, enabled, sort_order)
VALUES
    ('94000000-0000-4000-8000-000000000001', 'hash_rate_1940', '高额返水 1.94 倍',  'hash', true, 10),
    ('95000000-0000-4000-8000-000000000001', 'hash_rate_1950', '高额返水 1.95 倍',  'hash', true, 20),
    ('96000000-0000-4000-8000-000000000001', 'hash_rate_1960', '通用倍率 1.96 倍',  'hash', true, 30),
    ('97000000-0000-4000-8000-000000000001', 'hash_rate_1970', '通用倍率 1.97 倍',  'hash', true, 40),
    ('98000000-0000-4000-8000-000000000001', 'hash_rate_1980', '高额赔率 1.98 倍',  'hash', true, 50),
    ('98500000-0000-4000-8000-000000000001', 'hash_rate_1985', '高额赔率 1.985 倍', 'hash', true, 60);

-- 共享玩法的 outcomes 同时覆盖竞猜数字与上下路；hash_shared 使结果源一次返回
-- 数字、大小和单双，躲避玩法在结算时按赔率快照执行反向判定。
INSERT INTO game_types(id, room_id, code, name, mode, block_interval, close_before_seconds, enabled, rules)
VALUES
    ('05000000-0000-4000-8000-000000000001', NULL, 'hash_5',  '5区块',  'hash', 5,  5, true, '{"outcomes":["0","1","2","3","4","5","6","7","8","9","small","big","odd","even"],"payout_multiplier":1,"source":"tron_hash","extras":{"block_interval":5,"hash_shared":true}}'),
    ('09000000-0000-4000-8000-000000000001', NULL, 'hash_9',  '9区块',  'hash', 9,  5, true, '{"outcomes":["0","1","2","3","4","5","6","7","8","9","small","big","odd","even"],"payout_multiplier":1,"source":"tron_hash","extras":{"block_interval":9,"hash_shared":true}}'),
    ('13000000-0000-4000-8000-000000000001', NULL, 'hash_13', '13区块', 'hash', 13, 5, true, '{"outcomes":["0","1","2","3","4","5","6","7","8","9","small","big","odd","even"],"payout_multiplier":1,"source":"tron_hash","extras":{"block_interval":13,"hash_shared":true}}'),
    ('17000000-0000-4000-8000-000000000001', NULL, 'hash_17', '17区块', 'hash', 17, 5, true, '{"outcomes":["0","1","2","3","4","5","6","7","8","9","small","big","odd","even"],"payout_multiplier":1,"source":"tron_hash","extras":{"block_interval":17,"hash_shared":true}}'),
    ('19000000-0000-4000-8000-000000000001', NULL, 'hash_19', '19区块', 'hash', 19, 5, true, '{"outcomes":["0","1","2","3","4","5","6","7","8","9","small","big","odd","even"],"payout_multiplier":1,"source":"tron_hash","extras":{"block_interval":19,"hash_shared":true}}');

INSERT INTO game_room_types(room_id, game_type_id, sort_order)
SELECT room.id, game_type.id, game_type.sort_order
FROM (
    VALUES
        ('94000000-0000-4000-8000-000000000001'::uuid),
        ('95000000-0000-4000-8000-000000000001'::uuid),
        ('96000000-0000-4000-8000-000000000001'::uuid),
        ('97000000-0000-4000-8000-000000000001'::uuid),
        ('98000000-0000-4000-8000-000000000001'::uuid),
        ('98500000-0000-4000-8000-000000000001'::uuid)
) AS room(id)
CROSS JOIN (
    VALUES
        ('05000000-0000-4000-8000-000000000001'::uuid, 10),
        ('09000000-0000-4000-8000-000000000001'::uuid, 20),
        ('13000000-0000-4000-8000-000000000001'::uuid, 30),
        ('17000000-0000-4000-8000-000000000001'::uuid, 40),
        ('19000000-0000-4000-8000-000000000001'::uuid, 50)
) AS game_type(id, sort_order);

-- 参考项目把 POINTS/ORIGIN_STONE 作为高额组，把 USDT/JADE 作为低额组。
-- 倍率固定使用千分位整数，1.985 表示为 1985/1000。
WITH presets(room_id, guess_rate, dodge_rate, road_rate,
             high_guess, high_dodge, high_road,
             low_guess, low_dodge, low_road) AS (
    VALUES
        ('94000000-0000-4000-8000-000000000001'::uuid, 9350,1080,1940,  500, 1000, 1000,  50, 100,  50),
        ('95000000-0000-4000-8000-000000000001'::uuid, 9400,1082,1950,  500, 2000, 2000,  50, 200, 100),
        ('96000000-0000-4000-8000-000000000001'::uuid, 9450,1084,1960, 1000, 5000, 5000, 100, 500, 200),
        ('97000000-0000-4000-8000-000000000001'::uuid, 9500,1085,1970, 2000,10000,10000, 200,1000, 500),
        ('98000000-0000-4000-8000-000000000001'::uuid, 9500,1085,1980, 2000,20000,20000, 500,2000,1000),
        ('98500000-0000-4000-8000-000000000001'::uuid, 9500,1085,1985, 2000,30000,30000, 500,5000,3000)
), currencies(currency, high_limit, minimum) AS (
    VALUES
        ('POINTS', true, 1::bigint),
        ('ORIGIN_STONE', true, 1::bigint),
        ('USDT', false, 1::bigint),
        ('JADE', false, 1::bigint)
)
INSERT INTO hash_room_currency_configs(
    room_id,currency,guess_multiplier,dodge_multiplier,road_multiplier,
    guess_max_stake_minor,dodge_max_stake_minor,road_max_stake_minor,min_stake_minor
)
SELECT p.room_id,c.currency,p.guess_rate,p.dodge_rate,p.road_rate,
       CASE WHEN c.high_limit THEN p.high_guess ELSE p.low_guess END,
       CASE WHEN c.high_limit THEN p.high_dodge ELSE p.low_dodge END,
       CASE WHEN c.high_limit THEN p.high_road ELSE p.low_road END,
       c.minimum
FROM presets p CROSS JOIN currencies c;
