ALTER TABLE rounds
    ADD COLUMN result_at TIMESTAMPTZ;

UPDATE rounds SET result_at = bet_closes_at + interval '5 seconds';

ALTER TABLE rounds ALTER COLUMN result_at SET NOT NULL;

CREATE FUNCTION set_round_result_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.result_at := NEW.bet_closes_at + interval '5 seconds';
    RETURN NEW;
END;
$$;

CREATE TRIGGER rounds_set_result_at
BEFORE INSERT OR UPDATE OF bet_closes_at, result_at ON rounds
FOR EACH ROW EXECUTE FUNCTION set_round_result_at();

CREATE INDEX rounds_due_settlement_idx
    ON rounds (result_at, id)
    WHERE status IN ('closed', 'settling');
