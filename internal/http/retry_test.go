package http

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	stdctx "context"

	"github.com/goreleaser/goreleaser/v2/internal/artifact"
	"github.com/goreleaser/goreleaser/v2/internal/retry"
	"github.com/goreleaser/goreleaser/v2/internal/testctx"
	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/goreleaser/goreleaser/v2/pkg/context"
	"github.com/stretchr/testify/require"
)

func TestUploadRetriesFullBodyAndAudits(t *testing.T) {
	assetOpenReset()
	payload := []byte("full-artifact-body")
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, payload, body)
		if calls.Add(1) < 3 {
			w.Header().Set("Retry-After", "9")
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	require.NoError(t, os.WriteFile(bin, payload, 0o644))

	ctx := testctx.WrapWithCfg(t.Context(), config.Project{ProjectName: "proj"}, testctx.WithVersion("1.2.3"))
	art := &artifact.Artifact{
		Name: "bin",
		Path: bin,
		Type: artifact.UploadableBinary,
		Extra: map[string]any{
			artifact.ExtraID: "foo",
		},
	}
	ctx.Artifacts.Add(art)

	var waits []time.Duration
	var waitMu sync.Mutex
	orig := retry.Sleep
	t.Cleanup(func() { retry.Sleep = orig })
	retry.Sleep = func(ctx stdctx.Context, d time.Duration) error {
		waitMu.Lock()
		waits = append(waits, d)
		waitMu.Unlock()
		return orig(ctx, 0)
	}

	err := Upload(ctx, []config.Upload{{
		Name:   "production",
		Mode:   ModeBinary,
		Target: srv.URL + "/{{ .ProjectName }}/{{ .Version }}/",
		Retry:  config.Retry{Attempts: 4, Delay: 10 * time.Millisecond, MaxDelay: 25 * time.Millisecond},
	}}, "upload", func(r *http.Response) error {
		if r.StatusCode/100 == 2 {
			return nil
		}
		return &statusErr{status: r.Status}
	})
	require.NoError(t, err)
	require.Equal(t, int32(3), calls.Load())
	require.Equal(t, []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}, waits)

	attempts := ctx.PublishAttempts()
	require.Len(t, attempts, 3)
	for _, attempt := range attempts {
		require.Equal(t, "upload", attempt.Publisher)
		require.Equal(t, "production", attempt.Instance)
		require.Equal(t, srv.URL+"/proj/1.2.3/bin", attempt.Target)
	}
	raw, err := json.Marshal(attempts)
	require.NoError(t, err)
	require.NotContains(t, string(raw), `"error":""`)

	perArt, ok := art.Extra[context.ExtraPublishAttempts].([]context.PublishAttempt)
	require.True(t, ok)
	require.Equal(t, 3, len(perArt))
	require.Equal(t, context.PublishStatusFailure, perArt[0].Status)
	require.NotEmpty(t, perArt[0].Error)
	require.Equal(t, context.PublishStatusFailure, perArt[1].Status)
	require.Equal(t, context.PublishStatusSuccess, perArt[2].Status)
	require.Empty(t, perArt[2].Error)
}

func TestUploadDoesNotRetryPermanentStatus(t *testing.T) {
	assetOpenReset()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	require.NoError(t, os.WriteFile(bin, []byte("x"), 0o644))
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{})
	ctx.Artifacts.Add(&artifact.Artifact{Name: "bin", Path: bin, Type: artifact.UploadableBinary})

	err := Upload(ctx, []config.Upload{{
		Name:   "production",
		Mode:   ModeBinary,
		Target: srv.URL + "/",
		Retry:  config.Retry{Attempts: 5, Delay: time.Second},
	}}, "upload", func(r *http.Response) error {
		if r.StatusCode >= 200 && r.StatusCode < 300 {
			return nil
		}
		return &statusErr{status: r.Status}
	})
	require.Error(t, err)
	require.Equal(t, int32(1), calls.Load())
	require.Len(t, ctx.PublishAttempts(), 1)
	require.Equal(t, context.PublishStatusFailure, ctx.PublishAttempts()[0].Status)
}

func TestUploadRetryAfterIsCapped(t *testing.T) {
	assetOpenReset()
	var calls atomic.Int32
	future := time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		require.Equal(t, "xyz", string(body))
		n := calls.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if n == 2 {
			w.Header().Set("Retry-After", future)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	require.NoError(t, os.WriteFile(bin, []byte("xyz"), 0o644))
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{})
	ctx.Artifacts.Add(&artifact.Artifact{Name: "bin", Path: bin, Type: artifact.UploadableBinary})

	var got []time.Duration
	orig := retry.Sleep
	t.Cleanup(func() { retry.Sleep = orig })
	retry.Sleep = func(_ stdctx.Context, d time.Duration) error {
		got = append(got, d)
		return nil
	}

	err := Upload(ctx, []config.Upload{{
		Name:   "edge",
		Mode:   ModeBinary,
		Target: srv.URL + "/",
		Retry:  config.Retry{Attempts: 3, Delay: time.Millisecond, MaxDelay: 40 * time.Millisecond},
	}}, "upload", func(r *http.Response) error {
		if r.StatusCode/100 == 2 {
			return nil
		}
		return &statusErr{status: r.Status}
	})
	require.NoError(t, err)
	require.Equal(t, []time.Duration{40 * time.Millisecond, 40 * time.Millisecond}, got)
	require.Equal(t, context.PublishStatusSuccess, ctx.PublishAttempts()[2].Status)
}

func TestUploadContextCancel(t *testing.T) {
	assetOpenReset()
	parent, cancel := stdctx.WithCancel(t.Context())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		cancel()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	require.NoError(t, os.WriteFile(bin, []byte("xyz"), 0o644))
	ctx := testctx.WrapWithCfg(parent, config.Project{})
	ctx.Artifacts.Add(&artifact.Artifact{Name: "bin", Path: bin, Type: artifact.UploadableBinary})

	err := Upload(ctx, []config.Upload{{
		Name:   "production",
		Mode:   ModeBinary,
		Target: srv.URL + "/",
		Retry:  config.Retry{Attempts: 5, Delay: time.Hour, MaxDelay: time.Hour},
	}}, "upload", func(r *http.Response) error {
		if r.StatusCode/100 == 2 {
			return nil
		}
		return &statusErr{status: r.Status}
	})
	require.ErrorIs(t, err, stdctx.Canceled)
	require.Len(t, ctx.PublishAttempts(), 1)
	require.Equal(t, context.PublishStatusFailure, ctx.PublishAttempts()[0].Status)
}

func TestUploadAttemptOrder(t *testing.T) {
	assetOpenReset()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	for _, name := range []string{"b.bin", "a.bin"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644))
	}
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{})
	for _, name := range []string{"b.bin", "a.bin"} {
		ctx.Artifacts.Add(&artifact.Artifact{
			Name: name,
			Path: filepath.Join(dir, name),
			Type: artifact.UploadableBinary,
		})
	}
	require.NoError(t, Upload(ctx, []config.Upload{
		{Name: "z", Mode: ModeBinary, Target: srv.URL + "/z/"},
		{Name: "a", Mode: ModeBinary, Target: srv.URL + "/a/"},
	}, "upload", func(r *http.Response) error {
		if r.StatusCode/100 == 2 {
			return nil
		}
		return &statusErr{status: r.Status}
	}))

	var keys []string
	for _, attempt := range ctx.PublishAttempts() {
		require.Equal(t, 1, attempt.Attempt)
		require.Equal(t, context.PublishStatusSuccess, attempt.Status)
		keys = append(keys, attempt.Instance+" "+attempt.Target)
	}
	require.Equal(t, []string{
		"a " + srv.URL + "/a/a.bin",
		"a " + srv.URL + "/a/b.bin",
		"z " + srv.URL + "/z/a.bin",
		"z " + srv.URL + "/z/b.bin",
	}, keys)
}

func TestRetryAfterIgnoredUnless429Or503(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusBadGateway, Header: http.Header{"Retry-After": []string{"10"}}}
	_, ok := retryAfterFor(resp, time.Now())
	require.False(t, ok)
	resp.StatusCode = http.StatusTooManyRequests
	d, ok := retryAfterFor(resp, time.Now())
	require.True(t, ok)
	require.Equal(t, 10*time.Second, d)
}

type statusErr struct{ status string }

func (e *statusErr) Error() string { return "unexpected http response status: " + e.status }

func TestTransportErrorRetries(t *testing.T) {
	assetOpenReset()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	require.NoError(t, os.WriteFile(bin, []byte("xyz"), 0o644))
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{})
	ctx.Artifacts.Add(&artifact.Artifact{Name: "bin", Path: bin, Type: artifact.UploadableBinary})

	err := Upload(ctx, []config.Upload{{
		Name:               "production",
		Mode:               ModeBinary,
		Target:             "http://127.0.0.1:1/{{ .ArtifactName }}",
		CustomArtifactName: true,
		Retry:              config.Retry{Attempts: 3},
	}}, "artifactory", func(r *http.Response) error { return nil })
	require.Error(t, err)
	attempts := ctx.PublishAttempts()
	require.Len(t, attempts, 3)
	for i, attempt := range attempts {
		require.Equal(t, "artifactory", attempt.Publisher)
		require.Equal(t, "production", attempt.Instance)
		require.Equal(t, i+1, attempt.Attempt)
		require.Equal(t, context.PublishStatusFailure, attempt.Status)
		require.NotEmpty(t, attempt.Error)
		require.Contains(t, attempt.Target, "bin")
	}
	raw, err := json.Marshal(attempts)
	require.NoError(t, err)
	require.True(t, bytes.Contains(raw, []byte(`"publisher":"artifactory"`)))
}
