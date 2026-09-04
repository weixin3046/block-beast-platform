DROP TABLE IF EXISTS leaderboard_daily;

CREATE TABLE leaderboard_periods (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    period_type TEXT NOT NULL CHECK (period_type IN ('daily','weekly')),
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL CHECK (ends_at > starts_at),
    status TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running','frozen')),
    refreshed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    frozen_at TIMESTAMPTZ,
    UNIQUE(period_type, starts_at)
);

CREATE TABLE leaderboard_entries (
    period_id UUID NOT NULL REFERENCES leaderboard_periods(id) ON DELETE CASCADE,
    currency TEXT NOT NULL CHECK (btrim(currency) <> ''),
    user_id UUID NOT NULL REFERENCES users(id),
    public_user_id BIGINT NOT NULL,
    display_name TEXT NOT NULL,
    avatar_url TEXT NOT NULL DEFAULT '',
    is_virtual BOOLEAN NOT NULL DEFAULT false,
    effective_stake_minor BIGINT NOT NULL CHECK (effective_stake_minor >= 0),
    first_effective_at TIMESTAMPTZ NOT NULL,
    rank INTEGER,
    PRIMARY KEY(period_id, currency, user_id)
);
CREATE INDEX leaderboard_entries_rank_idx ON leaderboard_entries(period_id, currency, effective_stake_minor DESC, first_effective_at, public_user_id);

CREATE TABLE leaderboard_reward_rule_versions (
    period_type TEXT NOT NULL CHECK (period_type IN ('daily','weekly')),
    currency TEXT NOT NULL CHECK (btrim(currency) <> ''),
    version BIGINT NOT NULL DEFAULT 0 CHECK (version >= 0),
    PRIMARY KEY(period_type, currency)
);
CREATE TABLE leaderboard_reward_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    period_type TEXT NOT NULL CHECK (period_type IN ('daily','weekly')),
    currency TEXT NOT NULL CHECK (btrim(currency) <> ''),
    rank_from INTEGER NOT NULL CHECK (rank_from > 0),
    rank_to INTEGER NOT NULL CHECK (rank_to >= rank_from),
    reward_currency TEXT NOT NULL CHECK (btrim(reward_currency) <> ''),
    reward_minor BIGINT NOT NULL CHECK (reward_minor > 0),
    enabled BOOLEAN NOT NULL DEFAULT true,
    UNIQUE(period_type, currency, rank_from, rank_to)
);
CREATE TABLE leaderboard_reward_distributions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    period_id UUID NOT NULL REFERENCES leaderboard_periods(id),
    leaderboard_currency TEXT NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id),
    rank INTEGER NOT NULL CHECK (rank > 0),
    reward_currency TEXT NOT NULL,
    reward_minor BIGINT NOT NULL CHECK (reward_minor > 0),
    status TEXT NOT NULL DEFAULT 'paid' CHECK (status IN ('paid')),
    paid_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(period_id, leaderboard_currency, user_id)
);
CREATE INDEX leaderboard_reward_distributions_query_idx ON leaderboard_reward_distributions(period_id, leaderboard_currency, paid_at DESC);
