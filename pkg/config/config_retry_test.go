package config

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPublisherRetry(t *testing.T) {
	t.Parallel()

	prop, err := LoadReader(strings.NewReader(`
uploads:
  - name: up
    retry:
      attempts: 3
      delay: 1s
      max_delay: 30s
artifactories:
  - name: art
    retry:
      attempts: 4
      delay: 500ms
blobs:
  - provider: s3
    bucket: foo
    retry:
      attempts: 5
      max_delay: 1m
  - provider: gs
    bucket: bar
`))
	require.NoError(t, err)

	require.Equal(t, Retry{Attempts: 3, Delay: time.Second, MaxDelay: 30 * time.Second}, prop.Uploads[0].Retry)
	require.Equal(t, Retry{Attempts: 4, Delay: 500 * time.Millisecond}, prop.Artifactories[0].Retry)
	require.Equal(t, Retry{Attempts: 5, MaxDelay: time.Minute}, prop.Blobs[0].Retry)
	require.Equal(t, Retry{}, prop.Blobs[1].Retry)
}
