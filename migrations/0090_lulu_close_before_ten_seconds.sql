-- Lulu's three shared games must close ten seconds before the upstream result.
-- Existing rounds retain their recorded authoritative close time; this affects
-- only future round creation and the game-type configuration shown to clients.
UPDATE game_types
SET close_before_seconds=10,updated_at=now()
WHERE rules->>'source'='lulu_ws'
  AND rules->'extras'->>'external_game' IN ('lh','xdy','race');
