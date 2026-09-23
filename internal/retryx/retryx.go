// Package retryx retries an operation with exponential backoff.
package retryx

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/goreleaser/goreleaser/v2/pkg/config"
)

// Do calls fn until it succeeds, the error cannot be retried, the context is
// canceled, or the attempt budget is spent.
//
// fn is invoked with a 1-based attempt number. It reports whether a failure
// should be retried and an optional extra delay, such as an HTTP Retry-After.
// The wait before the next attempt is max(exponential backoff, extra delay),
// then capped by cfg.MaxDelay when that cap is set.
//
// A zero Attempts value tries exactly once. Context cancellation stops the
// loop and returns the context error.
func Do(ctx context.Context, cfg config.Retry, fn func(attempt int) (retriable bool, retryAfter time.Duration, err error)) error {
	total := cfg.Attempts
	if total == 0 {
		total = 1
	}

	var err error
	for attempt := 1; attempt <= int(total); attempt++ {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}

		var retriable bool
		var retryAfter time.Duration
		retriable, retryAfter, err = fn(attempt)
		if err == nil {
			return nil
		}
		if IsContext(err) {
			if cerr := ctx.Err(); cerr != nil {
				return cerr
			}
			return err
		}
		if !retriable || attempt == int(total) {
			return err
		}
		if serr := sleep(ctx, Wait(cfg, attempt, retryAfter)); serr != nil {
			return serr
		}
	}
	return err
}

// Wait is the delay that follows a failed attempt.
// attempt is 1-based. retryAfter is included only when the caller already
// decided the failure honors it.
func Wait(cfg config.Retry, attempt int, retryAfter time.Duration) time.Duration {
	wait := Backoff(cfg.Delay, attempt)
	if retryAfter > wait {
		wait = retryAfter
	}
	if cfg.MaxDelay > 0 && wait > cfg.MaxDelay {
		return cfg.MaxDelay
	}
	return wait
}

// Backoff is delay * 2^(attempt-1). A non-positive delay waits nothing.
// The result saturates at the maximum duration instead of overflowing.
func Backoff(delay time.Duration, attempt int) time.Duration {
	if delay <= 0 || attempt <= 0 {
		return 0
	}
	shift := attempt - 1
	if shift >= 62 {
		return time.Duration(math.MaxInt64)
	}
	if delay > time.Duration(math.MaxInt64)>>shift {
		return time.Duration(math.MaxInt64)
	}
	return delay << shift
}

// IsContext reports whether err is context cancellation or deadline expiry.
func IsContext(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// IsTransient reports whether err, or an error it wraps, implements
// Timeout() bool or Temporary() bool and returns true.
// Context cancellation is never transient.
func IsTransient(err error) bool {
	if err == nil || IsContext(err) {
		return false
	}
	for err != nil {
		if IsContext(err) {
			return false
		}
		if t, ok := err.(interface{ Timeout() bool }); ok && t.Timeout() {
			return true
		}
		if t, ok := err.(interface{ Temporary() bool }); ok && t.Temporary() {
			return true
		}
		err = errors.Unwrap(err)
	}
	return false
}

// ParseRetryAfter parses an HTTP Retry-After value at time now.
// Valid forms are delta-seconds and the HTTP date formats accepted by
// net/http.ParseTime. The boolean is false when the header is missing or invalid.
// A valid date in the past yields a zero duration.
func ParseRetryAfter(header string, now time.Time) (time.Duration, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0, false
	}
	if secs, err := strconv.ParseInt(header, 10, 64); err == nil {
		if secs < 0 {
			return 0, false
		}
		const maxSeconds = int64(math.MaxInt64 / int64(time.Second))
		if secs > maxSeconds {
			return time.Duration(math.MaxInt64), true
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

func sleep(ctx context.Context, d time.Duration) error {
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
