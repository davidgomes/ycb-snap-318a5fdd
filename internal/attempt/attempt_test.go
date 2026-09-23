package attempt

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestExponentialBackoffDoesNotOverflow(t *testing.T) {
	t.Parallel()
	got := exponentialBackoff(time.Duration(1<<61), 10)
	require.Positive(t, int64(got))
}

func TestWaitDuration(t *testing.T) {
	t.Parallel()
	delay := 10 * time.Millisecond
	require.Equal(t, delay, waitDuration(delay, 0, 1, 0))
	require.Equal(t, 20*time.Millisecond, waitDuration(delay, 0, 2, 0))
	require.Equal(t, 40*time.Millisecond, waitDuration(delay, 0, 3, 0))
	require.Equal(t, 30*time.Millisecond, waitDuration(delay, 30*time.Millisecond, 3, 0))
	require.Equal(t, 50*time.Millisecond, waitDuration(delay, 0, 1, 50*time.Millisecond))
	require.Equal(t, 25*time.Millisecond, waitDuration(delay, 25*time.Millisecond, 1, time.Second))
	require.Equal(t, time.Duration(0), waitDuration(0, 0, 1, 0))
	require.Equal(t, delay, waitDuration(delay, time.Second, 1, 0))
}

func TestDoCancelDuringWait(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	var tries int
	done := make(chan error, 1)
	go func() {
		done <- Do(ctx, config.Retry{Attempts: 4, Delay: time.Hour}, func() error {
			tries++
			if tries == 1 {
				cancel()
			}
			return Retriable(errors.New("busy"), 0)
		}, nil)
	}()
	// Cancel is invoked inside the first try, before the wait. Do checks the
	// context at the start of the next iteration and also when sleeping.
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, 1, tries)
	case <-time.After(2 * time.Second):
		t.Fatal("retry did not stop after cancel")
	}
}

func TestDoPermanent(t *testing.T) {
	t.Parallel()
	var tries int
	err := Do(t.Context(), config.Retry{Attempts: 4, Delay: time.Millisecond}, func() error {
		tries++
		return errors.New("nope")
	}, nil)
	require.EqualError(t, err, "nope")
	require.Equal(t, 1, tries)
}

func TestDoZeroAttemptsIsOne(t *testing.T) {
	t.Parallel()
	var tries int
	err := Do(t.Context(), config.Retry{}, func() error {
		tries++
		return errors.New("nope")
	}, nil)
	require.EqualError(t, err, "nope")
	require.Equal(t, 1, tries)
}

func TestDoSuccessOnSecond(t *testing.T) {
	t.Parallel()
	var got []error
	err := Do(t.Context(), config.Retry{Attempts: 3, Delay: time.Millisecond, MaxDelay: time.Millisecond}, func() error {
		if len(got) == 0 {
			return Retriable(errors.New("later"), 0)
		}
		return nil
	}, func(_ int, err error) {
		got = append(got, err)
	})
	require.NoError(t, err)
	require.EqualError(t, got[0], "later")
	require.NoError(t, got[1])
}
