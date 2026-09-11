-- Rebate settings are now shared by odds room.  Keep the original matrix
-- intact because historical bet snapshots retain its config_id for audit.
CREATE TABLE room_rebate_configs (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 room_id UUID NOT NULL UNIQUE REFERENCES game_rooms(id),
 enabled BOOLEAN NOT NULL DEFAULT true,
 version BIGINT NOT NULL DEFAULT 1 CHECK(version>0),
 rates INTEGER[] NOT NULL CHECK(array_ndims(rates)=1 AND array_lower(rates,1)=1 AND array_length(rates,1)=6
  AND rates[1]>=0 AND rates[6]<=1000
  AND rates[1]<=rates[2] AND rates[2]<=rates[3] AND rates[3]<=rates[4] AND rates[4]<=rates[5] AND rates[5]<=rates[6]
  AND array_position(rates,NULL) IS NULL),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A room previously had one setting per game type and currency.  When these
-- differ, use the most recently edited one as the unified starting setting;
-- all original rows remain available for historical snapshot inspection.
INSERT INTO room_rebate_configs(room_id,enabled,version,rates,updated_at)
SELECT DISTINCT ON (room_id) room_id,enabled,version,rates,updated_at
FROM hash_rebate_configs
ORDER BY room_id,updated_at DESC,id DESC;

ALTER TABLE bet_rebate_snapshots
 ADD COLUMN room_config_id UUID REFERENCES room_rebate_configs(id);
