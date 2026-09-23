package retry

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/stretchr/testify/require"
)

var errRetriable = errors.New("retriable")

func always(error) (bool, time.Duration) { return true, 0 }

func onlyRetriable(err error) (bool, time.Duration) {
	return errors.Is(err, errRetriable), 0
}

func TestDo(t *testing.T) {
	t.Run("no retry config makes a single attempt", func(t *testing.T) {
		var got []int
		err := Do(t.Context(), config.Retry{}, always, func(attempt int) error {
			got = append(got, attempt)
			return errRetriable
		})
		require.ErrorIs(t, err, errRetriable)
		require.Equal(t, []int{1}, got)
	})

	t.Run("succeeds first try", func(t *testing.T) {
		var got []int
		err := Do(t.Context(), config.Retry{Attempts: 5}, always, func(attempt int) error {
			got = append(got, attempt)
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, []int{1}, got)
	})

	t.Run("retries until success", func(t *testing.T) {
		var got []int
		err := Do(t.Context(), config.Retry{Attempts: 5}, always, func(attempt int) error {
			got = append(got, attempt)
			if attempt < 3 {
				return errRetriable
			}
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, []int{1, 2, 3}, got)
	})

	t.Run("gives up after all attempts", func(t *testing.T) {
		var got []int
		err := Do(t.Context(), config.Retry{Attempts: 3}, always, func(attempt int) error {
			got = append(got, attempt)
			return errRetriable
		})
		require.ErrorIs(t, err, errRetriable)
		require.Equal(t, []int{1, 2, 3}, got)
	})

	t.Run("does not retry non retriable errors", func(t *testing.T) {
		errFatal := errors.New("fatal")
		var got []int
		err := Do(t.Context(), config.Retry{Attempts: 3}, onlyRetriable, func(attempt int) error {
			got = append(got, attempt)
			if attempt == 2 {
				return errFatal
			}
			return errRetriable
		})
		require.ErrorIs(t, err, errFatal)
		require.Equal(t, []int{1, 2}, got)
	})

	t.Run("waits between attempts", func(t *testing.T) {
		start := time.Now()
		err := Do(t.Context(), config.Retry{
			Attempts: 3,
			Delay:    20 * time.Millisecond,
		}, always, func(int) error {
			return errRetriable
		})
		require.ErrorIs(t, err, errRetriable)
		require.GreaterOrEqual(t, time.Since(start), 60*time.Millisecond)
	})

	t.Run("honors the wait from the classifier", func(t *testing.T) {
		start := time.Now()
		err := Do(t.Context(), config.Retry{Attempts: 2}, func(error) (bool, time.Duration) {
			return true, 50 * time.Millisecond
		}, func(int) error {
			return errRetriable
		})
		require.ErrorIs(t, err, errRetriable)
		require.GreaterOrEqual(t, time.Since(start), 50*time.Millisecond)
	})

	t.Run("max delay caps the wait from the classifier", func(t *testing.T) {
		start := time.Now()
		err := Do(t.Context(), config.Retry{
			Attempts: 2,
			MaxDelay: time.Millisecond,
		}, func(error) (bool, time.Duration) {
			return true, time.Hour
		}, func(int) error {
			return errRetriable
		})
		require.ErrorIs(t, err, errRetriable)
		require.Less(t, time.Since(start), time.Minute)
	})

	t.Run("context canceled while waiting", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		var got []int
		err := Do(ctx, config.Retry{Attempts: 5, Delay: time.Hour}, always, func(attempt int) error {
			got = append(got, attempt)
			cancel()
			return errRetriable
		})
		require.ErrorIs(t, err, context.Canceled)
		require.NotErrorIs(t, err, errRetriable)
		require.Equal(t, []int{1}, got)
	})

	t.Run("context canceled without delay", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		var got []int
		err := Do(ctx, config.Retry{Attempts: 5}, always, func(attempt int) error {
			got = append(got, attempt)
			if attempt == 2 {
				cancel()
			}
			return errRetriable
		})
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, []int{1, 2}, got)
	})

	t.Run("context deadline exceeded", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
		defer cancel()
		err := Do(ctx, config.Retry{Attempts: 5, Delay: time.Hour}, always, func(int) error {
			return errRetriable
		})
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("success on a canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		require.NoError(t, Do(ctx, config.Retry{Attempts: 5}, always, func(int) error {
			return nil
		}))
	})
}

func TestDelay(t *testing.T) {
	for name, tt := range map[string]struct {
		cfg     config.Retry
		attempt int
		wait    time.Duration
		expect  time.Duration
	}{
		"no delay": {
			cfg:     config.Retry{},
			attempt: 3,
			expect:  0,
		},
		"first retry": {
			cfg:     config.Retry{Delay: time.Second},
			attempt: 1,
			expect:  time.Second,
		},
		"exponential": {
			cfg:     config.Retry{Delay: time.Second},
			attempt: 4,
			expect:  8 * time.Second,
		},
		"capped": {
			cfg:     config.Retry{Delay: time.Second, MaxDelay: 5 * time.Second},
			attempt: 4,
			expect:  5 * time.Second,
		},
		"wait bigger than backoff": {
			cfg:     config.Retry{Delay: time.Second},
			attempt: 2,
			wait:    10 * time.Second,
			expect:  10 * time.Second,
		},
		"backoff bigger than wait": {
			cfg:     config.Retry{Delay: time.Second},
			attempt: 3,
			wait:    time.Second,
			expect:  4 * time.Second,
		},
		"wait capped": {
			cfg:     config.Retry{Delay: time.Second, MaxDelay: 3 * time.Second},
			attempt: 1,
			wait:    time.Minute,
			expect:  3 * time.Second,
		},
		"negative delay": {
			cfg:     config.Retry{Delay: -time.Second},
			attempt: 2,
			expect:  0,
		},
		"overflow": {
			cfg:     config.Retry{Delay: time.Hour},
			attempt: 200,
			expect:  math.MaxInt64,
		},
		"overflow capped": {
			cfg:     config.Retry{Delay: time.Hour, MaxDelay: 2 * time.Hour},
			attempt: 200,
			expect:  2 * time.Hour,
		},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tt.expect, delay(tt.cfg, tt.attempt, tt.wait))
		})
	}
}
