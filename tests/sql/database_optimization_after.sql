-- Run after 0044..0048 in the same disposable database as the before fixture.
DO $$ BEGIN
  IF current_database() NOT LIKE 'block_beast_migration_test_%' THEN
    RAISE EXCEPTION 'requires a disposable block_beast_migration_test_* database';
  END IF;
  IF (SELECT count(*) FROM ledger_entries WHERE wallet_id IN(SELECT id FROM wallets WHERE user_id='a0000000-0000-4000-8000-000000000001')) <> 4 THEN
    RAISE EXCEPTION 'legacy ledger count changed';
  END IF;
  IF NOT EXISTS(SELECT 1 FROM ledger_entries WHERE id='a0000000-0000-4000-8000-000000000022' AND amount_minor=-1000 AND available_delta_minor=-1000 AND frozen_delta_minor=1000 AND balance_after_minor=3000 AND frozen_after_minor IS NULL AND remark='旧冻结' AND occurred_at='2026-08-01T00:00:01Z') THEN
    RAISE EXCEPTION 'legacy freeze not preserved';
  END IF;
  IF NOT EXISTS(SELECT 1 FROM wallets WHERE id='a0000000-0000-4000-8000-000000000011' AND available_minor=3000 AND frozen_minor=1000) THEN
    RAISE EXCEPTION 'wallet balance changed by migration';
  END IF;
  IF EXISTS(SELECT 1 FROM outbox_events WHERE event_type='wallet.ledger.committed') THEN
    RAISE EXCEPTION 'migration replayed historical wallet events';
  END IF;
  IF to_regclass('points_ledger') IS NOT NULL OR to_regclass('stamina_ledger') IS NOT NULL OR to_regclass('checkin_records') IS NOT NULL THEN
    RAISE EXCEPTION 'legacy tables still exist';
  END IF;
  IF NOT EXISTS(SELECT 1 FROM withdrawals WHERE id='a0000000-0000-4000-8000-000000000031' AND platform_decimals IS NULL) THEN
    RAISE EXCEPTION 'unknown historical withdrawal unit was invented';
  END IF;
END $$;

-- Constraints reject changes that would reinterpret historical balances.
DO $$ BEGIN
  BEGIN
    UPDATE wallets SET currency='JADE' WHERE id='a0000000-0000-4000-8000-000000000011';
    RAISE EXCEPTION 'wallet currency changed';
  EXCEPTION WHEN check_violation THEN NULL; END;
  BEGIN
    UPDATE currencies SET decimals=2 WHERE code='POINTS';
    RAISE EXCEPTION 'platform precision changed';
  EXCEPTION WHEN check_violation THEN NULL; END;
  INSERT INTO leaderboard_reward_rules(period_type,currency,rank_from,rank_to,reward_currency,reward_minor)
  VALUES('daily','JADE',1,5,'STAMINA',1);
  BEGIN
    INSERT INTO leaderboard_reward_rules(period_type,currency,rank_from,rank_to,reward_currency,reward_minor)
    VALUES('daily','JADE',5,10,'POINTS',1);
    RAISE EXCEPTION 'overlapping reward accepted';
  EXCEPTION WHEN exclusion_violation THEN NULL; END;
END $$;
