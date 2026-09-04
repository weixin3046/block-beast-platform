-- Network units must never determine the unit of a shared platform wallet.
ALTER TABLE withdrawals ADD COLUMN platform_decimals SMALLINT CHECK (platform_decimals BETWEEN 0 AND 18);
-- Preserve the unit used by old requests, including in-flight withdrawals.
UPDATE withdrawals SET platform_decimals=token_decimals;
-- Very old withdrawals may have no known unit: keep NULL and require manual
-- reconciliation rather than guessing a decimal scale during dispatch.
ALTER TABLE deposits
    ADD COLUMN provider_amount TEXT,
    ADD COLUMN chain_decimals SMALLINT CHECK (chain_decimals BETWEEN 0 AND 18),
    ADD COLUMN platform_decimals SMALLINT CHECK (platform_decimals BETWEEN 0 AND 18);
-- Unknown historical raw amounts and precisions remain NULL, never invented.
