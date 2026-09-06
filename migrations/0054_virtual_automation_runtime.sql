ALTER TABLE virtual_account_automations
    ADD COLUMN game_room_id UUID REFERENCES game_rooms(id),
    ADD COLUMN play_mode TEXT NOT NULL DEFAULT 'road' CHECK (play_mode IN ('road','guess','dodge')),
    ADD COLUMN run_id UUID,
    ADD COLUMN lease_until TIMESTAMPTZ,
    ADD COLUMN last_finished_at TIMESTAMPTZ,
    ADD COLUMN run_status TEXT NOT NULL DEFAULT 'stopped',
    ADD COLUMN last_results JSONB NOT NULL DEFAULT '[]'::jsonb;

-- Old configurations have no selected room. Do not silently pick one for them.
UPDATE virtual_account_automations SET enabled=false;
ALTER TABLE virtual_account_automations
    ADD CONSTRAINT virtual_automation_enabled_room CHECK (NOT enabled OR game_room_id IS NOT NULL);
