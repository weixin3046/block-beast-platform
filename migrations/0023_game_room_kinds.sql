ALTER TABLE game_rooms
    ADD COLUMN game_kind TEXT NOT NULL DEFAULT 'hash'
    CHECK (game_kind IN ('hash', 'kline'));

CREATE INDEX game_rooms_kind_sort_idx
    ON game_rooms (game_kind, enabled, sort_order);

-- 为已有的 BTC/ETH K 线玩法建立默认倍率房间；后续可在管理后台增删启停。
INSERT INTO game_rooms(id, code, name, game_kind, enabled, payout_multiplier, sort_order)
VALUES
    ('8f3eb416-31e7-4f08-8f2c-bafbf53cc194', 'kline_194', 'K 线 1.94 倍房', 'kline', true, 194, 100),
    ('8f3eb416-31e7-4f08-8f2c-bafbf53cc195', 'kline_195', 'K 线 1.95 倍房', 'kline', true, 195, 101);

UPDATE game_types
SET room_id = CASE
        WHEN code LIKE 'kline\_%\_195' ESCAPE '\' THEN '8f3eb416-31e7-4f08-8f2c-bafbf53cc195'::uuid
        ELSE '8f3eb416-31e7-4f08-8f2c-bafbf53cc194'::uuid
    END,
    mode = 'guess'
WHERE rules->>'source' = 'okx_kline';
