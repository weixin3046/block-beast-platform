-- New accepted orders of every game type use the same canonical-order rule.
-- Historical orders remain unchanged because merge_enabled is false for them.
DROP INDEX bets_pending_merge_idx;

CREATE UNIQUE INDEX bets_pending_merge_idx
    ON bets(
        user_id,
        round_id,
        wallet_id,
        COALESCE(game_room_id, '00000000-0000-0000-0000-000000000000'::uuid),
        COALESCE(play_mode, ''),
        selection
    )
    WHERE status='accepted' AND merge_enabled;
