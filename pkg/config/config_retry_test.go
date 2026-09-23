package config

import (
	"testing"
	"time"

	"github.com/goreleaser/goreleaser/v2/internal/yaml"
	"github.com/stretchr/testify/require"
)

func TestRetryConfigYAML(t *testing.T) {
	var project Project
	err := yaml.UnmarshalStrict([]byte(`
uploads:
  - name: prod
    target: https://example.com/up
    retry:
      attempts: 4
      delay: 2s
      max_delay: 30s
artifactories:
  - name: art
    target: https://example.com/art
    retry:
      attempts: 2
      delay: 500ms
      max_delay: 5s
blobs:
  - provider: s3
    bucket: b
    retry:
      attempts: 3
      delay: 1s
      max_delay: 8s
`), &project)
	require.NoError(t, err)
	require.Equal(t, Retry{Attempts: 4, Delay: 2 * time.Second, MaxDelay: 30 * time.Second}, project.Uploads[0].Retry)
	require.Equal(t, Retry{Attempts: 2, Delay: 500 * time.Millisecond, MaxDelay: 5 * time.Second}, project.Artifactories[0].Retry)
	require.Equal(t, Retry{Attempts: 3, Delay: time.Second, MaxDelay: 8 * time.Second}, project.Blobs[0].Retry)
}
