// Package publishretry implements the retry loop shared by publishers that
// support a [config.Retry] configuration.
package publishretry

import (
	"context"
	"errors"
	"time"

	"github.com/caarlos0/log"
	"github.com/goreleaser/goreleaser/v2/pkg/config"
)

// RetryAfterError is implemented by errors that carry a server provided
// minimum wait before the next attempt.
type RetryAfterError interface {
	error
	RetryAfter() (time.Duration, bool)
}

// Do calls fn until it succeeds, the configured attempts are exhausted, the
// error is not retriable, or ctx is done.
//
// fn receives the 1-based attempt number.
// If ctx is done, its error is returned.
func Do(ctx context.Context, cfg config.Retry, retriable func(error) bool, fn func(attempt uint) error) error {
	attempts := max(cfg.Attempts, 1)
	for attempt := uint(1); ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := fn(attempt)
		if err == nil {
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if attempt >= attempts || !retriable(err) {
			return err
		}

		var retryAfter time.Duration
		if rae, ok := errors.AsType[RetryAfterError](err); ok {
			retryAfter, _ = rae.RetryAfter()
		}
		wait := Delay(cfg, attempt, retryAfter)
		log.WithError(err).
			WithField("attempt", attempt).
			WithField("wait", wait.String()).
			Warn("retrying")

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Delay returns how long to wait after the given failed attempt: the
// exponential backoff (delay * 2^(attempt-1)), raised to retryAfter if it is
// larger, and capped by the configured max delay.
func Delay(cfg config.Retry, attempt uint, retryAfter time.Duration) time.Duration {
	wait := cfg.Delay
	for i := uint(1); i < attempt && wait > 0; i++ {
		if wait > (1<<62)/2 {
			wait = 1 << 62
			break
		}
		wait *= 2
	}
	wait = max(wait, retryAfter, 0)
	if cfg.MaxDelay > 0 {
		wait = min(wait, cfg.MaxDelay)
	}
	return wait
}

// IsTransient reports whether err, or any error it wraps, implements
// Timeout() bool or Temporary() bool returning true.
func IsTransient(err error) bool {
	if t, ok := errors.AsType[interface {
		error
		Timeout() bool
	}](err); ok && t.Timeout() {
		return true
	}
	if t, ok := errors.AsType[interface {
		error
		Temporary() bool
	}](err); ok && t.Temporary() {
		return true
	}
	return false
}
