package retryx

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestDoAttempts(t *testing.T) {
	t.Run("zero attempts tries once", func(t *testing.T) {
		calls := 0
		err := Do(t.Context(), config.Retry{}, func(int) (bool, time.Duration, error) {
			calls++
			return true, 0, errors.New("nope")
		})
		require.EqualError(t, err, "nope")
		require.Equal(t, 1, calls)
	})

	t.Run("stops on success", func(t *testing.T) {
		calls := 0
		err := Do(t.Context(), config.Retry{Attempts: 4}, func(attempt int) (bool, time.Duration, error) {
			calls++
			require.Equal(t, calls, attempt)
			if attempt == 2 {
				return false, 0, nil
			}
			return true, 0, errors.New("later")
		})
		require.NoError(t, err)
		require.Equal(t, 2, calls)
	})

	t.Run("does not retry permanent errors", func(t *testing.T) {
		calls := 0
		err := Do(t.Context(), config.Retry{Attempts: 5}, func(int) (bool, time.Duration, error) {
			calls++
			return false, 0, errors.New("permanent")
		})
		require.EqualError(t, err, "permanent")
		require.Equal(t, 1, calls)
	})

	t.Run("returns the last error", func(t *testing.T) {
		calls := 0
		err := Do(t.Context(), config.Retry{Attempts: 3}, func(attempt int) (bool, time.Duration, error) {
			calls++
			return true, 0, fmt.Errorf("try %d", attempt)
		})
		require.EqualError(t, err, "try 3")
		require.Equal(t, 3, calls)
	})
}

func TestDoContext(t *testing.T) {
	t.Run("canceled before the first try", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		calls := 0
		err := Do(ctx, config.Retry{Attempts: 4}, func(int) (bool, time.Duration, error) {
			calls++
			return true, 0, errors.New("nope")
		})
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, context.Canceled, err)
		require.Equal(t, 0, calls)
	})

	t.Run("canceled during the wait", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		calls := 0
		started := make(chan struct{})
		go func() {
			<-started
			cancel()
		}()
		start := time.Now()
		err := Do(ctx, config.Retry{Attempts: 4, Delay: time.Hour}, func(int) (bool, time.Duration, error) {
			calls++
			if calls == 1 {
				close(started)
			}
			return true, 0, errors.New("later")
		})
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, context.Canceled, err)
		require.Equal(t, 1, calls)
		require.Less(t, time.Since(start), 2*time.Second)
	})

	t.Run("context error from the operation is not retried", func(t *testing.T) {
		calls := 0
		err := Do(t.Context(), config.Retry{Attempts: 4}, func(int) (bool, time.Duration, error) {
			calls++
			return true, 0, context.DeadlineExceeded
		})
		require.Equal(t, context.DeadlineExceeded, err)
		require.Equal(t, 1, calls)
	})
}

func TestWait(t *testing.T) {
	cfg := config.Retry{Delay: 100 * time.Millisecond, MaxDelay: 300 * time.Millisecond}
	require.Equal(t, 100*time.Millisecond, Wait(cfg, 1, 0))
	require.Equal(t, 200*time.Millisecond, Wait(cfg, 2, 0))
	require.Equal(t, 300*time.Millisecond, Wait(cfg, 3, 0))
	require.Equal(t, 300*time.Millisecond, Wait(cfg, 1, time.Second))
	require.Equal(t, time.Second, Wait(config.Retry{Delay: 100 * time.Millisecond}, 1, time.Second))
	require.Equal(t, 50*time.Millisecond, Wait(config.Retry{Delay: time.Second, MaxDelay: 50 * time.Millisecond}, 1, 0))
	require.Equal(t, time.Duration(0), Wait(config.Retry{}, 3, 0))
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	delay, ok := ParseRetryAfter("120", now)
	require.True(t, ok)
	require.Equal(t, 120*time.Second, delay)

	delay, ok = ParseRetryAfter("  15 ", now)
	require.True(t, ok)
	require.Equal(t, 15*time.Second, delay)

	_, ok = ParseRetryAfter("", now)
	require.False(t, ok)
	_, ok = ParseRetryAfter("soon", now)
	require.False(t, ok)
	_, ok = ParseRetryAfter("-1", now)
	require.False(t, ok)

	delay, ok = ParseRetryAfter(now.Add(-time.Minute).Format(http.TimeFormat), now)
	require.True(t, ok)
	require.Equal(t, time.Duration(0), delay)

	delay, ok = ParseRetryAfter(now.Add(45*time.Second).Format(http.TimeFormat), now)
	require.True(t, ok)
	require.Equal(t, 45*time.Second, delay)
}

type timeoutError struct {
	timeout   bool
	temporary bool
}

func (e timeoutError) Error() string   { return "net" }
func (e timeoutError) Timeout() bool   { return e.timeout }
func (e timeoutError) Temporary() bool { return e.temporary }

func TestIsTransient(t *testing.T) {
	require.False(t, IsTransient(nil))
	require.False(t, IsTransient(errors.New("nope")))
	require.False(t, IsTransient(context.Canceled))
	require.False(t, IsTransient(context.DeadlineExceeded))
	require.False(t, IsTransient(fmt.Errorf("wrap: %w", context.Canceled)))
	require.False(t, IsTransient(timeoutError{}))
	require.True(t, IsTransient(timeoutError{timeout: true}))
	require.True(t, IsTransient(timeoutError{temporary: true}))
	require.True(t, IsTransient(fmt.Errorf("wrap: %w", timeoutError{timeout: true})))
}
