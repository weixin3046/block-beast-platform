package identity

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (repository *PostgresRepository) LoginBlocked(ctx context.Context, loginName string, now time.Time) (bool, error) {
	var blocked bool
	err := repository.pool.QueryRow(ctx, `
		SELECT COALESCE(locked_until > $2, false)
		FROM auth_login_guards
		WHERE login_name=$1`, loginName, now).Scan(&blocked)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return blocked, err
}

// RecordLoginFailure serializes updates by login name with an advisory
// transaction lock, so limits remain correct across multiple API instances.
func (repository *PostgresRepository) RecordLoginFailure(
	ctx context.Context,
	loginName string,
	now time.Time,
	maxFailures int,
	window time.Duration,
	lockout time.Duration,
) (bool, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, loginName); err != nil {
		return false, err
	}
	var failures int
	var windowStarted time.Time
	var lockedUntil *time.Time
	err = tx.QueryRow(ctx, `
		SELECT failed_attempts, window_started_at, locked_until
		FROM auth_login_guards
		WHERE login_name=$1
		FOR UPDATE`, loginName).Scan(&failures, &windowStarted, &lockedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		failures = 0
		windowStarted = now
		lockedUntil = nil
	} else if err != nil {
		return false, err
	}
	if (lockedUntil != nil && !lockedUntil.After(now)) || now.Sub(windowStarted) >= window {
		failures = 0
		windowStarted = now
		lockedUntil = nil
	}
	failures++
	blocked := lockedUntil != nil && lockedUntil.After(now)
	if failures >= maxFailures {
		until := now.Add(lockout)
		lockedUntil = &until
		blocked = true
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO auth_login_guards (
			login_name, failed_attempts, window_started_at, locked_until, updated_at
		) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (login_name) DO UPDATE SET
			failed_attempts=EXCLUDED.failed_attempts,
			window_started_at=EXCLUDED.window_started_at,
			locked_until=EXCLUDED.locked_until,
			updated_at=EXCLUDED.updated_at`,
		loginName, failures, windowStarted, lockedUntil, now)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return blocked, nil
}

func (repository *PostgresRepository) ClearLoginFailures(ctx context.Context, loginName string) error {
	_, err := repository.pool.Exec(ctx, `DELETE FROM auth_login_guards WHERE login_name=$1`, loginName)
	return err
}
