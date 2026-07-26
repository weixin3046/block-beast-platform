package auth

import (
	"context"
	"errors"
	"time"
)

var ErrTooManyLoginAttempts = errors.New("too many login attempts; try again later")

type LoginProtectionPolicy struct {
	MaxFailures int
	Window      time.Duration
	Lockout     time.Duration
}

func (policy LoginProtectionPolicy) Validate() error {
	if policy.MaxFailures <= 0 {
		return errors.New("login protection max failures must be positive")
	}
	if policy.Window <= 0 || policy.Lockout <= 0 {
		return errors.New("login protection durations must be positive")
	}
	return nil
}

type LoginAttemptStore interface {
	LoginBlocked(ctx context.Context, loginName string, now time.Time) (bool, error)
	RecordLoginFailure(ctx context.Context, loginName string, now time.Time, maxFailures int, window, lockout time.Duration) (bool, error)
	ClearLoginFailures(ctx context.Context, loginName string) error
}
