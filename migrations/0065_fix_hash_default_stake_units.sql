-- 0041 seeded integer point limits; 0044 declares these currencies at 3
-- decimals. Repair only complete legacy default limit triples. Custom or
-- partially edited triples are deliberately left for operator review.
-- No wallets, ledgers, bets, odds or minimum stakes are modified.
DO $$
DECLARE changed_rows INTEGER;
BEGIN
    PERFORM 1 FROM hash_game_settings WHERE singleton=true FOR UPDATE;
    WITH defaults(room_id,high_guess,high_dodge,high_road,low_guess,low_dodge,low_road) AS (
        VALUES
        ('94000000-0000-4000-8000-000000000001'::uuid,500,1000,1000,50,100,50),
        ('95000000-0000-4000-8000-000000000001'::uuid,500,2000,2000,50,200,100),
        ('96000000-0000-4000-8000-000000000001'::uuid,1000,5000,5000,100,500,200),
        ('97000000-0000-4000-8000-000000000001'::uuid,2000,10000,10000,200,1000,500),
        ('98000000-0000-4000-8000-000000000001'::uuid,2000,20000,20000,500,2000,1000),
        ('98500000-0000-4000-8000-000000000001'::uuid,2000,30000,30000,500,5000,3000)
    )
    UPDATE hash_room_currency_configs c
    SET guess_max_stake_minor=c.guess_max_stake_minor*1000,
        dodge_max_stake_minor=c.dodge_max_stake_minor*1000,
        road_max_stake_minor=c.road_max_stake_minor*1000
    FROM defaults d,currencies cur
    WHERE c.room_id=d.room_id AND cur.code=c.currency AND cur.decimals=3
      AND c.currency IN ('POINTS','JADE','ORIGIN_STONE')
      AND c.guess_max_stake_minor=CASE WHEN c.currency='JADE' THEN d.low_guess ELSE d.high_guess END
      AND c.dodge_max_stake_minor=CASE WHEN c.currency='JADE' THEN d.low_dodge ELSE d.high_dodge END
      AND c.road_max_stake_minor=CASE WHEN c.currency='JADE' THEN d.low_road ELSE d.high_road END;
    GET DIAGNOSTICS changed_rows = ROW_COUNT;
    IF changed_rows > 0 THEN
        UPDATE hash_game_settings SET version=version+1,updated_at=now() WHERE singleton=true;
    END IF;
END $$;
