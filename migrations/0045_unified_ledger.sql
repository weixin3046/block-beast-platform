-- Preserve original IDs, amounts, timestamps and operator remarks. No balances
-- are recomputed from incomplete historical ledgers.
-- Refuse to drop a legacy ledger if its wallet association is missing.
DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM points_ledger p WHERE NOT EXISTS(SELECT 1 FROM wallets w WHERE w.user_id=p.user_id AND w.currency='POINTS'))
       OR EXISTS(SELECT 1 FROM stamina_ledger p WHERE NOT EXISTS(SELECT 1 FROM wallets w WHERE w.user_id=p.user_id AND w.currency='STAMINA')) THEN
        RAISE EXCEPTION 'legacy ledger has missing wallet; repair associations before migration';
    END IF;
END $$;
ALTER TABLE ledger_entries
    ADD COLUMN remark TEXT NOT NULL DEFAULT '',
    ADD COLUMN operator_id UUID REFERENCES users(id),
    ADD COLUMN available_delta_minor BIGINT,
    ADD COLUMN frozen_delta_minor BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN frozen_after_minor BIGINT CHECK (frozen_after_minor >= 0);

INSERT INTO ledger_entries(id,wallet_id,business_type,business_id,entry_type,amount_minor,balance_after_minor,remark,operator_id,occurred_at)
SELECT p.id,w.id,p.business_type,p.business_id,p.business_type,p.amount_minor,p.balance_after_minor,p.remark,p.operator_id,p.occurred_at
FROM points_ledger p JOIN wallets w ON w.user_id=p.user_id AND w.currency='POINTS';
INSERT INTO ledger_entries(id,wallet_id,business_type,business_id,entry_type,amount_minor,balance_after_minor,remark,operator_id,occurred_at)
SELECT p.id,w.id,p.business_type,p.business_id,p.business_type,p.amount_minor,p.balance_after_minor,p.remark,p.operator_id,p.occurred_at
FROM stamina_ledger p JOIN wallets w ON w.user_id=p.user_id AND w.currency='STAMINA';

UPDATE ledger_entries SET
    available_delta_minor=CASE WHEN entry_type IN ('withdrawal_debit','point_withdrawal_debit') THEN 0 ELSE amount_minor END,
    frozen_delta_minor=CASE
        WHEN entry_type IN ('withdrawal_freeze','point_withdrawal') THEN -amount_minor
        WHEN entry_type IN ('withdrawal_debit','point_withdrawal_debit') THEN amount_minor
        WHEN entry_type IN ('withdrawal_unfreeze','point_withdrawal_unfreeze') THEN -amount_minor
        ELSE 0 END;
ALTER TABLE ledger_entries ALTER COLUMN available_delta_minor SET NOT NULL;

-- Historical frozen snapshots cannot be reconstructed reliably and stay NULL.
-- All new ledger entries snapshot the wallet under its existing row lock.
CREATE FUNCTION fill_ledger_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.available_delta_minor := COALESCE(NEW.available_delta_minor,NEW.amount_minor);
    SELECT frozen_minor INTO STRICT NEW.frozen_after_minor FROM wallets WHERE id=NEW.wallet_id;
    RETURN NEW;
END $$;
CREATE TRIGGER ledger_snapshot BEFORE INSERT ON ledger_entries
FOR EACH ROW EXECUTE FUNCTION fill_ledger_snapshot();

CREATE FUNCTION enqueue_ledger_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE account_id UUID; currency_code TEXT;
BEGIN
    SELECT user_id,currency INTO STRICT account_id,currency_code FROM wallets WHERE id=NEW.wallet_id;
    INSERT INTO outbox_events(id,aggregate_type,aggregate_id,event_type,payload)
    VALUES(gen_random_uuid(),'ledger',NEW.id::text,'wallet.ledger.committed',jsonb_build_object(
        'user_id',account_id,'currency',currency_code,'ledger_id',NEW.id,
        'business_id',NEW.business_id,'business_type',NEW.business_type,
        'available_delta_minor',NEW.available_delta_minor,'frozen_delta_minor',NEW.frozen_delta_minor,
        'available_after_minor',NEW.balance_after_minor,'frozen_after_minor',NEW.frozen_after_minor));
    RETURN NEW;
END $$;
CREATE TRIGGER ledger_outbox AFTER INSERT ON ledger_entries
FOR EACH ROW EXECUTE FUNCTION enqueue_ledger_event();

DROP TABLE points_ledger;
DROP TABLE stamina_ledger;
DROP TABLE checkin_records;
CREATE INDEX ledger_entries_operator_time_idx ON ledger_entries(operator_id,occurred_at DESC,id DESC);
