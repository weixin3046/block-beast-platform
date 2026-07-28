ALTER TABLE game_types
    ADD COLUMN close_before_seconds INTEGER
    CHECK (close_before_seconds BETWEEN 1 AND 59);

-- 将已有房间配置迁移到玩法，之后封盘秒数由每个房内玩法独立维护。
UPDATE game_types gt
SET close_before_seconds = gr.close_before_seconds
FROM game_rooms gr
WHERE gr.id = gt.room_id;

UPDATE game_types
SET close_before_seconds = 3
WHERE close_before_seconds IS NULL;

ALTER TABLE game_types
    ALTER COLUMN close_before_seconds SET DEFAULT 3,
    ALTER COLUMN close_before_seconds SET NOT NULL;

CREATE OR REPLACE FUNCTION set_round_result_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    close_seconds INTEGER;
BEGIN
    SELECT COALESCE(gt.close_before_seconds, 3)
    INTO close_seconds
    FROM game_types gt
    WHERE gt.id = NEW.game_type_id;

    NEW.result_at := NEW.bet_closes_at + make_interval(secs => COALESCE(close_seconds, 3));
    RETURN NEW;
END;
$$;

UPDATE rounds r
SET result_at = r.bet_closes_at + make_interval(secs => gt.close_before_seconds)
FROM game_types gt
WHERE r.game_type_id = gt.id
  AND r.status IN ('open', 'closed');
