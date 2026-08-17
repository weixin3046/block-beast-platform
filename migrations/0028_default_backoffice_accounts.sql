-- 默认后台账号。仅在账号不存在时创建，重复执行不会重置既有密码或角色。
-- 密码以 Argon2id 哈希保存，不在数据库中保存明文。
INSERT INTO users (id, login_name, display_name)
VALUES
    ('10000000-0000-0000-0000-000000000001', 'xingxing001', 'xingxing001'),
    ('10000000-0000-0000-0000-000000000002', 'xingxing002', 'xingxing002'),
    ('10000000-0000-0000-0000-000000000003', 'xingxing003', 'xingxing003'),
    ('10000000-0000-0000-0000-000000000004', 'xingxing004', 'xingxing004'),
    ('10000000-0000-0000-0000-000000000005', 'xingxing005', 'xingxing005'),
    ('10000000-0000-0000-0000-000000000006', 'xingxing006', 'xingxing006')
ON CONFLICT (login_name) DO NOTHING;

INSERT INTO auth_identities (id, user_id, provider, subject, password_hash)
SELECT account.identity_id, u.id, 'password', account.login_name,
    '$argon2id$v=19$m=65536,t=3,p=4$zenC/UFqOOFaC4Qsmxjivw$vRal8hhu/ZIgMvn7wJ3Nz6PVX0hc6KF86KHs6ksFHGI'
FROM (VALUES
    ('xingxing001', '20000000-0000-0000-0000-000000000001'::uuid),
    ('xingxing002', '20000000-0000-0000-0000-000000000002'::uuid),
    ('xingxing003', '20000000-0000-0000-0000-000000000003'::uuid),
    ('xingxing004', '20000000-0000-0000-0000-000000000004'::uuid),
    ('xingxing005', '20000000-0000-0000-0000-000000000005'::uuid),
    ('xingxing006', '20000000-0000-0000-0000-000000000006'::uuid)
) AS account(login_name, identity_id)
JOIN users u ON u.login_name = account.login_name
ON CONFLICT (provider, subject) DO NOTHING;

INSERT INTO user_roles (user_id, role_id)
SELECT u.id, r.id
FROM users u
JOIN roles r ON r.code = 'operator'
WHERE u.login_name IN ('xingxing001', 'xingxing002', 'xingxing003', 'xingxing004', 'xingxing005', 'xingxing006')
ON CONFLICT DO NOTHING;
