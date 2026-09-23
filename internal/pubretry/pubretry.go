// Package pubretry implements retries with exponential backoff for publishers.
package pubretry

import (
	stdctx "context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/goreleaser/goreleaser/v2/pkg/config"
)

// Result is the outcome of a single attempt.
type Result struct {
	Err error
	// Retryable tells whether the error may be retried.
	Retryable bool
	// RetryAfter is a server-provided minimum wait, if any.
	RetryAfter time.Duration
}

// Do runs fn up to conf.Attempts times (at least once), waiting between
// attempts with exponential backoff capped by conf.MaxDelay.
// onAttempt is called after every attempt with its 1-based number and error.
func Do(ctx stdctx.Context, conf config.Retry, fn func(attempt int) Result, onAttempt func(attempt int, err error)) error {
	attempts := max(int(conf.Attempts), 1)
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		res := fn(attempt)
		if res.Err != nil && ctx.Err() != nil {
			res.Err = ctx.Err()
		}
		if onAttempt != nil {
			onAttempt(attempt, res.Err)
		}
		if res.Err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !res.Retryable || attempt >= attempts {
			return res.Err
		}
		wait := Backoff(conf, attempt, res.RetryAfter)
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

// Backoff returns the wait before the next attempt, given the attempt that
// just failed.
func Backoff(conf config.Retry, attempt int, retryAfter time.Duration) time.Duration {
	wait := conf.Delay
	for i := 1; i < attempt; i++ {
		if conf.MaxDelay > 0 && wait >= conf.MaxDelay {
			break
		}
		if wait > time.Duration(1<<62) {
			break
		}
		wait *= 2
	}
	wait = max(wait, retryAfter)
	if conf.MaxDelay > 0 {
		wait = min(wait, conf.MaxDelay)
	}
	return wait
}

// RetryableStatus reports whether an HTTP status code may be retried.
func RetryableStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}

// RetryAfter parses the Retry-After header of 429 and 503 responses.
func RetryAfter(resp *http.Response, now time.Time) time.Duration {
	if resp == nil {
		return 0
	}
	if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != http.StatusServiceUnavailable {
		return 0
	}
	v := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

// IsTransient reports whether err implements Timeout() or Temporary()
// returning true.
func IsTransient(err error) bool {
	var te interface{ Timeout() bool }
	if errors.As(err, &te) && te.Timeout() {
		return true
	}
	var tmp interface{ Temporary() bool }
	if errors.As(err, &tmp) && tmp.Temporary() {
		return true
	}
	return false
}
