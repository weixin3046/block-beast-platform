package game

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// ActivateScheduledRounds opens successors after settlement, independent of
// external block fetching or the slower maintenance polling interval.
func (r *PostgresRepository) ActivateScheduledRounds(ctx context.Context, now time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('game-round-scheduler',0))`); err != nil {
		return err
	}
	if err = activateScheduledRounds(ctx, tx, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func activateScheduledRounds(ctx context.Context, tx pgx.Tx, now time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE rounds r SET status='open',version=version+1
 WHERE r.status='scheduled' AND r.bet_closes_at>$1
 AND EXISTS(SELECT 1 FROM game_types gt WHERE gt.id=r.game_type_id AND gt.enabled)
 AND NOT EXISTS(SELECT 1 FROM rounds prior WHERE prior.game_type_id=r.game_type_id
 AND prior.sequence<r.sequence AND prior.status IN ('scheduled','open','closed','settling'))`, now)
	return err
}
