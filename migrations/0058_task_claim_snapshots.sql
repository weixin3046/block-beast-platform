-- Keep the full task rule used for each successful claim.
ALTER TABLE task_reward_claims ADD COLUMN config_snapshot JSONB;
