package publishretry

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestDelay(t *testing.T) {
	cfg := config.Retry{Delay: time.Second, MaxDelay: 5 * time.Second}
	require.Equal(t, time.Second, Delay(cfg, 1, 0))
	require.Equal(t, 2*time.Second, Delay(cfg, 2, 0))
	require.Equal(t, 4*time.Second, Delay(cfg, 3, 0))
	require.Equal(t, 5*time.Second, Delay(cfg, 4, 0))
	require.Equal(t, 5*time.Second, Delay(cfg, 200, 0))
	require.Equal(t, 3*time.Second, Delay(cfg, 1, 3*time.Second))
	require.Equal(t, 2*time.Second, Delay(cfg, 2, time.Second))
	require.Equal(t, 5*time.Second, Delay(cfg, 1, time.Hour))
	require.Equal(t, time.Hour, Delay(config.Retry{Delay: time.Second}, 1, time.Hour))
	require.Equal(t, time.Duration(0), Delay(config.Retry{}, 3, 0))
}

type retryAfterErr struct{ d time.Duration }

func (e retryAfterErr) Error() string                     { return "retry after" }
func (e retryAfterErr) RetryAfter() (time.Duration, bool) { return e.d, true }

func TestDo(t *testing.T) {
	t.Run("success after retries", func(t *testing.T) {
		var calls []uint
		err := Do(t.Context(), config.Retry{Attempts: 3}, func(error) bool { return true }, func(attempt uint) error {
			calls = append(calls, attempt)
			if attempt < 3 {
				return errors.New("fail")
			}
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, []uint{1, 2, 3}, calls)
	})

	t.Run("exhausted", func(t *testing.T) {
		var calls int
		err := Do(t.Context(), config.Retry{Attempts: 2}, func(error) bool { return true }, func(uint) error {
			calls++
			return fmt.Errorf("fail %d", calls)
		})
		require.EqualError(t, err, "fail 2")
		require.Equal(t, 2, calls)
	})

	t.Run("zero attempts means one", func(t *testing.T) {
		var calls int
		err := Do(t.Context(), config.Retry{}, func(error) bool { return true }, func(uint) error {
			calls++
			return errors.New("fail")
		})
		require.Error(t, err)
		require.Equal(t, 1, calls)
	})

	t.Run("not retriable", func(t *testing.T) {
		var calls int
		err := Do(t.Context(), config.Retry{Attempts: 5}, func(error) bool { return false }, func(uint) error {
			calls++
			return errors.New("fail")
		})
		require.Error(t, err)
		require.Equal(t, 1, calls)
	})

	t.Run("retry after capped by max delay", func(t *testing.T) {
		start := time.Now()
		cfg := config.Retry{Attempts: 2, Delay: time.Millisecond, MaxDelay: 20 * time.Millisecond}
		err := Do(t.Context(), cfg, func(error) bool { return true }, func(attempt uint) error {
			if attempt == 1 {
				return retryAfterErr{time.Hour}
			}
			return nil
		})
		require.NoError(t, err)
		require.Less(t, time.Since(start), 5*time.Second)
	})

	t.Run("context canceled while waiting", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		var calls int
		err := Do(ctx, config.Retry{Attempts: 5, Delay: time.Hour}, func(error) bool { return true }, func(uint) error {
			calls++
			cancel()
			return errors.New("fail")
		})
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, 1, calls)
	})

	t.Run("context canceled before start", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err := Do(ctx, config.Retry{Attempts: 5}, func(error) bool { return true }, func(uint) error {
			t.Fatal("should not be called")
			return nil
		})
		require.ErrorIs(t, err, context.Canceled)
	})
}

type transient struct{ timeout, temporary bool }

func (e transient) Error() string   { return "transient" }
func (e transient) Timeout() bool   { return e.timeout }
func (e transient) Temporary() bool { return e.temporary }

func TestIsTransient(t *testing.T) {
	require.True(t, IsTransient(transient{timeout: true}))
	require.True(t, IsTransient(transient{temporary: true}))
	require.True(t, IsTransient(fmt.Errorf("wrapped: %w", transient{timeout: true})))
	require.False(t, IsTransient(transient{}))
	require.False(t, IsTransient(errors.New("nope")))
}
