-- 哈希轮次由 Worker 根据 TronGrid 最新区块的实际时间戳写入 result_at。
-- 兼容未显式提供 result_at 的旧写入路径。
CREATE OR REPLACE FUNCTION set_round_result_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.result_at IS NULL THEN
        NEW.result_at := NEW.bet_closes_at + interval '3 seconds';
    END IF;
    RETURN NEW;
END;
$$;
