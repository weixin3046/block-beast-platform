INSERT INTO roles (id, code, description)
VALUES
    ('00000000-0000-0000-0000-000000000001', 'player', 'player account'),
    ('00000000-0000-0000-0000-000000000002', 'operator', 'back-office operator'),
    ('00000000-0000-0000-0000-000000000003', 'admin', 'platform administrator')
ON CONFLICT (code) DO NOTHING;
