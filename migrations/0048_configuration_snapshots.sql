CREATE EXTENSION IF NOT EXISTS btree_gist;
ALTER TABLE leaderboard_reward_rules ADD CONSTRAINT leaderboard_reward_no_overlap
    EXCLUDE USING gist(period_type WITH =,currency WITH =,int8range(rank_from::bigint,rank_to::bigint,'[]') WITH &&)
    WHERE (enabled);

ALTER TABLE bet_task_configs ADD COLUMN version BIGINT NOT NULL DEFAULT 1 CHECK (version>0);
ALTER TABLE spin_configs ADD COLUMN version BIGINT NOT NULL DEFAULT 1 CHECK (version>0);
CREATE FUNCTION bump_activity_config_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.version:=OLD.version+1;
    RETURN NEW;
END $$;
CREATE TRIGGER task_config_version BEFORE UPDATE ON bet_task_configs FOR EACH ROW EXECUTE FUNCTION bump_activity_config_version();
CREATE TRIGGER spin_config_version BEFORE UPDATE ON spin_configs FOR EACH ROW EXECUTE FUNCTION bump_activity_config_version();

-- Old records retain NULL; current configuration is not historical evidence.
ALTER TABLE bet_task_reward_records ADD COLUMN config_snapshot JSONB;
ALTER TABLE lucky_spin_records ADD COLUMN config_snapshot JSONB;
ALTER TABLE leaderboard_reward_distributions ADD COLUMN rule_snapshot JSONB;
CREATE FUNCTION snapshot_task_reward_config() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    SELECT to_jsonb(c) INTO NEW.config_snapshot FROM bet_task_configs c WHERE c.id=NEW.config_id;
    RETURN NEW;
END $$;
CREATE TRIGGER task_reward_snapshot BEFORE INSERT ON bet_task_reward_records FOR EACH ROW EXECUTE FUNCTION snapshot_task_reward_config();
CREATE FUNCTION snapshot_spin_config() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    SELECT to_jsonb(c)||jsonb_build_object('prizes',
        (SELECT jsonb_agg(to_jsonb(p) ORDER BY p.sort_order,p.id) FROM spin_prizes p WHERE p.spin_id=c.id))
      INTO NEW.config_snapshot FROM spin_configs c WHERE c.id=NEW.spin_id;
    RETURN NEW;
END $$;
CREATE TRIGGER spin_result_snapshot BEFORE INSERT ON lucky_spin_records FOR EACH ROW EXECUTE FUNCTION snapshot_spin_config();
-- Leaderboard payouts carry the snapshot from the same SELECT that chose the
-- award; reading the mutable rule again at INSERT time would be incorrect.

-- Local PostgreSQL initdb executes every SQL file in order without migrate.sh.
-- Record that completed baseline too, so a subsequent migrate.sh cannot replay
-- old destructive seed migrations. Apply this file only in the normal sequence.
CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO schema_migrations(version)
SELECT lpad(n::text,4,'0') FROM generate_series(1,48) n
ON CONFLICT(version) DO NOTHING;
