-- Stop old API/Worker writers before applying. Historical orders and their
-- immutable ledgers are not rewritten; only new orders opt into merging.
ALTER TABLE bets
    ADD COLUMN merge_enabled BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN placement_count BIGINT NOT NULL DEFAULT 1 CHECK (placement_count > 0),
    ADD COLUMN last_placed_at TIMESTAMPTZ;

CREATE UNIQUE INDEX bets_pending_merge_idx
    ON bets(user_id,round_id,wallet_id,game_room_id,play_mode,(selection->>'pick'))
    WHERE status='accepted' AND merge_enabled;

-- One row per successfully accepted request, including simulated placements.
-- A stable order ID allows cancellation/settlement/audit references to survive
-- further placements. Each request retains its own amount and identity.
CREATE TABLE bet_placements (
    id UUID PRIMARY KEY,
    bet_id UUID NOT NULL REFERENCES bets(id),
    user_id UUID NOT NULL REFERENCES users(id),
    round_id UUID NOT NULL REFERENCES rounds(id),
    client_request_id TEXT NOT NULL,
    currency TEXT NOT NULL REFERENCES currencies(code),
    game_room_id UUID REFERENCES game_rooms(id),
    play_mode TEXT,
    selection JSONB NOT NULL,
    stake_minor BIGINT NOT NULL CHECK (stake_minor > 0),
    robot_plan_id UUID REFERENCES robot_plans(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(user_id,client_request_id)
);
CREATE INDEX bet_placements_bet_idx ON bet_placements(bet_id,created_at DESC,id);
CREATE UNIQUE INDEX bet_placements_robot_round_idx
    ON bet_placements(robot_plan_id,round_id) WHERE robot_plan_id IS NOT NULL;
INSERT INTO bet_placements(id,bet_id,user_id,round_id,client_request_id,currency,
    game_room_id,play_mode,selection,stake_minor,robot_plan_id,created_at)
SELECT b.id,b.id,b.user_id,b.round_id,b.client_request_id,w.currency,
    b.game_room_id,b.play_mode,b.selection,b.stake_minor,b.robot_plan_id,b.created_at
FROM bets b JOIN wallets w ON w.id=b.wallet_id;

-- Keep business_id = canonical bet ID on EVERY debit. Other business types
-- retain their original deduplication semantics, as do historical bet debits.
ALTER TABLE ledger_entries ADD COLUMN bet_placement_id UUID REFERENCES bet_placements(id),
    ADD CONSTRAINT ledger_bet_placement_kind CHECK
      (bet_placement_id IS NULL OR (business_type='bet' AND entry_type='bet_debit'));
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_wallet_id_business_type_business_id_entry_ty_key;
CREATE UNIQUE INDEX ledger_entries_business_unique_idx
    ON ledger_entries(wallet_id,business_type,business_id,entry_type)
    WHERE bet_placement_id IS NULL;
CREATE UNIQUE INDEX ledger_entries_bet_placement_unique_idx
    ON ledger_entries(bet_placement_id) WHERE bet_placement_id IS NOT NULL;
CREATE INDEX ledger_entries_bet_debit_idx
    ON ledger_entries(wallet_id,business_id,occurred_at DESC,id)
    WHERE business_type='bet' AND entry_type='bet_debit';
