package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRepo(t *testing.T) {
	require.Equal(
		t,
		"goreleaser/godownloader",
		Repo{Owner: "goreleaser", Name: "godownloader"}.String(),
	)
}

func TestEmptyRepoNameAndOwner(t *testing.T) {
	require.Empty(t, Repo{}.String())
}

func TestLoadRetryConfig(t *testing.T) {
	conf := `
version: 2
uploads:
  - name: production
    target: https://example.invalid/{{ .Version }}/
    retry:
      attempts: 4
      delay: 2s
      max_delay: 30s
artifactories:
  - name: corp
    target: https://artifactory.example/{{ .Version }}/
    retry:
      attempts: 3
      delay: 1s
      max_delay: 15s
blobs:
  - provider: s3
    bucket: '{{ .Env.BUCKET }}'
    retry:
      attempts: 5
      delay: 500ms
      max_delay: 1m
`
	prop, err := LoadReader(strings.NewReader(conf))
	require.NoError(t, err)
	require.Equal(t, Retry{Attempts: 4, Delay: 2 * time.Second, MaxDelay: 30 * time.Second}, prop.Uploads[0].Retry)
	require.Equal(t, Retry{Attempts: 3, Delay: time.Second, MaxDelay: 15 * time.Second}, prop.Artifactories[0].Retry)
	require.Equal(t, Retry{Attempts: 5, Delay: 500 * time.Millisecond, MaxDelay: time.Minute}, prop.Blobs[0].Retry)
}

func TestLoadReader(t *testing.T) {
	conf := `
nfpms:
  - homepage: http://goreleaser.github.io
`
	buf := strings.NewReader(conf)
	prop, err := LoadReader(buf)

	require.NoError(t, err)
	require.Equal(t, "http://goreleaser.github.io", prop.NFPMs[0].Homepage, "yaml did not load correctly")
}

func TestArrayEmptyVsNil(t *testing.T) {
	conf := `
builds: []
# blobs:
`
	buf := strings.NewReader(conf)
	prop, err := LoadReader(buf)

	require.NoError(t, err)
	require.NotNil(t, prop.Builds)
	require.Empty(t, prop.Builds)
	require.Nil(t, prop.Blobs)
	require.Empty(t, prop.Blobs)
}

type errorReader struct{}

func (errorReader) Read(_ []byte) (n int, err error) {
	return 1, fmt.Errorf("error")
}

func TestLoadBadReader(t *testing.T) {
	_, err := LoadReader(errorReader{})
	require.Error(t, err)
}

func TestFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "config")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	_, err = Load(f.Name())
	require.NoError(t, err)
}

func TestFileNotFound(t *testing.T) {
	_, err := Load("/nope/no-way.yml")
	require.Error(t, err)
}

func TestInvalidFields(t *testing.T) {
	_, err := Load("testdata/invalid_config.yml")
	require.EqualError(t, err, "yaml: unmarshal errors:\n  line 3: field invalid_yaml not found in type config.Build")
}

func TestInvalidYaml(t *testing.T) {
	_, err := Load("testdata/invalid.yml")
	require.EqualError(t, err, "yaml: line 2: did not find expected node content")
}

func TestConfigWithAnchors(t *testing.T) {
	_, err := Load("testdata/anchor.yaml")
	require.NoError(t, err)
}

func TestVersion(t *testing.T) {
	t.Run("allow no version", func(t *testing.T) {
		_, err := LoadReader(bytes.NewReader(nil))
		require.NoError(t, err)
	})
	t.Run("do not allow no version with errors", func(t *testing.T) {
		_, err := LoadReader(strings.NewReader("nope: nope"))
		require.Error(t, err)
		require.ErrorIs(t, err, VersionError{0})
	})
	t.Run("allow v0", func(t *testing.T) {
		_, err := LoadReader(strings.NewReader("version: 0"))
		require.NoError(t, err)
	})
	t.Run("do not allow v0 with errors", func(t *testing.T) {
		_, err := LoadReader(strings.NewReader("version: 0\nnope: nope"))
		require.Error(t, err)
		require.ErrorIs(t, err, VersionError{0})
	})
	t.Run("allow v1", func(t *testing.T) {
		_, err := LoadReader(strings.NewReader("version: 1"))
		require.NoError(t, err)
	})
	t.Run("do not allow v1 with errors", func(t *testing.T) {
		_, err := LoadReader(strings.NewReader("version: 1\nnope: nope"))
		require.Error(t, err)
		require.ErrorIs(t, err, VersionError{1})
	})
	t.Run("allow v2", func(t *testing.T) {
		_, err := LoadReader(strings.NewReader("version: 2\nbuilds: []"))
		require.NoError(t, err)
	})
}

func TestPro(t *testing.T) {
	_, err := LoadReader(strings.NewReader("version: 2\npro: true\nnope: true\n"))
	require.ErrorIs(t, err, ErrProConfig)
}
