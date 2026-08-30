ALTER TABLE users DROP CONSTRAINT IF EXISTS users_invitation_code_positive;

-- 项目尚未上线，直接按创建时间重新编号现有账号，避免保留 101 起的旧号码段。
-- 先写入互不冲突的负数，再切换到 10001 起的正式号码，兼容唯一索引。
WITH ranked AS (
    SELECT id, row_number() OVER (ORDER BY created_at, id) AS position
    FROM users
)
UPDATE users
SET invitation_code = -ranked.position
FROM ranked
WHERE users.id = ranked.id;

WITH ranked AS (
    SELECT id, row_number() OVER (ORDER BY created_at, id) AS position
    FROM users
)
UPDATE users
SET invitation_code = 10000 + ranked.position
FROM ranked
WHERE users.id = ranked.id;

ALTER TABLE users
    ADD CONSTRAINT users_invitation_code_positive CHECK (invitation_code >= 10001);

ALTER SEQUENCE user_invitation_code_seq RESTART WITH 10001;

DO $$
DECLARE
    maximum_code BIGINT;
BEGIN
    SELECT max(invitation_code) INTO maximum_code FROM users;
    IF maximum_code IS NULL THEN
        PERFORM setval('user_invitation_code_seq', 10001, false);
    ELSE
        PERFORM setval('user_invitation_code_seq', maximum_code, true);
    END IF;
END
$$;

ALTER TABLE users
    ALTER COLUMN invitation_code SET DEFAULT nextval('user_invitation_code_seq');
