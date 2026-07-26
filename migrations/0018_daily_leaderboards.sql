CREATE TABLE leaderboard_daily (
    leaderboard_date DATE NOT NULL,
    currency TEXT NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    effective_stake_minor BIGINT NOT NULL CHECK (effective_stake_minor >= 0),
    payout_minor BIGINT NOT NULL CHECK (payout_minor >= 0),
    net_profit_minor BIGINT NOT NULL,
    bet_count INTEGER NOT NULL CHECK (bet_count >= 0),
    win_count INTEGER NOT NULL CHECK (win_count >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (leaderboard_date, currency, user_id)
);

CREATE INDEX leaderboard_daily_turnover_idx
    ON leaderboard_daily (leaderboard_date, currency, effective_stake_minor DESC, user_id);
CREATE INDEX leaderboard_daily_profit_idx
    ON leaderboard_daily (leaderboard_date, currency, net_profit_minor DESC, user_id);
