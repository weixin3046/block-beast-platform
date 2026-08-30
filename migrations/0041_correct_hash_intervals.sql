-- 项目尚未上线，本迁移直接重置哈希玩法数据：固定六个赔率房间共享
-- 9/13/17/19/23/29 六套 TRON 目标区块轮次。
DELETE FROM commission_entries;
DELETE FROM bets;
UPDATE chat_rooms SET game_type_id = NULL WHERE game_type_id IS NOT NULL;
DELETE FROM rounds;
DELETE FROM game_room_types;
DELETE FROM hash_room_currency_configs;
DELETE FROM game_types;
DELETE FROM game_rooms;

-- 参考项目的主结算强制关闭 waterRate，后台页面也只配置倍率和上限；
-- 删除此前误暴露但不会参与结算的返水字段，避免运营产生错误预期。
ALTER TABLE hash_room_currency_configs
    DROP COLUMN water_multiplier,
    DROP COLUMN water_divisor;

INSERT INTO game_rooms(id, code, name, game_kind, enabled, sort_order)
VALUES
    ('94000000-0000-4000-8000-000000000001', 'hash_rate_1940', '高额返水 1.94 倍',  'hash', true, 10),
    ('95000000-0000-4000-8000-000000000001', 'hash_rate_1950', '高额返水 1.95 倍',  'hash', true, 20),
    ('96000000-0000-4000-8000-000000000001', 'hash_rate_1960', '通用倍率 1.96 倍',  'hash', true, 30),
    ('97000000-0000-4000-8000-000000000001', 'hash_rate_1970', '通用倍率 1.97 倍',  'hash', true, 40),
    ('98000000-0000-4000-8000-000000000001', 'hash_rate_1980', '高额赔率 1.98 倍',  'hash', true, 50),
    ('98500000-0000-4000-8000-000000000001', 'hash_rate_1985', '高额赔率 1.985 倍', 'hash', true, 60);

INSERT INTO game_types(id, room_id, code, name, mode, block_interval, close_before_seconds, enabled, rules)
VALUES
    ('09000000-0000-4000-8000-000000000001', NULL, 'hash_9',  '9区块',  'hash', 9,  5, true, '{"outcomes":["0","1","2","3","4","5","6","7","8","9","small","big","odd","even"],"payout_multiplier":1,"source":"tron_hash","extras":{"block_interval":9,"hash_shared":true}}'),
    ('13000000-0000-4000-8000-000000000001', NULL, 'hash_13', '13区块', 'hash', 13, 5, true, '{"outcomes":["0","1","2","3","4","5","6","7","8","9","small","big","odd","even"],"payout_multiplier":1,"source":"tron_hash","extras":{"block_interval":13,"hash_shared":true}}'),
    ('17000000-0000-4000-8000-000000000001', NULL, 'hash_17', '17区块', 'hash', 17, 5, true, '{"outcomes":["0","1","2","3","4","5","6","7","8","9","small","big","odd","even"],"payout_multiplier":1,"source":"tron_hash","extras":{"block_interval":17,"hash_shared":true}}'),
    ('19000000-0000-4000-8000-000000000001', NULL, 'hash_19', '19区块', 'hash', 19, 5, true, '{"outcomes":["0","1","2","3","4","5","6","7","8","9","small","big","odd","even"],"payout_multiplier":1,"source":"tron_hash","extras":{"block_interval":19,"hash_shared":true}}'),
    ('23000000-0000-4000-8000-000000000001', NULL, 'hash_23', '23区块', 'hash', 23, 5, true, '{"outcomes":["0","1","2","3","4","5","6","7","8","9","small","big","odd","even"],"payout_multiplier":1,"source":"tron_hash","extras":{"block_interval":23,"hash_shared":true}}'),
    ('29000000-0000-4000-8000-000000000001', NULL, 'hash_29', '29区块', 'hash', 29, 5, true, '{"outcomes":["0","1","2","3","4","5","6","7","8","9","small","big","odd","even"],"payout_multiplier":1,"source":"tron_hash","extras":{"block_interval":29,"hash_shared":true}}');

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
        ('09000000-0000-4000-8000-000000000001'::uuid, 10),
        ('13000000-0000-4000-8000-000000000001'::uuid, 20),
        ('17000000-0000-4000-8000-000000000001'::uuid, 30),
        ('19000000-0000-4000-8000-000000000001'::uuid, 40),
        ('23000000-0000-4000-8000-000000000001'::uuid, 50),
        ('29000000-0000-4000-8000-000000000001'::uuid, 60)
) AS game_type(id, sort_order);

-- API 与钱包统一使用 minor unit。USDT 按 6 位精度换算参考页面上的
-- 0.1 最低投注与各档上限；平台内部币种保持整数单位。
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
), currencies(currency, high_limit, scale, minimum) AS (
    VALUES
        ('POINTS', true, 1::bigint, 1::bigint),
        ('ORIGIN_STONE', true, 1::bigint, 1::bigint),
        ('USDT', false, 1000000::bigint, 100000::bigint),
        ('JADE', false, 1::bigint, 1::bigint)
)
INSERT INTO hash_room_currency_configs(
    room_id,currency,guess_multiplier,dodge_multiplier,road_multiplier,
    guess_max_stake_minor,dodge_max_stake_minor,road_max_stake_minor,min_stake_minor
)
SELECT p.room_id,c.currency,p.guess_rate,p.dodge_rate,p.road_rate,
       (CASE WHEN c.high_limit THEN p.high_guess ELSE p.low_guess END) * c.scale,
       (CASE WHEN c.high_limit THEN p.high_dodge ELSE p.low_dodge END) * c.scale,
       (CASE WHEN c.high_limit THEN p.high_road ELSE p.low_road END) * c.scale,
       c.minimum
FROM presets p CROSS JOIN currencies c;

UPDATE hash_game_settings SET version=version+1, updated_at=now() WHERE singleton=true;
