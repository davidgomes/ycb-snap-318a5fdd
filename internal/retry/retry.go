// Package retry implements bounded exponential backoff shared by publishers.
package retry

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	stdctx "context"

	"github.com/goreleaser/goreleaser/v2/pkg/config"
)

// Sleep waits for the next retry. It is replaced in tests.
var Sleep = sleep

func sleep(ctx stdctx.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
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

// Count returns how many times an operation should be tried.
// An unset attempts value runs once.
func Count(cfg config.Retry) uint {
	if cfg.Attempts == 0 {
		return 1
	}
	return cfg.Attempts
}

// NextDelay is the wait after failedAttempt (1-based) before the next try.
// Exponential backoff starts at cfg.Delay and doubles after each failure.
// When hasRetryAfter is set, the wait is max(backoff, retryAfter), then capped
// by cfg.MaxDelay when that cap is positive.
func NextDelay(cfg config.Retry, failedAttempt int, retryAfter time.Duration, hasRetryAfter bool) time.Duration {
	wait := exponential(cfg.Delay, failedAttempt)
	if hasRetryAfter && retryAfter > wait {
		wait = retryAfter
	}
	if cfg.MaxDelay > 0 && wait > cfg.MaxDelay {
		wait = cfg.MaxDelay
	}
	if wait < 0 {
		return 0
	}
	return wait
}

func exponential(delay time.Duration, failedAttempt int) time.Duration {
	if delay <= 0 || failedAttempt <= 1 {
		if delay < 0 {
			return 0
		}
		return delay
	}
	shift := failedAttempt - 1
	d := delay
	for range shift {
		if d > math.MaxInt64/2 {
			return math.MaxInt64
		}
		d *= 2
	}
	return d
}

// ParseRetryAfter parses a Retry-After header value.
// Valid values are delta-seconds or an HTTP-date. Invalid values report false.
func ParseRetryAfter(header string, now time.Time) (time.Duration, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(header); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	when, err := http.ParseTime(header)
	if err != nil {
		return 0, false
	}
	d := when.Sub(now)
	if d < 0 {
		return 0, true
	}
	return d, true
}
