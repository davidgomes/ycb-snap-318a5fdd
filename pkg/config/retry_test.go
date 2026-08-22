package config

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRetryMaxAttempts(t *testing.T) {
	require.Equal(t, uint(1), Retry{}.MaxAttempts())
	require.Equal(t, uint(3), Retry{Attempts: 3}.MaxAttempts())
}

func TestRetryInterval(t *testing.T) {
	r := Retry{Delay: time.Second, MaxDelay: 3 * time.Second}
	require.Equal(t, time.Second, r.Interval(1, 0))
	require.Equal(t, 2*time.Second, r.Interval(2, 0))
	require.Equal(t, 3*time.Second, r.Interval(3, 0))
	require.Equal(t, 3*time.Second, r.Interval(1, 10*time.Second))
	require.Equal(t, 2*time.Second, r.Interval(1, 2*time.Second))
}

func TestRetrySleepCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := Retry{Delay: time.Hour}.Sleep(ctx, 1, 0)
	require.ErrorIs(t, err, context.Canceled)
}
