-- Configuration only. Settlement remains unchanged until versioned bet snapshots
-- and atomic differential distributions are installed in the next migration.
CREATE TABLE hash_rebate_configs (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 game_type_id UUID NOT NULL REFERENCES game_types(id),
 room_id UUID NOT NULL REFERENCES game_rooms(id),
 currency TEXT NOT NULL REFERENCES currencies(code),
 enabled BOOLEAN NOT NULL DEFAULT true,
 version BIGINT NOT NULL DEFAULT 1 CHECK(version>0),
 rates INTEGER[] NOT NULL CHECK(array_ndims(rates)=1 AND array_lower(rates,1)=1 AND array_length(rates,1)=6
  AND rates[1]>=0 AND rates[6]<=1000
  AND rates[1]<=rates[2] AND rates[2]<=rates[3] AND rates[3]<=rates[4] AND rates[4]<=rates[5] AND rates[5]<=rates[6]
  AND array_position(rates,NULL) IS NULL),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(game_type_id,room_id,currency),
 FOREIGN KEY(room_id,game_type_id) REFERENCES game_room_types(room_id,game_type_id)
);

INSERT INTO hash_rebate_configs(game_type_id,room_id,currency,rates)
SELECT gt.id,gr.id,c.code,seed.rates
FROM (VALUES
 ('94000000-0000-4000-8000-000000000001'::uuid,ARRAY[14,16,20,22,25,26]),
 ('95000000-0000-4000-8000-000000000001'::uuid,ARRAY[8,12,16,18,20,21]),
 ('96000000-0000-4000-8000-000000000001'::uuid,ARRAY[7,9,11,13,15,16]),
 ('97000000-0000-4000-8000-000000000001'::uuid,ARRAY[4,5,6,8,10,11]),
 ('98000000-0000-4000-8000-000000000001'::uuid,ARRAY[1,2,3,4,5,6]),
 ('98500000-0000-4000-8000-000000000001'::uuid,ARRAY[0,0,1,2,3,4])
) seed(room_id,rates)
JOIN game_rooms gr ON gr.id=seed.room_id
JOIN game_room_types rt ON rt.room_id=gr.id
JOIN game_types gt ON gt.id=rt.game_type_id
CROSS JOIN currencies c
WHERE gt.code IN ('hash_9','hash_13','hash_17','hash_19','hash_23','hash_29')
 AND c.code IN ('POINTS','USDT','JADE','ORIGIN_STONE');
