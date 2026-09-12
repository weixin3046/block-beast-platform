-- Fresh installations must not expose an activity that operations has not configured.
-- The legacy seed used a reserved code, while admin-created configs receive a UUID-based code.
-- Preserve any used legacy configuration because lucky_spin_records retains its history.
DELETE FROM spin_configs AS config
WHERE config.code = 'lucky-spin'
  AND NOT EXISTS (
      SELECT 1
      FROM lucky_spin_records AS record
      WHERE record.spin_id = config.id
  );
