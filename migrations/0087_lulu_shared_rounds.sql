-- One external issue becomes one shared round.  Odds and stake limits are
-- independently configurable per existing rate room; room rebates stay shared.
CREATE TABLE lulu_play_configs (
    game_type_id UUID NOT NULL REFERENCES game_types(id) ON DELETE CASCADE,
    code TEXT NOT NULL CHECK (btrim(code) <> ''),
    name TEXT NOT NULL CHECK (btrim(name) <> ''),
    sort_order INTEGER NOT NULL DEFAULT 0,
    enabled BOOLEAN NOT NULL DEFAULT true,
    outcomes JSONB NOT NULL CHECK (jsonb_typeof(outcomes) = 'array' AND jsonb_array_length(outcomes) > 0),
    result_map JSONB NOT NULL CHECK (jsonb_typeof(result_map) = 'object'),
    dodge_mode BOOLEAN NOT NULL DEFAULT false,
    PRIMARY KEY (game_type_id, code)
);

CREATE TABLE lulu_room_play_currency_configs (
    game_type_id UUID NOT NULL,
    room_id UUID NOT NULL,
    play_code TEXT NOT NULL,
    currency TEXT NOT NULL REFERENCES currencies(code),
    payout_multiplier BIGINT NOT NULL CHECK (payout_multiplier > 0),
    payout_divisor BIGINT NOT NULL CHECK (payout_divisor > 0),
    min_stake_minor BIGINT NOT NULL CHECK (min_stake_minor > 0),
    max_stake_minor BIGINT NOT NULL CHECK (max_stake_minor >= min_stake_minor),
    PRIMARY KEY (game_type_id, room_id, play_code, currency),
    FOREIGN KEY (game_type_id, play_code) REFERENCES lulu_play_configs(game_type_id, code) ON DELETE CASCADE,
    FOREIGN KEY (room_id, game_type_id) REFERENCES game_room_types(room_id, game_type_id) ON DELETE CASCADE
);

WITH shared(code, name, external_game, outcomes) AS (
    VALUES
        ('lulu-xdy', '星海逃杀', 'xdy', '["1","2","3","4","5","6","7","8"]'::jsonb),
        ('lulu-lh', '怒翎破阵', 'lh', '["1","2"]'::jsonb),
        ('lulu-race', '绿茵疾冲', 'race', '["1","2","3","4","5","6"]'::jsonb)
)
INSERT INTO game_types(id, code, name, close_before_seconds, enabled, rules)
SELECT gen_random_uuid(), code, name, 3, true,
    jsonb_build_object('outcomes', outcomes, 'payout_multiplier', 1, 'source', 'lulu_ws',
        'extras', jsonb_build_object('external_game', external_game, 'lulu_shared', true))
FROM shared
ON CONFLICT (code) DO UPDATE SET name = EXCLUDED.name, enabled = true, rules = EXCLUDED.rules, updated_at = now();

INSERT INTO game_room_types(room_id, game_type_id, sort_order)
SELECT room.id, game_type.id, room.sort_order
FROM game_rooms room CROSS JOIN game_types game_type
WHERE room.game_kind = 'hash' AND game_type.code IN ('lulu-xdy', 'lulu-lh', 'lulu-race')
ON CONFLICT (room_id, game_type_id) DO NOTHING;

WITH plays(game_code, code, name, sort_order, outcomes, result_map, dodge_mode, multiplier, max_kind) AS (
    VALUES
      ('lulu-xdy','direct','直选',10,'["1","2","3","4","5","6","7","8"]'::jsonb,'{"1":["1"],"2":["2"],"3":["3"],"4":["4"],"5":["5"],"6":["6"],"7":["7"],"8":["8"]}'::jsonb,false,750,'direct'),
      ('lulu-xdy','up_down','上下',20,'["up","down"]'::jsonb,'{"1":["up"],"2":["up"],"3":["up"],"4":["up"],"5":["down"],"6":["down"],"7":["down"],"8":["down"]}'::jsonb,false,1972,'group'),
      ('lulu-xdy','left_right','左右',30,'["left","right"]'::jsonb,'{"1":["left"],"2":["right"],"3":["right"],"4":["left"],"5":["right"],"6":["left"],"7":["right"],"8":["left"]}'::jsonb,false,1972,'group'),
      ('lulu-xdy','odd_even','单双',40,'["odd","even"]'::jsonb,'{"1":["odd"],"2":["even"],"3":["odd"],"4":["even"],"5":["odd"],"6":["even"],"7":["odd"],"8":["even"]}'::jsonb,false,1972,'group'),
      ('lulu-xdy','dodge','躲房间',50,'["dodge_1","dodge_2","dodge_3","dodge_4","dodge_5","dodge_6","dodge_7","dodge_8"]'::jsonb,'{"1":["dodge_1"],"2":["dodge_2"],"3":["dodge_3"],"4":["dodge_4"],"5":["dodge_5"],"6":["dodge_6"],"7":["dodge_7"],"8":["dodge_8"]}'::jsonb,true,112,'dodge'),
      ('lulu-lh','winner','胜方',10,'["1","2"]'::jsonb,'{"1":["1"],"2":["2"]}'::jsonb,false,1972,'group'),
      ('lulu-race','direct','直选',10,'["1","2","3","4","5","6"]'::jsonb,'{"1":["1"],"2":["2"],"3":["3"],"4":["4"],"5":["5"],"6":["6"]}'::jsonb,false,750,'direct'),
      ('lulu-race','up_down','上下',20,'["up","down"]'::jsonb,'{"1":["up"],"2":["up"],"3":["up"],"4":["down"],"5":["down"],"6":["down"]}'::jsonb,false,1972,'group'),
      ('lulu-race','left_right','左右',30,'["left","right"]'::jsonb,'{"1":["left"],"2":["right"],"3":["right"],"4":["right"],"5":["left"],"6":["left"]}'::jsonb,false,1972,'group'),
      ('lulu-race','odd_even','单双',40,'["odd","even"]'::jsonb,'{"1":["odd"],"2":["even"],"3":["odd"],"4":["even"],"5":["odd"],"6":["even"]}'::jsonb,false,1972,'group'),
      ('lulu-race','dodge','躲选手',50,'["dodge_1","dodge_2","dodge_3","dodge_4","dodge_5","dodge_6"]'::jsonb,'{"1":["dodge_1"],"2":["dodge_2"],"3":["dodge_3"],"4":["dodge_4"],"5":["dodge_5"],"6":["dodge_6"]}'::jsonb,true,118,'dodge')
)
INSERT INTO lulu_play_configs(game_type_id, code, name, sort_order, outcomes, result_map, dodge_mode)
SELECT game_type.id, plays.code, plays.name, plays.sort_order, plays.outcomes, plays.result_map, plays.dodge_mode
FROM plays JOIN game_types game_type ON game_type.code = plays.game_code
ON CONFLICT (game_type_id, code) DO UPDATE SET name=EXCLUDED.name,sort_order=EXCLUDED.sort_order,outcomes=EXCLUDED.outcomes,result_map=EXCLUDED.result_map,dodge_mode=EXCLUDED.dodge_mode,enabled=true;

WITH plays(game_code, code, multiplier, max_kind) AS (
    VALUES ('lulu-xdy','direct',750,'direct'),('lulu-xdy','up_down',1972,'group'),('lulu-xdy','left_right',1972,'group'),('lulu-xdy','odd_even',1972,'group'),('lulu-xdy','dodge',112,'dodge'),('lulu-lh','winner',1972,'group'),('lulu-race','direct',750,'direct'),('lulu-race','up_down',1972,'group'),('lulu-race','left_right',1972,'group'),('lulu-race','odd_even',1972,'group'),('lulu-race','dodge',118,'dodge')
)
INSERT INTO lulu_room_play_currency_configs(game_type_id, room_id, play_code, currency, payout_multiplier, payout_divisor, min_stake_minor, max_stake_minor)
SELECT gt.id, room.id, plays.code, currency.code, plays.multiplier, 100,
    power(10::numeric,currency.decimals)::bigint,
    (CASE plays.max_kind WHEN 'direct' THEN 2000 WHEN 'dodge' THEN 10000 ELSE 5000 END * power(10::numeric,currency.decimals))::bigint
FROM plays JOIN game_types gt ON gt.code=plays.game_code
JOIN game_rooms room ON room.game_kind='hash'
CROSS JOIN currencies currency
ON CONFLICT (game_type_id, room_id, play_code, currency) DO NOTHING;

-- Result-only upstream messages previously persisted their confirmed outcome
-- without creating a platform round. Backfill those records as closed history:
-- they are never open for betting, but can settle and keep visible issue history
-- continuous for every result actually received from the upstream source.
INSERT INTO rounds(id, game_type_id, sequence, status, bet_closes_at, result_at)
SELECT gen_random_uuid(), game_type.id, draw.external_round, 'closed',
    COALESCE(draw.close_at, draw.result_received_at, now()),
    COALESCE(draw.close_at, draw.result_received_at, now())
FROM external_draw_rounds draw
JOIN game_types game_type ON game_type.code = CASE draw.game
    WHEN 'xdy' THEN 'lulu-xdy'
    WHEN 'lh' THEN 'lulu-lh'
    WHEN 'race' THEN 'lulu-race'
END
WHERE draw.source='lulu_ws' AND draw.status='confirmed'
ON CONFLICT (game_type_id, sequence) DO NOTHING;

UPDATE game_types SET enabled=false,updated_at=now()
WHERE code IN ('lulu-xdy-direct','lulu-xdy-up-down','lulu-xdy-left-right','lulu-xdy-odd-even','lulu-xdy-dodge','lulu-lh-winner','lulu-race-direct','lulu-race-up-down','lulu-race-odd-even','lulu-race-dodge');
