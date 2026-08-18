ALTER TABLE users ADD COLUMN IF NOT EXISTS avatar_url TEXT NOT NULL DEFAULT '';

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_avatar_url_length;
ALTER TABLE users ADD CONSTRAINT users_avatar_url_length CHECK (char_length(avatar_url) <= 2048);
