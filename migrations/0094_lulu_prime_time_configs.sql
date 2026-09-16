-- Fixed Beijing 20:00-21:00 ("prime time") odds override for 星海逃杀.
-- While the window is active, bets on a play price and limit themselves from
-- this table instead of the daily lulu_room_play_currency_configs. Plays
-- without an enabled row fall back to the daily configuration, so the table
-- starts empty on purpose. lulu-lh and lulu-race never read it.
CREATE TABLE lulu_prime_time_configs (
    game_type_id UUID NOT NULL,
    room_id UUID NOT NULL,
    play_code TEXT NOT NULL,
    currency TEXT NOT NULL REFERENCES currencies(code),
    payout_multiplier BIGINT NOT NULL CHECK (payout_multiplier > 0),
    payout_divisor BIGINT NOT NULL CHECK (payout_divisor > 0),
    min_stake_minor BIGINT NOT NULL CHECK (min_stake_minor > 0),
    max_stake_minor BIGINT NOT NULL CHECK (max_stake_minor >= min_stake_minor),
    enabled BOOLEAN NOT NULL DEFAULT true,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (game_type_id, room_id, play_code, currency),
    FOREIGN KEY (game_type_id, play_code) REFERENCES lulu_play_configs(game_type_id, code) ON DELETE CASCADE,
    FOREIGN KEY (room_id, game_type_id) REFERENCES game_room_types(room_id, game_type_id) ON DELETE CASCADE
);
