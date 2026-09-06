-- Old bets stay version 1. Only the new API explicitly creates version 2 bets.
ALTER TABLE bets ADD COLUMN rebate_version SMALLINT NOT NULL DEFAULT 1 CHECK(rebate_version IN (1,2));
CREATE TABLE bet_rebate_snapshots (
 bet_id UUID PRIMARY KEY REFERENCES bets(id),
 config_id UUID REFERENCES hash_rebate_configs(id),
 config_version BIGINT,
 enabled BOOLEAN NOT NULL,
 ancestors JSONB NOT NULL CHECK(jsonb_typeof(ancestors)='array'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE rebate_allocations (
 commission_id UUID PRIMARY KEY REFERENCES commission_entries(id),
 bet_id UUID NOT NULL REFERENCES bet_rebate_snapshots(bet_id),
 beneficiary_user_id UUID NOT NULL REFERENCES users(id),
 base_minor BIGINT NOT NULL CHECK(base_minor>0),
 agent_level SMALLINT NOT NULL CHECK(agent_level BETWEEN 1 AND 6),
 differential_per_mille INTEGER NOT NULL CHECK(differential_per_mille BETWEEN 1 AND 1000),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(bet_id,beneficiary_user_id)
);
CREATE INDEX rebate_allocations_beneficiary_created_idx ON rebate_allocations(beneficiary_user_id,created_at DESC);
