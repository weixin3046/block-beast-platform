-- Completed cycles have already been paid. Raising a completion limit must
-- require new betting instead of reusing the old final-cycle progress.
UPDATE task_progress p SET progress_minor=0
FROM bet_task_configs c
WHERE c.id=p.config_id AND c.max_complete_count>0
AND p.complete_count>=c.max_complete_count AND p.progress_minor>0;
