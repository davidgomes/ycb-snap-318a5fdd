// Package retry implements the retry policy used by publishers.
package retry

import (
	"context"
	"math"
	"time"

	"github.com/goreleaser/goreleaser/v2/pkg/config"
)

// Classifier tells whether err is worth retrying, and the minimum time to
// wait before doing so (e.g. from a Retry-After header).
type Classifier func(err error) (retry bool, wait time.Duration)

// Do calls fn, passing the 1-based attempt number, until it succeeds, it fails
// with an error that classify deems not retriable, or cfg.Attempts attempts
// were made (at least one attempt is always made).
//
// Before each retry, it waits for the larger of the exponential backoff
// (cfg.Delay, doubled on every retry) and the wait returned by classify,
// capped by cfg.MaxDelay.
//
// If ctx is done, it stops retrying and returns the context error.
func Do(ctx context.Context, cfg config.Retry, classify Classifier, fn func(attempt int) error) error {
	attempts := max(cfg.Attempts, 1)
	for attempt := 1; ; attempt++ {
		err := fn(attempt)
		if err == nil {
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		retry, wait := classify(err)
		if !retry || uint(attempt) >= attempts {
			return err
		}
		if err := sleep(ctx, delay(cfg, attempt, wait)); err != nil {
			return err
		}
	}
}

// delay returns how long to wait after the given failed attempt.
func delay(cfg config.Retry, attempt int, wait time.Duration) time.Duration {
	d := max(cfg.Delay, 0)
	for i := 1; i < attempt && d > 0; i++ {
		if d > math.MaxInt64/2 {
			d = math.MaxInt64
			break
		}
		d *= 2
	}
	d = max(d, wait)
	if cfg.MaxDelay > 0 {
		d = min(d, cfg.MaxDelay)
	}
	return d
}

func sleep(ctx context.Context, d time.Duration) error {
	if d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
		case <-t.C:
		}
	}
	return ctx.Err()
}
