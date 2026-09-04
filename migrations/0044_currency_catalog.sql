-- Canonical platform units are independent of provider/network units.
-- No balances are rescaled or erased by this migration.
CREATE TABLE currencies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code TEXT NOT NULL UNIQUE CHECK (code ~ '^[A-Z][A-Z0-9_]{0,63}$'),
    name TEXT NOT NULL CHECK (btrim(name) <> ''),
    decimals SMALLINT NOT NULL CHECK (decimals BETWEEN 0 AND 18),
    category TEXT NOT NULL CHECK (category IN ('token','points','stamina','custom')),
    enabled BOOLEAN NOT NULL DEFAULT true,
    create_on_registration BOOLEAN NOT NULL DEFAULT false,
    sort_order INTEGER NOT NULL DEFAULT 0,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO currencies(code,name,decimals,category,create_on_registration,sort_order) VALUES
('USDT','USDT',6,'token',true,10),
('POINTS','宝石',3,'points',true,20),
('JADE','玉石',3,'points',true,30),
('ORIGIN_STONE','源石',3,'points',true,40),
('STAMINA','宝石体力',0,'stamina',true,50),
('USDT_STAMINA','USDT体力',0,'stamina',true,60),
('JADE_STAMINA','玉石体力',0,'stamina',true,70),
('ORIGIN_STONE_STAMINA','源石体力',0,'stamina',true,80);

-- Foreign keys are installed separately in 0047. If an operator-defined code
-- already exists, register its explicitly confirmed precision after this
-- migration and before 0047; never infer the unit from existing balances.

-- Changing an existing unit would reinterpret every historical amount. A new
-- unit must be introduced as a new currency instead; metadata remains editable.
CREATE FUNCTION protect_currency_units() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.code IS DISTINCT FROM OLD.code OR NEW.decimals IS DISTINCT FROM OLD.decimals THEN
        RAISE EXCEPTION 'currency code and decimals are immutable' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER currencies_protect_units BEFORE UPDATE ON currencies
    FOR EACH ROW EXECUTE FUNCTION protect_currency_units();

CREATE INDEX wallets_currency_user_idx ON wallets(currency,user_id);
CREATE INDEX bets_user_created_id_idx ON bets(user_id,created_at DESC,id DESC);
CREATE INDEX bets_status_created_id_idx ON bets(status,created_at DESC,id DESC);
CREATE INDEX ledger_entries_time_id_idx ON ledger_entries(occurred_at DESC,id DESC);
CREATE INDEX audit_logs_actor_time_idx ON audit_logs(actor_user_id,created_at DESC,id DESC);
CREATE INDEX audit_logs_action_time_idx ON audit_logs(action,created_at DESC,id DESC);
CREATE INDEX deposits_address_time_idx ON deposits(chain_address_id,confirmed_at DESC,id DESC);
CREATE INDEX withdrawals_wallet_idx ON withdrawals(wallet_id);
CREATE INDEX auth_identities_user_idx ON auth_identities(user_id);
CREATE INDEX task_reward_config_idx ON bet_task_reward_records(config_id);
