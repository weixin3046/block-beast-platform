CREATE TABLE agent_commission_rates (
    agent_user_id UUID PRIMARY KEY REFERENCES users(id),
    rate_basis_points INTEGER NOT NULL CHECK (rate_basis_points >= 0 AND rate_basis_points <= 10000),
    updated_by UUID REFERENCES users(id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
