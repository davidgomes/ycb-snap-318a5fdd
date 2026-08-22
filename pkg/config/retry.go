package config

import (
	"context"
	"time"
)

// MaxAttempts returns how many times an operation should be tried.
// A zero value means a single attempt.
func (r Retry) MaxAttempts() uint {
	if r.Attempts == 0 {
		return 1
	}
	return r.Attempts
}

// Interval is the wait after a failed attempt (1-based), using exponential
// backoff, optionally raised to retryAfter, then capped by MaxDelay.
func (r Retry) Interval(failedAttempt uint, retryAfter time.Duration) time.Duration {
	wait := r.backoff(failedAttempt)
	if retryAfter > wait {
		wait = retryAfter
	}
	if r.MaxDelay > 0 && wait > r.MaxDelay {
		return r.MaxDelay
	}
	return wait
}

func (r Retry) backoff(failedAttempt uint) time.Duration {
	if r.Delay <= 0 || failedAttempt == 0 {
		return 0
	}
	n := min(failedAttempt-1, 62)
	return r.Delay * (1 << n)
}

// Sleep waits Interval, or returns ctx.Err() if the context is done first.
func (r Retry) Sleep(ctx context.Context, failedAttempt uint, retryAfter time.Duration) error {
	wait := r.Interval(failedAttempt, retryAfter)
	if wait <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
