ALTER TABLE game_rooms
    ADD COLUMN close_before_seconds INTEGER NOT NULL DEFAULT 3
    CHECK (close_before_seconds BETWEEN 1 AND 59);

-- result_at 始终根据玩法所属房间的封盘提前秒数计算。未关联房间的
-- 旧玩法继续使用 3 秒，保证手工创建轮次与自动排期采用同一规则。
CREATE OR REPLACE FUNCTION set_round_result_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    close_seconds INTEGER;
BEGIN
    SELECT COALESCE(gr.close_before_seconds, 3)
    INTO close_seconds
    FROM game_types gt
    LEFT JOIN game_rooms gr ON gr.id = gt.room_id
    WHERE gt.id = NEW.game_type_id;

    NEW.result_at := NEW.bet_closes_at + make_interval(secs => COALESCE(close_seconds, 3));
    RETURN NEW;
END;
$$;

UPDATE rounds r
SET result_at = r.bet_closes_at + make_interval(
    secs => COALESCE(gr.close_before_seconds, 3)
)
FROM game_types gt
LEFT JOIN game_rooms gr ON gr.id = gt.room_id
WHERE r.game_type_id = gt.id
  AND r.status IN ('open', 'closed');
