// Package retry runs an operation with exponential backoff.
package retry

import (
	"context"
	"errors"
	"time"

	"github.com/goreleaser/goreleaser/v2/pkg/config"
)

// Do calls fn until it succeeds, the error is not retriable, attempts are
// exhausted, or ctx is canceled. attempt is 1-based.
//
// The wait after a failed attempt is wait(attempt, err), then capped by
// cfg.MaxDelay when that value is positive.
func Do(ctx context.Context, cfg config.Retry, fn func(attempt int) error, retriable func(error) bool, wait func(attempt int, err error) time.Duration) error {
	attempts := cfg.Attempts
	if attempts == 0 {
		attempts = 1
	}
	var err error
	for attempt := 1; attempt <= int(attempts); attempt++ {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		err = fn(attempt)
		if err == nil {
			return nil
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if attempt == int(attempts) || retriable == nil || !retriable(err) {
			return err
		}
		delay := time.Duration(0)
		if wait != nil {
			delay = wait(attempt, err)
		}
		delay = Cap(delay, cfg.MaxDelay)
		if serr := Sleep(ctx, delay); serr != nil {
			return serr
		}
	}
	return err
}

// Cap limits d to max when max is positive.
func Cap(d, max time.Duration) time.Duration {
	if max > 0 && d > max {
		return max
	}
	return d
}

// Backoff is delay * 2^(attempt-1) for a 1-based attempt number.
func Backoff(delay time.Duration, attempt int) time.Duration {
	if delay <= 0 || attempt <= 1 {
		if delay < 0 {
			return 0
		}
		if attempt <= 1 {
			return delay
		}
		return 0
	}
	d := delay
	for i := 1; i < attempt; i++ {
		if d > time.Duration(1<<62)/2 {
			return time.Duration(1 << 62)
		}
		d *= 2
	}
	return d
}

// Sleep waits for d or until ctx is canceled.
func Sleep(ctx context.Context, d time.Duration) error {
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	if d <= 0 {
		return nil
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
