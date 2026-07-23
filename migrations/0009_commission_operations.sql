ALTER TABLE commission_entries
    ADD COLUMN IF NOT EXISTS currency TEXT;

UPDATE commission_entries AS commission
SET currency = wallets.currency
FROM bets
JOIN wallets ON wallets.id = bets.wallet_id
WHERE commission.source_bet_id = bets.id
  AND commission.currency IS NULL;

ALTER TABLE commission_entries
    ALTER COLUMN currency SET NOT NULL;

CREATE INDEX IF NOT EXISTS commission_entries_beneficiary_status_idx
    ON commission_entries(beneficiary_user_id, status);
