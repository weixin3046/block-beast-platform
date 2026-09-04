-- Stable natural keys avoid maintaining two writable currency identifiers.
ALTER TABLE wallets ADD CONSTRAINT wallets_currency_fk FOREIGN KEY(currency) REFERENCES currencies(code);
ALTER TABLE bet_task_configs
    ADD CONSTRAINT task_accumulation_currency_fk FOREIGN KEY(accumulation_currency) REFERENCES currencies(code),
    ADD CONSTRAINT task_reward_currency_fk FOREIGN KEY(reward_currency) REFERENCES currencies(code);
ALTER TABLE spin_configs ADD CONSTRAINT spin_cost_currency_fk FOREIGN KEY(cost_currency) REFERENCES currencies(code);
ALTER TABLE spin_prizes ADD CONSTRAINT spin_reward_currency_fk FOREIGN KEY(reward_currency) REFERENCES currencies(code);
ALTER TABLE hash_room_currency_configs ADD CONSTRAINT hash_currency_fk FOREIGN KEY(currency) REFERENCES currencies(code);
ALTER TABLE virtual_account_automations ADD CONSTRAINT automation_currency_fk FOREIGN KEY(currency) REFERENCES currencies(code);
ALTER TABLE leaderboard_reward_rules
    ADD CONSTRAINT leaderboard_currency_fk FOREIGN KEY(currency) REFERENCES currencies(code),
    ADD CONSTRAINT leaderboard_reward_currency_fk FOREIGN KEY(reward_currency) REFERENCES currencies(code);
ALTER TABLE leaderboard_reward_rule_versions ADD CONSTRAINT leaderboard_version_currency_fk FOREIGN KEY(currency) REFERENCES currencies(code);

CREATE INDEX ledger_entries_wallet_time_id_idx ON ledger_entries(wallet_id,occurred_at DESC,id DESC);

CREATE FUNCTION protect_wallet_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.user_id IS DISTINCT FROM OLD.user_id OR NEW.currency IS DISTINCT FROM OLD.currency THEN
        RAISE EXCEPTION 'wallet owner and currency are immutable' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER wallets_protect_identity BEFORE UPDATE ON wallets
    FOR EACH ROW EXECUTE FUNCTION protect_wallet_identity();
