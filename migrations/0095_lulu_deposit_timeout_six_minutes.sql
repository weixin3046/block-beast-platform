ALTER TABLE lulu_orders
ALTER COLUMN expires_at SET DEFAULT (now() + interval '6 minutes');