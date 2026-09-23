package pubretry

import (
	stdctx "context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestBackoff(t *testing.T) {
	conf := config.Retry{Delay: time.Second, MaxDelay: 5 * time.Second}
	require.Equal(t, time.Second, Backoff(conf, 1, 0))
	require.Equal(t, 2*time.Second, Backoff(conf, 2, 0))
	require.Equal(t, 5*time.Second, Backoff(conf, 10, 0))
	require.Equal(t, 3*time.Second, Backoff(conf, 1, 3*time.Second))
	require.Equal(t, 5*time.Second, Backoff(conf, 1, time.Hour))
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resp := func(code int, v string) *http.Response {
		return &http.Response{StatusCode: code, Header: http.Header{"Retry-After": []string{v}}}
	}
	require.Equal(t, 7*time.Second, RetryAfter(resp(429, "7"), now))
	require.Equal(t, 10*time.Second, RetryAfter(resp(503, now.Add(10*time.Second).Format(http.TimeFormat)), now))
	require.Zero(t, RetryAfter(resp(500, "7"), now))
	require.Zero(t, RetryAfter(resp(429, "nope"), now))
}

type tempErr struct{}

func (tempErr) Error() string   { return "temp" }
func (tempErr) Temporary() bool { return true }

func TestDo(t *testing.T) {
	var got []int
	err := Do(t.Context(), config.Retry{Attempts: 3}, func(attempt int) Result {
		if attempt < 3 {
			return Result{Err: tempErr{}, Retryable: IsTransient(tempErr{})}
		}
		return Result{}
	}, func(attempt int, _ error) { got = append(got, attempt) })
	require.NoError(t, err)
	require.Equal(t, []int{1, 2, 3}, got)

	calls := 0
	err = Do(t.Context(), config.Retry{Attempts: 3}, func(int) Result {
		calls++
		return Result{Err: errors.New("fatal")}
	}, nil)
	require.EqualError(t, err, "fatal")
	require.Equal(t, 1, calls)

	ctx, cancel := stdctx.WithCancel(t.Context())
	err = Do(ctx, config.Retry{Attempts: 3, Delay: time.Hour}, func(int) Result {
		cancel()
		return Result{Err: tempErr{}, Retryable: true}
	}, nil)
	require.ErrorIs(t, err, stdctx.Canceled)
}
