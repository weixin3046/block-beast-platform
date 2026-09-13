-- 0087 introduced Lulu's per-play modes, but bets.play_mode still retained
-- the hash-only constraint from 0040.  Permit every persisted Lulu mode while
-- preserving the existing hash modes and nullable historical rows.
ALTER TABLE bets
    DROP CONSTRAINT IF EXISTS bets_play_mode_check;

ALTER TABLE bets
    ADD CONSTRAINT bets_play_mode_check CHECK (
        play_mode IS NULL OR play_mode IN (
            'guess', 'dodge', 'road',
            'direct', 'up_down', 'left_right', 'odd_even', 'winner'
        )
    );
