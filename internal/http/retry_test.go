package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/goreleaser/goreleaser/v2/internal/artifact"
	"github.com/goreleaser/goreleaser/v2/internal/testctx"
	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/stretchr/testify/require"
)

func statusCheck(res *http.Response) error {
	if c := res.StatusCode; c < 200 || 299 < c {
		return &unexpectedStatus{res.Status}
	}
	return nil
}

type unexpectedStatus struct{ status string }

func (e *unexpectedStatus) Error() string { return "unexpected http response status: " + e.status }

type recordingServer struct {
	*httptest.Server
	mu     sync.Mutex
	bodies map[string][]string
}

func newRecordingServer(t *testing.T, handler func(path string, n int, w http.ResponseWriter)) *recordingServer {
	t.Helper()
	rs := &recordingServer{bodies: map[string][]string{}}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rs.mu.Lock()
		rs.bodies[r.URL.Path] = append(rs.bodies[r.URL.Path], string(body))
		n := len(rs.bodies[r.URL.Path])
		rs.mu.Unlock()
		handler(r.URL.Path, n, w)
	}))
	t.Cleanup(rs.Close)
	return rs
}

func setupRetryCtx(t *testing.T, srvURL string, retry config.Retry) (*artifact.Artifact, *config.Upload, func() error) {
	t.Helper()
	t.Chdir(t.TempDir())
	path := "a.tar.gz"
	require.NoError(t, os.WriteFile(path, []byte("full-content"), 0o644))
	extra := "extra.txt"
	require.NoError(t, os.WriteFile(extra, []byte("extra-content"), 0o644))

	upload := config.Upload{
		Name:       "prod",
		Mode:       ModeArchive,
		Method:     http.MethodPut,
		Target:     srvURL + "/{{ .ProjectName }}",
		Retry:      retry,
		ExtraFiles: []config.ExtraFile{{Glob: extra}},
	}
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{
		ProjectName: "proj",
		Uploads:     []config.Upload{upload},
	})
	a := &artifact.Artifact{
		Name: "a.tar.gz",
		Path: path,
		Type: artifact.UploadableArchive,
	}
	ctx.Artifacts.Add(a)
	return a, &ctx.Config.Uploads[0], func() error {
		return Upload(ctx, ctx.Config.Uploads, "upload", statusCheck)
	}
}

func TestUploadRetryTransientStatus(t *testing.T) {
	srv := newRecordingServer(t, func(path string, n int, w http.ResponseWriter) {
		if path == "/proj/a.tar.gz" && n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if path == "/proj/extra.txt" && n < 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})
	a, _, run := setupRetryCtx(t, srv.URL, config.Retry{Attempts: 5, Delay: time.Millisecond})
	require.NoError(t, run())

	require.Equal(t, []string{"full-content", "full-content", "full-content"}, srv.bodies["/proj/a.tar.gz"])
	require.Equal(t, []string{"extra-content", "extra-content"}, srv.bodies["/proj/extra.txt"])

	target := srv.URL + "/proj/a.tar.gz"
	attempts := artifact.MustExtra[[]artifact.PublishAttempt](*a, artifact.ExtraPublishAttempts)
	require.Equal(t, []artifact.PublishAttempt{
		{Publisher: "upload", Instance: "prod", Target: target, Attempt: 1, Status: "failure", Error: "prod: upload: upload failed: unexpected http response status: 503 Service Unavailable"},
		{Publisher: "upload", Instance: "prod", Target: target, Attempt: 2, Status: "failure", Error: "prod: upload: upload failed: unexpected http response status: 503 Service Unavailable"},
		{Publisher: "upload", Instance: "prod", Target: target, Attempt: 3, Status: "success"},
	}, attempts)
}

func TestUploadNoRetryOnClientError(t *testing.T) {
	srv := newRecordingServer(t, func(_ string, _ int, w http.ResponseWriter) {
		w.WriteHeader(http.StatusBadRequest)
	})
	a, _, run := setupRetryCtx(t, srv.URL, config.Retry{Attempts: 5, Delay: time.Millisecond})
	require.Error(t, run())
	require.Len(t, srv.bodies["/proj/a.tar.gz"], 1)
	attempts := artifact.MustExtra[[]artifact.PublishAttempt](*a, artifact.ExtraPublishAttempts)
	require.Len(t, attempts, 1)
	require.Equal(t, "failure", attempts[0].Status)
	require.NotEmpty(t, attempts[0].Error)
}

func TestUploadRetryAllStatuses(t *testing.T) {
	for _, code := range []int{408, 429, 500, 502, 503, 504} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			srv := newRecordingServer(t, func(_ string, n int, w http.ResponseWriter) {
				if n == 1 {
					w.WriteHeader(code)
					return
				}
				w.WriteHeader(http.StatusOK)
			})
			_, _, run := setupRetryCtx(t, srv.URL, config.Retry{Attempts: 2})
			require.NoError(t, run())
			require.Len(t, srv.bodies["/proj/a.tar.gz"], 2)
		})
	}
}

func TestUploadRetryTransportError(t *testing.T) {
	srv := newRecordingServer(t, func(_ string, _ int, w http.ResponseWriter) {
		w.WriteHeader(http.StatusOK)
	})
	url := srv.URL
	srv.Close()
	a, _, run := setupRetryCtx(t, url, config.Retry{Attempts: 3})
	require.Error(t, run())
	attempts := artifact.MustExtra[[]artifact.PublishAttempt](*a, artifact.ExtraPublishAttempts)
	require.Len(t, attempts, 3)
	for i, at := range attempts {
		require.Equal(t, uint(i+1), at.Attempt)
		require.Equal(t, "failure", at.Status)
	}
}

func TestUploadRetryAfterCappedByMaxDelay(t *testing.T) {
	srv := newRecordingServer(t, func(_ string, n int, w http.ResponseWriter) {
		if n == 1 {
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	_, _, run := setupRetryCtx(t, srv.URL, config.Retry{Attempts: 2, MaxDelay: 10 * time.Millisecond})
	start := time.Now()
	require.NoError(t, run())
	require.Less(t, time.Since(start), 5*time.Second)
}

func TestUploadRetryContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	srv := newRecordingServer(t, func(_ string, _ int, w http.ResponseWriter) {
		cancel()
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	_, upload, _ := setupRetryCtx(t, srv.URL, config.Retry{Attempts: 5, Delay: time.Hour})
	gctx := testctx.WrapWithCfg(ctx, config.Project{ProjectName: "proj"})
	gctx.Artifacts.Add(&artifact.Artifact{Name: "b.tar.gz", Path: upload.ExtraFiles[0].Glob, Type: artifact.UploadableArchive})
	err := Upload(gctx, []config.Upload{*upload}, "upload", statusCheck)
	require.ErrorIs(t, err, context.Canceled)
}

func TestParseRetryAfter(t *testing.T) {
	d, ok := parseRetryAfter("5")
	require.True(t, ok)
	require.Equal(t, 5*time.Second, d)

	d, ok = parseRetryAfter(time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
	require.True(t, ok)
	require.Greater(t, d, 59*time.Minute)

	d, ok = parseRetryAfter(time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat))
	require.True(t, ok)
	require.Equal(t, time.Duration(0), d)

	for _, v := range []string{"", "-1", "soon", "1.5"} {
		_, ok := parseRetryAfter(v)
		require.False(t, ok, v)
	}
}
