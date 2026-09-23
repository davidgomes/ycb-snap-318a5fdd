// Package attempt retries a single publish operation with exponential backoff.
package attempt

import (
	"context"
	"errors"
	"math"
	"math/bits"
	"time"

	"github.com/goreleaser/goreleaser/v2/pkg/config"
)

type retryAfter interface {
	RetryAfter() time.Duration
}

type retriableError struct {
	err   error
	after time.Duration
}

func (e *retriableError) Error() string             { return e.err.Error() }
func (e *retriableError) Unwrap() error             { return e.err }
func (e *retriableError) RetryAfter() time.Duration { return e.after }

// Retriable marks err as safe to retry.
//
// after is a minimum wait (for example Retry-After). The actual sleep is
// max(exponential backoff, after), then capped by max_delay.
func Retriable(err error, after time.Duration) error {
	if err == nil {
		return nil
	}
	return &retriableError{err: err, after: after}
}

// Do calls fn until it succeeds, the error is not retriable, the context is
// canceled, or Attempts is exhausted.
//
// Attempts of 0 publishes once. onAttempt, when set, receives every try.
// A nil error means that try succeeded. Context cancellation stops the loop
// and returns the context error.
func Do(ctx context.Context, cfg config.Retry, fn func() error, onAttempt func(attempt int, err error)) error {
	attempts := cfg.Attempts
	if attempts == 0 {
		attempts = 1
	}

	var lastErr error
	for n := 1; n <= int(attempts); n++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		err := fn()
		if onAttempt != nil {
			onAttempt(n, err)
		}
		if err == nil {
			return nil
		}
		lastErr = err

		if cerr := canceled(ctx, err); cerr != nil {
			return cerr
		}

		var ra retryAfter
		if !errors.As(err, &ra) || n == int(attempts) {
			return err
		}

		if err := sleep(ctx, waitDuration(cfg.Delay, cfg.MaxDelay, n, ra.RetryAfter())); err != nil {
			return err
		}
	}
	return lastErr
}

func canceled(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if cerr := ctx.Err(); cerr != nil && errors.Is(err, cerr) {
		return cerr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		return err
	}
	return nil
}

// waitDuration is max(exponential backoff, retryAfter), capped by maxDelay.
//
// attempt is the 1-based index of the try that just failed. The first wait is
// delay, then delay*2, delay*4, and so on. maxDelay <= 0 disables the cap.
func waitDuration(delay, maxDelay time.Duration, attempt int, retryAfter time.Duration) time.Duration {
	wait := exponentialBackoff(delay, attempt)
	if retryAfter > wait {
		wait = retryAfter
	}
	if maxDelay > 0 && wait > maxDelay {
		wait = maxDelay
	}
	if wait < 0 {
		return 0
	}
	return wait
}

func exponentialBackoff(delay time.Duration, attempt int) time.Duration {
	if delay <= 0 || attempt <= 0 {
		return 0
	}
	shift := attempt - 1
	// Keep delay<<shift inside int64. bits.Len64(delay) is the index of the
	// highest set bit plus one, so the remaining magnitude is 63-len, and we
	// stay at or below bit 62.
	maxShift := 62 - bits.Len64(uint64(delay))
	if maxShift < 0 {
		maxShift = 0
	}
	if shift > maxShift {
		shift = maxShift
	}
	scaled := delay << shift
	if scaled <= 0 {
		return time.Duration(math.MaxInt64)
	}
	return scaled
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
