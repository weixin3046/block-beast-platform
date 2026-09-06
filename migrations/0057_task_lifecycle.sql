ALTER TABLE bet_task_configs
 ADD COLUMN title TEXT NOT NULL DEFAULT '投注任务',
 ADD COLUMN period_type TEXT NOT NULL DEFAULT 'daily' CHECK(period_type IN ('daily','global')),
 ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0,
 ADD COLUMN max_complete_count INTEGER NOT NULL DEFAULT 1 CHECK(max_complete_count BETWEEN 0 AND 100000),
 ADD COLUMN deleted BOOLEAN NOT NULL DEFAULT false,
 ADD COLUMN rewards JSONB NOT NULL DEFAULT '[]';
UPDATE bet_task_configs SET rewards=jsonb_build_array(jsonb_build_object('currency',reward_currency,'amount_minor',reward_minor));
ALTER TABLE bet_task_configs DROP CONSTRAINT bet_task_configs_accumulation_currency_threshold_minor_key;
CREATE TABLE task_progress (
 config_id UUID NOT NULL REFERENCES bet_task_configs(id),
 user_id UUID NOT NULL REFERENCES users(id),
 period_date DATE NOT NULL,
 progress_minor BIGINT NOT NULL DEFAULT 0 CHECK(progress_minor>=0),
 complete_count INTEGER NOT NULL DEFAULT 0 CHECK(complete_count>=0),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(config_id,user_id,period_date)
);
CREATE INDEX task_progress_user_date_idx ON task_progress(user_id,period_date);
INSERT INTO task_progress(config_id,user_id,period_date,progress_minor,complete_count)
 SELECT c.id,p.user_id,p.bet_date,least(p.total_stake_minor,c.threshold_minor),
 CASE WHEN EXISTS(SELECT 1 FROM bet_task_reward_records r WHERE r.config_id=c.id AND r.user_id=p.user_id AND r.bet_date=p.bet_date) THEN 1 ELSE 0 END
 FROM user_daily_bet_progress p JOIN bet_task_configs c ON c.accumulation_currency=p.accumulation_currency;
CREATE TABLE task_reward_claims(
 id UUID PRIMARY KEY,
 config_id UUID NOT NULL REFERENCES bet_task_configs(id),
 user_id UUID NOT NULL REFERENCES users(id),
 period_date DATE NOT NULL,
 complete_count INTEGER NOT NULL,
 rewards JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(config_id,user_id,period_date,complete_count)
);
CREATE INDEX task_reward_claims_user_created_idx ON task_reward_claims(user_id,created_at DESC);
