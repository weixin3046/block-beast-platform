-- Preserve configurations referenced by historical draws.
ALTER TABLE spin_configs ADD COLUMN deleted BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE spin_prizes ADD COLUMN disabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE spin_prizes DROP CONSTRAINT spin_prizes_weight_check;
ALTER TABLE spin_prizes ADD CONSTRAINT spin_prizes_weight_check CHECK(weight >= 0);
CREATE INDEX lucky_spin_records_spin_created_idx ON lucky_spin_records(spin_id,created_at DESC,id DESC);
CREATE INDEX lucky_spin_records_created_idx ON lucky_spin_records(created_at DESC,id DESC);
