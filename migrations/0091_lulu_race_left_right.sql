-- Restore the shared "left/right" play for Lulu race.  Some deployments
-- received the shared-round migration before this play was included.
INSERT INTO lulu_play_configs(game_type_id, code, name, sort_order, outcomes, result_map, dodge_mode)
SELECT id, 'left_right', '左右', 30,
    '["left","right"]'::jsonb,
    '{"1":["left"],"2":["right"],"3":["right"],"4":["right"],"5":["left"],"6":["left"]}'::jsonb,
    false
FROM game_types
WHERE code = 'lulu-race'
ON CONFLICT (game_type_id, code) DO UPDATE
SET name = EXCLUDED.name,
    sort_order = EXCLUDED.sort_order,
    outcomes = EXCLUDED.outcomes,
    result_map = EXCLUDED.result_map,
    dodge_mode = EXCLUDED.dodge_mode,
    enabled = true;

INSERT INTO lulu_room_play_currency_configs(
    game_type_id, room_id, play_code, currency,
    payout_multiplier, payout_divisor, min_stake_minor, max_stake_minor
)
SELECT gt.id, room.id, 'left_right', currency.code,
    1972, 100,
    power(10::numeric, currency.decimals)::bigint,
    (5000 * power(10::numeric, currency.decimals))::bigint
FROM game_types gt
JOIN game_rooms room ON room.game_kind = 'hash'
CROSS JOIN currencies currency
WHERE gt.code = 'lulu-race'
ON CONFLICT (game_type_id, room_id, play_code, currency) DO NOTHING;
