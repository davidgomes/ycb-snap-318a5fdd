package retry

import (
	"testing"
	"time"

	stdctx "context"

	"github.com/goreleaser/goreleaser/v2/internal/yaml"
	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestNextDelay(t *testing.T) {
	cfg := config.Retry{Delay: time.Second, MaxDelay: 2500 * time.Millisecond}
	require.Equal(t, time.Second, NextDelay(cfg, 1, 0, false))
	require.Equal(t, 2*time.Second, NextDelay(cfg, 2, 0, false))
	require.Equal(t, 2500*time.Millisecond, NextDelay(cfg, 3, 0, false))

	// Retry-After wins over backoff, then max_delay caps the wait.
	require.Equal(t, 2500*time.Millisecond, NextDelay(cfg, 1, 5*time.Second, true))

	// A shorter Retry-After does not shrink the exponential wait.
	require.Equal(t, 2*time.Second, NextDelay(cfg, 2, 100*time.Millisecond, true))

	uncapped := config.Retry{Delay: time.Second}
	require.Equal(t, 8*time.Second, NextDelay(uncapped, 4, 0, false))
	require.Equal(t, 9*time.Second, NextDelay(uncapped, 1, 9*time.Second, true))
	require.Equal(t, time.Duration(0), NextDelay(config.Retry{}, 3, 0, false))
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	d, ok := ParseRetryAfter("120", now)
	require.True(t, ok)
	require.Equal(t, 120*time.Second, d)

	d, ok = ParseRetryAfter("  15 ", now)
	require.True(t, ok)
	require.Equal(t, 15*time.Second, d)

	d, ok = ParseRetryAfter("Wed, 23 Sep 2026 12:00:30 GMT", now)
	require.True(t, ok)
	require.Equal(t, 30*time.Second, d)

	d, ok = ParseRetryAfter("Wed, 23 Sep 2026 11:00:00 GMT", now)
	require.True(t, ok)
	require.Equal(t, time.Duration(0), d)

	_, ok = ParseRetryAfter("not-a-date", now)
	require.False(t, ok)
	_, ok = ParseRetryAfter("-1", now)
	require.False(t, ok)
	_, ok = ParseRetryAfter("", now)
	require.False(t, ok)
}

func TestSleepContext(t *testing.T) {
	ctx, cancel := stdctx.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, Sleep(ctx, time.Hour), stdctx.Canceled)

	ctx, cancel = stdctx.WithCancel(t.Context())
	defer cancel()
	require.NoError(t, Sleep(ctx, 0))
}

func TestRetryConfigYAML(t *testing.T) {
	const doc = `
uploads:
  - name: production
    target: https://example.com/upload
    retry:
      attempts: 4
      delay: 2s
      max_delay: 30s
artifactories:
  - name: art
    target: https://example.com/artifactory
    retry:
      attempts: 2
      delay: 250ms
      max_delay: 1s
blobs:
  - provider: s3
    bucket: '{{ .ProjectName }}'
    retry:
      attempts: 3
      delay: 1s
      max_delay: 5s
`
	var project config.Project
	require.NoError(t, yaml.UnmarshalStrict([]byte(doc), &project))
	require.Equal(t, config.Retry{Attempts: 4, Delay: 2 * time.Second, MaxDelay: 30 * time.Second}, project.Uploads[0].Retry)
	require.Equal(t, config.Retry{Attempts: 2, Delay: 250 * time.Millisecond, MaxDelay: time.Second}, project.Artifactories[0].Retry)
	require.Equal(t, config.Retry{Attempts: 3, Delay: time.Second, MaxDelay: 5 * time.Second}, project.Blobs[0].Retry)
}
