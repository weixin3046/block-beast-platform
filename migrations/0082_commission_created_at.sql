-- Existing entries must not receive the migration execution time.
ALTER TABLE commission_entries ADD COLUMN created_at TIMESTAMPTZ;

-- Commission credit and the commission entry were committed in the same
-- transaction. Match the exact business ID and beneficiary wallet, never a
-- time/amount heuristic. Missing historical ledger evidence stays NULL.
UPDATE commission_entries ce
SET created_at = evidence.created_at
FROM (
    SELECT c.id, min(l.occurred_at) AS created_at
    FROM commission_entries c
    JOIN ledger_entries l ON l.business_id = c.id::text
        AND l.business_type = 'commission' AND l.entry_type = 'commission_credit'
    JOIN wallets w ON w.id = l.wallet_id
        AND w.user_id = c.beneficiary_user_id AND w.currency = c.currency
    GROUP BY c.id
) evidence
WHERE ce.id = evidence.id;

-- New entries (including those written by old workers during a rolling
-- upgrade) get the transaction timestamp without changing ledger behavior.
ALTER TABLE commission_entries ALTER COLUMN created_at SET DEFAULT now();
CREATE INDEX commission_entries_created_at_idx
    ON commission_entries (created_at DESC NULLS LAST, id DESC);
