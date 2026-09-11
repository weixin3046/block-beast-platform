-- Deploy with old API/Worker stopped. Preserve all existing bets and funds.
ALTER TABLE rounds DROP CONSTRAINT rounds_status_check;
ALTER TABLE rounds ADD CONSTRAINT rounds_status_check
CHECK (status IN ('scheduled','open','closed','settling','settled','cancelled'));

-- Only the first unfinished round may remain open. Closed/settling rounds
-- block opening their successors until their outcome is finalized.
UPDATE rounds r SET status='scheduled',version=version+1
WHERE r.status='open' AND EXISTS (
 SELECT 1 FROM rounds prior WHERE prior.game_type_id=r.game_type_id
 AND prior.sequence<r.sequence AND prior.status IN ('open','closed','settling','scheduled')
);
CREATE INDEX rounds_unfinished_sequence_idx ON rounds(game_type_id,sequence)
WHERE status IN ('scheduled','open','closed','settling');
