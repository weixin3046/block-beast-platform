-- New requests have three minutes to transfer; retain deadlines already issued.
ALTER TABLE lulu_orders ALTER COLUMN expires_at SET DEFAULT (now()+interval '3 minutes');
CREATE INDEX lulu_deposit_expiration ON lulu_orders(expires_at) WHERE kind='deposit' AND status='requested';
UPDATE lulu_orders SET status='expired',updated_at=now() WHERE kind='deposit' AND status='requested' AND expires_at<now();
