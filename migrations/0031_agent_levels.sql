ALTER TABLE users ADD COLUMN IF NOT EXISTS agent_level SMALLINT;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_agent_level_valid;
ALTER TABLE users ADD CONSTRAINT users_agent_level_valid CHECK (agent_level IS NULL OR agent_level BETWEEN 1 AND 99);
CREATE INDEX IF NOT EXISTS users_agent_level_idx ON users(agent_level) WHERE agent_level IS NOT NULL;
