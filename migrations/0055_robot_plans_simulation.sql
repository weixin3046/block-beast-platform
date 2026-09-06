CREATE TABLE robot_plans (
 id UUID PRIMARY KEY,
 user_id UUID NOT NULL REFERENCES users(id),
 operator_id UUID NOT NULL REFERENCES users(id),
 request_id TEXT NOT NULL,
 create_input JSONB NOT NULL,
 enabled BOOLEAN NOT NULL DEFAULT false,
 deleted BOOLEAN NOT NULL DEFAULT false,
 game_type TEXT NOT NULL CHECK (game_type IN ('hash_9','hash_13','hash_17','hash_19','hash_23','hash_29')),
 game_room_id UUID NOT NULL REFERENCES game_rooms(id),
 currency TEXT NOT NULL REFERENCES currencies(code),
 min_stake_minor BIGINT NOT NULL CHECK (min_stake_minor>0),
 max_stake_minor BIGINT NOT NULL CHECK (max_stake_minor>=min_stake_minor),
 selections JSONB NOT NULL CHECK (jsonb_typeof(selections)='array' AND jsonb_array_length(selections)>0),
 skip_min INTEGER NOT NULL CHECK (skip_min BETWEEN 1 AND 10000),
 skip_max INTEGER NOT NULL CHECK (skip_max BETWEEN skip_min AND 10000),
 next_round_sequence BIGINT,
 last_seen_sequence BIGINT,
 last_checked_at TIMESTAMPTZ,
 last_status TEXT NOT NULL DEFAULT 'ready',
 last_error TEXT NOT NULL DEFAULT '',
 last_bet_id UUID,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(operator_id,request_id)
);
CREATE INDEX robot_plans_due_idx ON robot_plans(last_checked_at NULLS FIRST,id) WHERE enabled AND NOT deleted;
CREATE INDEX robot_plans_user_idx ON robot_plans(user_id,created_at DESC);
ALTER TABLE bets ADD COLUMN is_simulated BOOLEAN NOT NULL DEFAULT false,
 ADD COLUMN robot_plan_id UUID REFERENCES robot_plans(id);
CREATE UNIQUE INDEX bets_robot_plan_round_idx ON bets(robot_plan_id,round_id) WHERE robot_plan_id IS NOT NULL;
