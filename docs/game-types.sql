-- 游戏房间配置参考 SQL
-- 每种「玩法 + 倍率」= game_types 表一行记录，前端按 code 前缀分组为房间。
-- 倍率说明：payout_multiplier 为整数，194 表示 1.94 倍（含本金）。

-- ============================================================
-- 哈希游戏（TronHash）— 数据源：TRON 区块哈希
-- ============================================================

-- 1.94 倍房间：哈希5 竞猜尾数
INSERT INTO game_types (id, code, name, enabled, rules) VALUES (
    gen_random_uuid(),
    'tronhash_hash5_guess_194',
    '哈希5-竞猜-1.94倍',
    true,
    '{
        "outcomes": ["0","1","2","3","4","5","6","7","8","9"],
        "payout_multiplier": 194,
        "source": "tron_hash",
        "extras": {"base_block_height": 84687805, "block_interval": 5}
    }'::jsonb
);

-- 1.94 倍房间：哈希5 大小单双
INSERT INTO game_types (id, code, name, enabled, rules) VALUES (
    gen_random_uuid(),
    'tronhash_hash5_bigsmall_194',
    '哈希5-大小单双-1.94倍',
    true,
    '{
        "outcomes": ["small","big","odd","even"],
        "payout_multiplier": 194,
        "source": "tron_hash",
        "extras": {"base_block_height": 84687805, "block_interval": 5}
    }'::jsonb
);

-- 1.94 倍房间：哈希5 躲避
INSERT INTO game_types (id, code, name, enabled, rules) VALUES (
    gen_random_uuid(),
    'tronhash_hash5_dodge_194',
    '哈希5-躲避-1.94倍',
    true,
    '{
        "outcomes": ["dodge_0","dodge_1","dodge_2","dodge_3","dodge_4","dodge_5","dodge_6","dodge_7","dodge_8","dodge_9"],
        "payout_multiplier": 194,
        "source": "tron_hash",
        "dodge_mode": true,
        "extras": {"base_block_height": 84687805, "block_interval": 5}
    }'::jsonb
);

-- 1.94 倍房间：哈希9 竞猜尾数
INSERT INTO game_types (id, code, name, enabled, rules) VALUES (
    gen_random_uuid(),
    'tronhash_hash9_guess_194',
    '哈希9-竞猜-1.94倍',
    true,
    '{
        "outcomes": ["0","1","2","3","4","5","6","7","8","9"],
        "payout_multiplier": 194,
        "source": "tron_hash",
        "extras": {"base_block_height": 84687805, "block_interval": 9}
    }'::jsonb
);

-- 1.94 倍房间：哈希9 大小单双
INSERT INTO game_types (id, code, name, enabled, rules) VALUES (
    gen_random_uuid(),
    'tronhash_hash9_bigsmall_194',
    '哈希9-大小单双-1.94倍',
    true,
    '{
        "outcomes": ["small","big","odd","even"],
        "payout_multiplier": 194,
        "source": "tron_hash",
        "extras": {"base_block_height": 84687805, "block_interval": 9}
    }'::jsonb
);

-- 1.94 倍房间：哈希9 躲避
INSERT INTO game_types (id, code, name, enabled, rules) VALUES (
    gen_random_uuid(),
    'tronhash_hash9_dodge_194',
    '哈希9-躲避-1.94倍',
    true,
    '{
        "outcomes": ["dodge_0","dodge_1","dodge_2","dodge_3","dodge_4","dodge_5","dodge_6","dodge_7","dodge_8","dodge_9"],
        "payout_multiplier": 194,
        "source": "tron_hash",
        "dodge_mode": true,
        "extras": {"base_block_height": 84687805, "block_interval": 9}
    }'::jsonb
);

-- 1.95 倍房间：哈希5 竞猜尾数（倍率不同，其他参数相同）
INSERT INTO game_types (id, code, name, enabled, rules) VALUES (
    gen_random_uuid(),
    'tronhash_hash5_guess_195',
    '哈希5-竞猜-1.95倍',
    true,
    '{
        "outcomes": ["0","1","2","3","4","5","6","7","8","9"],
        "payout_multiplier": 195,
        "source": "tron_hash",
        "extras": {"base_block_height": 84687805, "block_interval": 5}
    }'::jsonb
);

-- 1.95 倍房间：哈希5 大小单双
INSERT INTO game_types (id, code, name, enabled, rules) VALUES (
    gen_random_uuid(),
    'tronhash_hash5_bigsmall_195',
    '哈希5-大小单双-1.95倍',
    true,
    '{
        "outcomes": ["small","big","odd","even"],
        "payout_multiplier": 195,
        "source": "tron_hash",
        "extras": {"base_block_height": 84687805, "block_interval": 5}
    }'::jsonb
);

-- 1.95 倍房间：哈希5 躲避
INSERT INTO game_types (id, code, name, enabled, rules) VALUES (
    gen_random_uuid(),
    'tronhash_hash5_dodge_195',
    '哈希5-躲避-1.95倍',
    true,
    '{
        "outcomes": ["dodge_0","dodge_1","dodge_2","dodge_3","dodge_4","dodge_5","dodge_6","dodge_7","dodge_8","dodge_9"],
        "payout_multiplier": 195,
        "source": "tron_hash",
        "dodge_mode": true,
        "extras": {"base_block_height": 84687805, "block_interval": 5}
    }'::jsonb
);

-- ============================================================
-- K 线游戏（Kline）— 数据源：OKX 现货 1 分钟 K 线
-- ============================================================

-- 1.94 倍房间：BTC 奇偶
INSERT INTO game_types (id, code, name, enabled, rules) VALUES (
    gen_random_uuid(),
    'kline_btc_oddeven_194',
    'BTC K线-奇偶-1.94倍',
    true,
    '{
        "outcomes": ["odd","even"],
        "payout_multiplier": 194,
        "source": "okx_kline",
        "extras": {"symbol": "BTC-USDT"}
    }'::jsonb
);

-- 1.94 倍房间：ETH 奇偶
INSERT INTO game_types (id, code, name, enabled, rules) VALUES (
    gen_random_uuid(),
    'kline_eth_oddeven_194',
    'ETH K线-奇偶-1.94倍',
    true,
    '{
        "outcomes": ["odd","even"],
        "payout_multiplier": 194,
        "source": "okx_kline",
        "extras": {"symbol": "ETH-USDT"}
    }'::jsonb
);

-- 1.95 倍房间：BTC 奇偶
INSERT INTO game_types (id, code, name, enabled, rules) VALUES (
    gen_random_uuid(),
    'kline_btc_oddeven_195',
    'BTC K线-奇偶-1.95倍',
    true,
    '{
        "outcomes": ["odd","even"],
        "payout_multiplier": 195,
        "source": "okx_kline",
        "extras": {"symbol": "BTC-USDT"}
    }'::jsonb
);

-- 1.95 倍房间：ETH 奇偶
INSERT INTO game_types (id, code, name, enabled, rules) VALUES (
    gen_random_uuid(),
    'kline_eth_oddeven_195',
    'ETH K线-奇偶-1.95倍',
    true,
    '{
        "outcomes": ["odd","even"],
        "payout_multiplier": 195,
        "source": "okx_kline",
        "extras": {"symbol": "ETH-USDT"}
    }'::jsonb
);
