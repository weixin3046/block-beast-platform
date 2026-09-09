ALTER TABLE lulu_collector_state
 ADD COLUMN last_cycle TEXT NOT NULL DEFAULT '',
 ADD COLUMN down_at BIGINT NOT NULL DEFAULT 0 CHECK(down_at>=0),
 ADD COLUMN recovered_at BIGINT NOT NULL DEFAULT 0 CHECK(recovered_at>=0),
 ADD COLUMN last_down_ms BIGINT NOT NULL DEFAULT 0 CHECK(last_down_ms>=0);
