ALTER TABLE users DROP CONSTRAINT IF EXISTS users_agent_level_valid;
ALTER TABLE users ADD CONSTRAINT users_agent_level_valid CHECK (agent_level IS NULL OR agent_level BETWEEN 1 AND 6);

-- All agent levels start at 0% rebate. Administrators configure rates later.
INSERT INTO agent_commission_rates (agent_user_id, rate_basis_points)
SELECT id, 0 FROM users WHERE agent_level IS NOT NULL
ON CONFLICT (agent_user_id) DO UPDATE SET rate_basis_points = 0, updated_at = now();
