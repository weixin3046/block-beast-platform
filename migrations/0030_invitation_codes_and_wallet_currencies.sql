CREATE SEQUENCE IF NOT EXISTS user_invitation_code_seq START WITH 101;

ALTER TABLE users ADD COLUMN IF NOT EXISTS invitation_code BIGINT;
UPDATE users SET invitation_code = nextval('user_invitation_code_seq') WHERE invitation_code IS NULL;
ALTER TABLE users ALTER COLUMN invitation_code SET DEFAULT nextval('user_invitation_code_seq');
ALTER TABLE users ALTER COLUMN invitation_code SET NOT NULL;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_invitation_code_positive;
ALTER TABLE users ADD CONSTRAINT users_invitation_code_positive CHECK (invitation_code >= 101);
CREATE UNIQUE INDEX IF NOT EXISTS users_invitation_code_key ON users(invitation_code);
SELECT setval('user_invitation_code_seq', GREATEST((SELECT COALESCE(MAX(invitation_code), 100) FROM users), 100), true);

-- POINTS remains 宝石 and STAMINA remains activity-only stamina.
INSERT INTO wallets (id, user_id, currency)
SELECT gen_random_uuid(), u.id, c.currency
FROM users u CROSS JOIN (VALUES ('JADE'), ('ORIGIN_STONE')) AS c(currency)
ON CONFLICT (user_id, currency) DO NOTHING;
