-- UUID 继续作为内部主键；public_id 是面向前台、后台和客服展示的连续数字用户 ID。
CREATE SEQUENCE IF NOT EXISTS users_public_id_seq START WITH 100000;

ALTER TABLE users ADD COLUMN IF NOT EXISTS public_id BIGINT;

WITH numbered AS (
    SELECT id, 99999 + row_number() OVER (ORDER BY created_at, id) AS public_id
    FROM users
    WHERE public_id IS NULL
)
UPDATE users u
SET public_id = numbered.public_id
FROM numbered
WHERE u.id = numbered.id;

ALTER TABLE users ALTER COLUMN public_id SET DEFAULT nextval('users_public_id_seq');
ALTER TABLE users ALTER COLUMN public_id SET NOT NULL;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_public_id_minimum;
ALTER TABLE users ADD CONSTRAINT users_public_id_minimum CHECK (public_id >= 100000);
CREATE UNIQUE INDEX IF NOT EXISTS users_public_id_key ON users(public_id);

SELECT setval('users_public_id_seq', GREATEST((SELECT COALESCE(MAX(public_id), 99999) FROM users), 99999), true);
