-- Stop API and lulu-worker before applying: old binaries target LULU wallets.
-- Never erase funded wallets, financial history, or active payment obligations.
DO $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM currencies WHERE code='ORIGIN_STONE' AND decimals=3) THEN
  RAISE EXCEPTION 'ORIGIN_STONE with decimals=3 is required';
 END IF;
 IF EXISTS(SELECT 1 FROM lulu_orders WHERE status IN ('requested','approved','sending','unknown'))
 OR EXISTS(SELECT 1 FROM wallets WHERE currency='LULU' AND (available_minor<>0 OR frozen_minor<>0))
 OR EXISTS(SELECT 1 FROM ledger_entries l JOIN wallets w ON w.id=l.wallet_id WHERE w.currency='LULU') THEN
  RAISE EXCEPTION 'Resolve Lulu obligations and reconcile funded or historical LULU wallets before migration 0074';
 END IF;
END $$;
UPDATE lulu_config SET enabled=false,version=version+1,updated_at=now() WHERE singleton;
DELETE FROM wallets WHERE currency='LULU';
DELETE FROM currencies WHERE code='LULU';
-- Existing ORIGIN_STONE wallets, precision and balances remain unchanged.
