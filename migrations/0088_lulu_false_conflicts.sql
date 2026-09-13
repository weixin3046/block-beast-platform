-- Restore only identical JSONB results misclassified by textual comparison.
-- Genuine conflicts remain quarantined; no balances or settled bets change.
UPDATE external_draw_rounds
SET status='confirmed', conflict_outcome=NULL, updated_at=now()
WHERE source='lulu_ws' AND status='conflict'
  AND jsonb_typeof(outcome)='array' AND outcome=conflict_outcome;
