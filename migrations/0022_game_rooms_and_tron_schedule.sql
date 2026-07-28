CREATE TABLE game_rooms (
    id UUID PRIMARY KEY,
    code TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    payout_multiplier BIGINT NOT NULL CHECK (payout_multiplier > 0),
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE game_types
    ADD COLUMN room_id UUID REFERENCES game_rooms(id) ON DELETE SET NULL,
    ADD COLUMN mode TEXT,
    ADD COLUMN block_interval INTEGER;

-- 历史哈希玩法文档一直使用 194 表示 1.94 倍；补上除数，修正旧结算按
-- 194 倍派奖的问题。小于 100 的传统整数倍率玩法保持原语义。
UPDATE game_types
SET rules = jsonb_set(rules, '{payout_divisor}', '100'::jsonb)
WHERE (rules->>'payout_multiplier')::bigint >= 100
  AND NOT rules ? 'payout_divisor';

ALTER TABLE game_types
    ADD CONSTRAINT game_types_block_interval_positive
    CHECK (block_interval IS NULL OR block_interval > 0);

CREATE INDEX game_types_room_idx
    ON game_types (room_id, enabled, block_interval, mode);

DROP TRIGGER IF EXISTS rounds_set_result_at ON rounds;
DROP FUNCTION IF EXISTS set_round_result_at();

CREATE FUNCTION set_round_result_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.result_at := NEW.bet_closes_at + interval '3 seconds';
    RETURN NEW;
END;
$$;

CREATE TRIGGER rounds_set_result_at
BEFORE INSERT OR UPDATE OF bet_closes_at, result_at ON rounds
FOR EACH ROW EXECUTE FUNCTION set_round_result_at();

UPDATE rounds
SET result_at = bet_closes_at + interval '3 seconds'
WHERE status IN ('open', 'closed');
