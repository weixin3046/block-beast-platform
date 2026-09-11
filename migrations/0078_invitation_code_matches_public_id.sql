ALTER TABLE users ALTER COLUMN invitation_code DROP DEFAULT;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_invitation_code_positive;
-- Use a collision-free temporary domain because an old invitation code may
-- equal another user's public ID while the unique index is still active.
UPDATE users SET invitation_code=-public_id WHERE invitation_code<>-public_id;
UPDATE users SET invitation_code=public_id;
ALTER TABLE users ADD CONSTRAINT users_invitation_code_matches_public_id CHECK (invitation_code=public_id);
CREATE OR REPLACE FUNCTION users_set_invitation_code_from_public_id() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN NEW.invitation_code := NEW.public_id; RETURN NEW; END $$;
DROP TRIGGER IF EXISTS users_invitation_code_from_public_id ON users;
CREATE TRIGGER users_invitation_code_from_public_id BEFORE INSERT OR UPDATE OF public_id ON users
FOR EACH ROW EXECUTE FUNCTION users_set_invitation_code_from_public_id();
