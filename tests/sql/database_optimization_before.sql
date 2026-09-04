-- Run only in a disposable database after migrations 0001..0043.
DO $$ BEGIN
  IF current_database() NOT LIKE 'block_beast_migration_test_%' THEN
    RAISE EXCEPTION 'requires a disposable block_beast_migration_test_* database';
  END IF;
END $$;
INSERT INTO users(id,display_name) VALUES('a0000000-0000-4000-8000-000000000001','migration test');
INSERT INTO wallets(id,user_id,currency,available_minor,frozen_minor) VALUES
('a0000000-0000-4000-8000-000000000011','a0000000-0000-4000-8000-000000000001','POINTS',3000,1000),
('a0000000-0000-4000-8000-000000000012','a0000000-0000-4000-8000-000000000001','STAMINA',7,0),
('a0000000-0000-4000-8000-000000000013','a0000000-0000-4000-8000-000000000001','USDT',100,0);
INSERT INTO points_ledger(id,user_id,business_type,business_id,amount_minor,balance_after_minor,remark,occurred_at) VALUES
('a0000000-0000-4000-8000-000000000021','a0000000-0000-4000-8000-000000000001','admin_credit','legacy-credit',4000,4000,'旧上分','2026-08-01T00:00:00Z'),
('a0000000-0000-4000-8000-000000000022','a0000000-0000-4000-8000-000000000001','point_withdrawal','legacy-freeze',-1000,3000,'旧冻结','2026-08-01T00:00:01Z');
INSERT INTO stamina_ledger(id,user_id,business_type,business_id,amount_minor,balance_after_minor,occurred_at) VALUES
('a0000000-0000-4000-8000-000000000023','a0000000-0000-4000-8000-000000000001','bet_task_reward','legacy-reward',7,7,'2026-08-01T00:00:02Z');
INSERT INTO ledger_entries(id,wallet_id,business_type,business_id,entry_type,amount_minor,balance_after_minor) VALUES
('a0000000-0000-4000-8000-000000000024','a0000000-0000-4000-8000-000000000013','deposit','legacy-deposit','deposit_credit',100,100);
INSERT INTO withdrawals(id,user_id,wallet_id,client_request_id,destination_address,amount_minor,status) VALUES
('a0000000-0000-4000-8000-000000000031','a0000000-0000-4000-8000-000000000001','a0000000-0000-4000-8000-000000000013','legacy-withdraw','test-only',1,'confirmed');
