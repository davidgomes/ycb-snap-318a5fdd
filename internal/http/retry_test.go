package http

import (
	"bytes"
	stdctx "context"
	"fmt"
	"io"
	h "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goreleaser/goreleaser/v2/internal/artifact"
	"github.com/goreleaser/goreleaser/v2/internal/publishattempt"
	"github.com/goreleaser/goreleaser/v2/internal/testctx"
	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestUploadRetriesTransientStatuses(t *testing.T) {
	payload := bytes.Repeat([]byte("abc123"), 4096)
	statuses := []int{
		h.StatusRequestTimeout,
		h.StatusTooManyRequests,
		h.StatusInternalServerError,
		h.StatusBadGateway,
		h.StatusServiceUnavailable,
		h.StatusGatewayTimeout,
	}
	for _, status := range statuses {
		t.Run(h.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			var bodies [][]byte
			var mu sync.Mutex
			srv := httptest.NewServer(h.HandlerFunc(func(w h.ResponseWriter, r *h.Request) {
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				mu.Lock()
				bodies = append(bodies, body)
				mu.Unlock()
				n := calls.Add(1)
				if n == 1 {
					w.WriteHeader(status)
					return
				}
				w.WriteHeader(h.StatusCreated)
			}))
			t.Cleanup(srv.Close)

			art := newRetryArtifact(t, payload)
			ctx := testctx.WrapWithCfg(t.Context(), config.Project{
				Uploads: []config.Upload{retryUpload(srv.URL, config.Retry{Attempts: 2})},
			})
			ctx.Artifacts.Add(art)

			require.NoError(t, Upload(ctx, ctx.Config.Uploads, "upload", accept2xx))
			require.Equal(t, int32(2), calls.Load())
			require.Equal(t, [][]byte{payload, payload}, bodies)
			require.Equal(t, []artifact.PublishAttempt{
				{
					Publisher: "upload",
					Instance:  "production",
					Target:    srv.URL + "/artifact.bin",
					Attempt:   1,
					Status:    publishattempt.StatusFailure,
					Error:     fmt.Sprintf("unexpected http status code: %d", status),
				},
				{
					Publisher: "upload",
					Instance:  "production",
					Target:    srv.URL + "/artifact.bin",
					Attempt:   2,
					Status:    publishattempt.StatusSuccess,
				},
			}, publishattempt.List(art))
		})
	}
}

func TestUploadDoesNotRetryPermanentStatus(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(h.HandlerFunc(func(w h.ResponseWriter, _ *h.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(h.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	art := newRetryArtifact(t, []byte("permanent"))
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{
		Uploads: []config.Upload{retryUpload(srv.URL, config.Retry{Attempts: 5, Delay: time.Hour})},
	})
	ctx.Artifacts.Add(art)

	err := Upload(ctx, ctx.Config.Uploads, "artifactory", accept2xx)
	require.ErrorContains(t, err, "unexpected http status code: 404")
	require.Equal(t, int32(1), calls.Load())
	require.Equal(t, publishattempt.StatusFailure, publishattempt.List(art)[0].Status)
	require.Equal(t, "artifactory", publishattempt.List(art)[0].Publisher)
	require.Len(t, publishattempt.List(art), 1)
}

func TestUploadRetryAfterAndMaxDelay(t *testing.T) {
	t.Run("delta seconds is honored", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(h.HandlerFunc(func(w h.ResponseWriter, _ *h.Request) {
			n := calls.Add(1)
			if n == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(h.StatusTooManyRequests)
				return
			}
			w.WriteHeader(h.StatusCreated)
		}))
		t.Cleanup(srv.Close)

		art := newRetryArtifact(t, []byte("ra"))
		ctx := testctx.WrapWithCfg(t.Context(), config.Project{
			Uploads: []config.Upload{retryUpload(srv.URL, config.Retry{Attempts: 2, Delay: 10 * time.Millisecond})},
		})
		ctx.Artifacts.Add(art)

		start := time.Now()
		require.NoError(t, Upload(ctx, ctx.Config.Uploads, "upload", accept2xx))
		elapsed := time.Since(start)
		require.Greater(t, elapsed, 800*time.Millisecond)
		require.Less(t, elapsed, 3*time.Second)
		require.Equal(t, int32(2), calls.Load())
	})

	t.Run("http date is honored", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(h.HandlerFunc(func(w h.ResponseWriter, _ *h.Request) {
			n := calls.Add(1)
			if n == 1 {
				w.Header().Set("Retry-After", time.Now().Add(2*time.Second).UTC().Format(h.TimeFormat))
				w.WriteHeader(h.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(h.StatusCreated)
		}))
		t.Cleanup(srv.Close)

		art := newRetryArtifact(t, []byte("date"))
		ctx := testctx.WrapWithCfg(t.Context(), config.Project{
			Uploads: []config.Upload{retryUpload(srv.URL, config.Retry{Attempts: 2})},
		})
		ctx.Artifacts.Add(art)

		start := time.Now()
		require.NoError(t, Upload(ctx, ctx.Config.Uploads, "upload", accept2xx))
		require.Greater(t, time.Since(start), time.Second)
		require.Less(t, time.Since(start), 5*time.Second)
		require.Equal(t, int32(2), calls.Load())
	})

	t.Run("max delay caps retry-after", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(h.HandlerFunc(func(w h.ResponseWriter, _ *h.Request) {
			n := calls.Add(1)
			if n == 1 {
				w.Header().Set("Retry-After", "30")
				w.WriteHeader(h.StatusTooManyRequests)
				return
			}
			w.WriteHeader(h.StatusCreated)
		}))
		t.Cleanup(srv.Close)

		art := newRetryArtifact(t, []byte("cap"))
		ctx := testctx.WrapWithCfg(t.Context(), config.Project{
			Uploads: []config.Upload{retryUpload(srv.URL, config.Retry{
				Attempts: 2,
				Delay:    time.Second,
				MaxDelay: 25 * time.Millisecond,
			})},
		})
		ctx.Artifacts.Add(art)

		start := time.Now()
		require.NoError(t, Upload(ctx, ctx.Config.Uploads, "upload", accept2xx))
		require.Less(t, time.Since(start), time.Second)
		require.Equal(t, int32(2), calls.Load())
	})

	t.Run("invalid retry-after falls back to backoff", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(h.HandlerFunc(func(w h.ResponseWriter, _ *h.Request) {
			n := calls.Add(1)
			if n == 1 {
				w.Header().Set("Retry-After", "soon")
				w.WriteHeader(h.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(h.StatusCreated)
		}))
		t.Cleanup(srv.Close)

		art := newRetryArtifact(t, []byte("bad-header"))
		ctx := testctx.WrapWithCfg(t.Context(), config.Project{
			Uploads: []config.Upload{retryUpload(srv.URL, config.Retry{Attempts: 2})},
		})
		ctx.Artifacts.Add(art)
		require.NoError(t, Upload(ctx, ctx.Config.Uploads, "upload", accept2xx))
		require.Equal(t, int32(2), calls.Load())
	})

	t.Run("retry-after on 500 is ignored", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(h.HandlerFunc(func(w h.ResponseWriter, _ *h.Request) {
			n := calls.Add(1)
			if n == 1 {
				w.Header().Set("Retry-After", "5")
				w.WriteHeader(h.StatusInternalServerError)
				return
			}
			w.WriteHeader(h.StatusCreated)
		}))
		t.Cleanup(srv.Close)

		art := newRetryArtifact(t, []byte("500"))
		ctx := testctx.WrapWithCfg(t.Context(), config.Project{
			Uploads: []config.Upload{retryUpload(srv.URL, config.Retry{Attempts: 2, Delay: time.Millisecond})},
		})
		ctx.Artifacts.Add(art)
		start := time.Now()
		require.NoError(t, Upload(ctx, ctx.Config.Uploads, "upload", accept2xx))
		require.Less(t, time.Since(start), time.Second)
		require.Equal(t, int32(2), calls.Load())
	})
}

func TestUploadRetriesTransportErrors(t *testing.T) {
	payload := []byte("transport-body")
	var calls atomic.Int32
	srv := httptest.NewServer(h.HandlerFunc(func(w h.ResponseWriter, r *h.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, payload, body)
		n := calls.Add(1)
		if n == 1 {
			hj, ok := w.(h.Hijacker)
			require.True(t, ok)
			conn, _, err := hj.Hijack()
			require.NoError(t, err)
			require.NoError(t, conn.Close())
			return
		}
		w.WriteHeader(h.StatusCreated)
	}))
	t.Cleanup(srv.Close)

	art := newRetryArtifact(t, payload)
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{
		Uploads: []config.Upload{retryUpload(srv.URL, config.Retry{Attempts: 2})},
	})
	ctx.Artifacts.Add(art)
	require.NoError(t, Upload(ctx, ctx.Config.Uploads, "upload", accept2xx))
	require.Equal(t, int32(2), calls.Load())
	attempts := publishattempt.List(art)
	require.Len(t, attempts, 2)
	require.Equal(t, publishattempt.StatusFailure, attempts[0].Status)
	require.NotEmpty(t, attempts[0].Error)
	require.Equal(t, publishattempt.StatusSuccess, attempts[1].Status)
}

func TestUploadContextCancel(t *testing.T) {
	t.Run("before the attempt", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(h.HandlerFunc(func(w h.ResponseWriter, _ *h.Request) {
			calls.Add(1)
			w.WriteHeader(h.StatusCreated)
		}))
		t.Cleanup(srv.Close)

		parent, cancel := stdctx.WithCancel(t.Context())
		cancel()
		art := newRetryArtifact(t, []byte("x"))
		ctx := testctx.WrapWithCfg(parent, config.Project{
			Uploads: []config.Upload{retryUpload(srv.URL, config.Retry{Attempts: 3})},
		})
		ctx.Artifacts.Add(art)
		err := Upload(ctx, ctx.Config.Uploads, "upload", accept2xx)
		require.Equal(t, stdctx.Canceled, err)
		require.Equal(t, int32(0), calls.Load())
		require.Empty(t, publishattempt.List(art))
	})

	t.Run("during backoff", func(t *testing.T) {
		started := make(chan struct{})
		var calls atomic.Int32
		srv := httptest.NewServer(h.HandlerFunc(func(w h.ResponseWriter, _ *h.Request) {
			if calls.Add(1) == 1 {
				close(started)
			}
			w.WriteHeader(h.StatusBadGateway)
		}))
		t.Cleanup(srv.Close)

		parent, cancel := stdctx.WithCancel(t.Context())
		t.Cleanup(cancel)
		art := newRetryArtifact(t, []byte("x"))
		ctx := testctx.WrapWithCfg(parent, config.Project{
			Uploads: []config.Upload{retryUpload(srv.URL, config.Retry{Attempts: 5, Delay: time.Hour})},
		})
		ctx.Artifacts.Add(art)

		errCh := make(chan error, 1)
		go func() {
			errCh <- Upload(ctx, ctx.Config.Uploads, "upload", accept2xx)
		}()
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("upload did not start")
		}
		cancel()
		select {
		case err := <-errCh:
			require.Equal(t, stdctx.Canceled, err)
		case <-time.After(2 * time.Second):
			t.Fatal("upload did not stop")
		}
		require.Equal(t, int32(1), calls.Load())
		attempts := publishattempt.List(art)
		require.Len(t, attempts, 1)
		require.Equal(t, publishattempt.StatusFailure, attempts[0].Status)
	})
}

func TestUploadRetriesExtraFile(t *testing.T) {
	// fileglob rejects absolute paths, and testdata/*.txt is used by other tests.
	path := filepath.Join("testdata", "retry", "notes.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	payload := []byte("extra-file-body")
	require.NoError(t, os.WriteFile(path, payload, 0o644))
	t.Cleanup(func() {
		_ = os.RemoveAll(filepath.Join("testdata", "retry"))
	})

	var calls atomic.Int32
	srv := httptest.NewServer(h.HandlerFunc(func(w h.ResponseWriter, r *h.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, payload, body)
		n := calls.Add(1)
		if n == 1 {
			w.WriteHeader(h.StatusGatewayTimeout)
			return
		}
		w.WriteHeader(h.StatusCreated)
	}))
	t.Cleanup(srv.Close)

	ctx := testctx.WrapWithCfg(t.Context(), config.Project{
		Uploads: []config.Upload{{
			Name:           "production",
			Target:         srv.URL + "/",
			Mode:           ModeArchive,
			Method:         h.MethodPut,
			ExtraFilesOnly: true,
			ExtraFiles:     []config.ExtraFile{{Glob: path}},
			Retry:          config.Retry{Attempts: 3},
		}},
	})
	require.NoError(t, Upload(ctx, ctx.Config.Uploads, "upload", accept2xx))
	require.Equal(t, int32(2), calls.Load())

	var found *artifact.Artifact
	for _, art := range ctx.Artifacts.List() {
		if art.Name == "notes.txt" {
			found = art
		}
	}
	require.NotNil(t, found)
	require.Equal(t, []artifact.PublishAttempt{
		{Publisher: "upload", Instance: "production", Target: srv.URL + "/notes.txt", Attempt: 1, Status: publishattempt.StatusFailure, Error: "unexpected http status code: 504"},
		{Publisher: "upload", Instance: "production", Target: srv.URL + "/notes.txt", Attempt: 2, Status: publishattempt.StatusSuccess},
	}, publishattempt.List(found))
}

func TestUploadAttemptsAreSortedAcrossInstances(t *testing.T) {
	srv := httptest.NewServer(h.HandlerFunc(func(w h.ResponseWriter, _ *h.Request) {
		w.WriteHeader(h.StatusCreated)
	}))
	t.Cleanup(srv.Close)

	art := newRetryArtifact(t, []byte("sorted"))
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{
		Uploads: []config.Upload{
			retryNamedUpload("zeta", srv.URL+"/z", config.Retry{}),
			retryNamedUpload("alpha", srv.URL+"/a", config.Retry{}),
		},
	})
	ctx.Artifacts.Add(art)
	require.NoError(t, Upload(ctx, ctx.Config.Uploads, "upload", accept2xx))
	require.Equal(t, []artifact.PublishAttempt{
		{Publisher: "upload", Instance: "alpha", Target: srv.URL + "/a/artifact.bin", Attempt: 1, Status: publishattempt.StatusSuccess},
		{Publisher: "upload", Instance: "zeta", Target: srv.URL + "/z/artifact.bin", Attempt: 1, Status: publishattempt.StatusSuccess},
	}, publishattempt.List(art))
}

func accept2xx(r *h.Response) error {
	if r.StatusCode/100 == 2 {
		return nil
	}
	return fmt.Errorf("unexpected http status code: %d", r.StatusCode)
}

func retryUpload(server string, retry config.Retry) config.Upload {
	return retryNamedUpload("production", server, retry)
}

func retryNamedUpload(name, server string, retry config.Retry) config.Upload {
	return config.Upload{
		Name:   name,
		Target: server + "/",
		Mode:   ModeBinary,
		Method: h.MethodPut,
		Retry:  retry,
	}
}

func newRetryArtifact(t *testing.T, payload []byte) *artifact.Artifact {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact.bin")
	require.NoError(t, os.WriteFile(path, payload, 0o644))
	return &artifact.Artifact{
		Name: "artifact.bin",
		Path: path,
		Type: artifact.UploadableBinary,
	}
}
