CREATE TABLE external_draw_rounds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source TEXT NOT NULL CHECK (source = 'lulu_ws'),
    game TEXT NOT NULL CHECK (game IN ('lh','xdy','race')),
    external_round BIGINT NOT NULL CHECK (external_round > 0),
    close_at TIMESTAMPTZ,
    outcome JSONB,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','confirmed','conflict')),
    conflict_outcome JSONB,
    first_received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    result_received_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source, game, external_round)
);

CREATE INDEX external_draw_rounds_result_idx
    ON external_draw_rounds (game, external_round)
    WHERE status = 'confirmed';

WITH limits AS (
    SELECT jsonb_object_agg(code, jsonb_build_object(
        'min_stake_minor', power(10::numeric, decimals)::bigint,
        'max_stake_minor', (2000 * power(10::numeric, decimals))::bigint
    )) AS direct,
    jsonb_object_agg(code, jsonb_build_object(
        'min_stake_minor', power(10::numeric, decimals)::bigint,
        'max_stake_minor', (5000 * power(10::numeric, decimals))::bigint
    )) AS group_limit,
    jsonb_object_agg(code, jsonb_build_object(
        'min_stake_minor', power(10::numeric, decimals)::bigint,
        'max_stake_minor', (10000 * power(10::numeric, decimals))::bigint
    )) AS dodge
    FROM currencies WHERE enabled
), plays(code, name, rules, limit_kind) AS (
    VALUES
    ('lulu-xdy-direct', '星海逃杀-直选', '{"outcomes":["1","2","3","4","5","6","7","8"],"payout_multiplier":750,"payout_divisor":100,"source":"lulu_ws","extras":{"external_game":"xdy","result_map":{"1":["1"],"2":["2"],"3":["3"],"4":["4"],"5":["5"],"6":["6"],"7":["7"],"8":["8"]}}}'::jsonb, 'direct'),
    ('lulu-xdy-up-down', '星海逃杀-上下', '{"outcomes":["up","down"],"payout_multiplier":1972,"payout_divisor":100,"source":"lulu_ws","extras":{"external_game":"xdy","result_map":{"1":["up"],"2":["up"],"3":["up"],"4":["up"],"5":["down"],"6":["down"],"7":["down"],"8":["down"]}}}'::jsonb, 'group'),
    ('lulu-xdy-left-right', '星海逃杀-左右', '{"outcomes":["left","right"],"payout_multiplier":1972,"payout_divisor":100,"source":"lulu_ws","extras":{"external_game":"xdy","result_map":{"1":["left"],"2":["right"],"3":["right"],"4":["left"],"5":["right"],"6":["left"],"7":["right"],"8":["left"]}}}'::jsonb, 'group'),
    ('lulu-xdy-odd-even', '星海逃杀-单双', '{"outcomes":["odd","even"],"payout_multiplier":1972,"payout_divisor":100,"source":"lulu_ws","extras":{"external_game":"xdy","result_map":{"1":["odd"],"2":["even"],"3":["odd"],"4":["even"],"5":["odd"],"6":["even"],"7":["odd"],"8":["even"]}}}'::jsonb, 'group'),
    ('lulu-xdy-dodge', '星海逃杀-躲房间', '{"outcomes":["dodge_1","dodge_2","dodge_3","dodge_4","dodge_5","dodge_6","dodge_7","dodge_8"],"payout_multiplier":112,"payout_divisor":100,"dodge_mode":true,"source":"lulu_ws","extras":{"external_game":"xdy","result_map":{"1":["dodge_1"],"2":["dodge_2"],"3":["dodge_3"],"4":["dodge_4"],"5":["dodge_5"],"6":["dodge_6"],"7":["dodge_7"],"8":["dodge_8"]}}}'::jsonb, 'dodge'),
    ('lulu-lh-winner', '怒翎破阵-胜方', '{"outcomes":["1","2"],"payout_multiplier":1972,"payout_divisor":100,"source":"lulu_ws","extras":{"external_game":"lh","result_map":{"1":["1"],"2":["2"]}}}'::jsonb, 'group'),
    ('lulu-race-direct', '绿茵疾冲-直选', '{"outcomes":["1","2","3","4","5","6"],"payout_multiplier":750,"payout_divisor":100,"source":"lulu_ws","extras":{"external_game":"race","result_map":{"1":["1"],"2":["2"],"3":["3"],"4":["4"],"5":["5"],"6":["6"]}}}'::jsonb, 'direct'),
    ('lulu-race-up-down', '绿茵疾冲-上下', '{"outcomes":["up","down"],"payout_multiplier":1972,"payout_divisor":100,"source":"lulu_ws","extras":{"external_game":"race","result_map":{"1":["up"],"2":["up"],"3":["up"],"4":["down"],"5":["down"],"6":["down"]}}}'::jsonb, 'group'),
    ('lulu-race-odd-even', '绿茵疾冲-单双', '{"outcomes":["odd","even"],"payout_multiplier":1972,"payout_divisor":100,"source":"lulu_ws","extras":{"external_game":"race","result_map":{"1":["odd"],"2":["even"],"3":["odd"],"4":["even"],"5":["odd"],"6":["even"]}}}'::jsonb, 'group'),
    ('lulu-race-dodge', '绿茵疾冲-躲选手', '{"outcomes":["dodge_1","dodge_2","dodge_3","dodge_4","dodge_5","dodge_6"],"payout_multiplier":118,"payout_divisor":100,"dodge_mode":true,"source":"lulu_ws","extras":{"external_game":"race","result_map":{"1":["dodge_1"],"2":["dodge_2"],"3":["dodge_3"],"4":["dodge_4"],"5":["dodge_5"],"6":["dodge_6"]}}}'::jsonb, 'dodge')
)
INSERT INTO game_types(id, code, name, close_before_seconds, enabled, rules)
SELECT gen_random_uuid(), plays.code, plays.name, 3, true,
    jsonb_set(plays.rules, '{bet_limits}', CASE plays.limit_kind
        WHEN 'direct' THEN limits.direct WHEN 'group' THEN limits.group_limit ELSE limits.dodge END)
FROM plays CROSS JOIN limits
ON CONFLICT (code) DO NOTHING;
