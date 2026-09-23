package http

import (
	stdctx "context"
	"encoding/json"
	"io"
	"net"
	h "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goreleaser/goreleaser/v2/internal/artifact"
	"github.com/goreleaser/goreleaser/v2/internal/testctx"
	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/goreleaser/goreleaser/v2/pkg/context"
	"github.com/stretchr/testify/require"
)

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	delay, ok := parseRetryAfter("5", now)
	require.True(t, ok)
	require.Equal(t, 5*time.Second, delay)

	delay, ok = parseRetryAfter(now.Add(2*time.Second).Format(h.TimeFormat), now)
	require.True(t, ok)
	require.Equal(t, 2*time.Second, delay)

	delay, ok = parseRetryAfter(now.Add(-time.Second).Format(h.TimeFormat), now)
	require.True(t, ok)
	require.Equal(t, time.Duration(0), delay)

	_, ok = parseRetryAfter("not-a-date", now)
	require.False(t, ok)
	_, ok = parseRetryAfter("", now)
	require.False(t, ok)
}

func TestUploadRetriesRetriableStatuses(t *testing.T) {
	for _, code := range []int{
		h.StatusRequestTimeout,
		h.StatusTooManyRequests,
		h.StatusInternalServerError,
		h.StatusBadGateway,
		h.StatusServiceUnavailable,
		h.StatusGatewayTimeout,
	} {
		t.Run(h.StatusText(code), func(t *testing.T) {
			var hits atomic.Int32
			body := []byte("artifact-bytes")
			srv := httptest.NewServer(h.HandlerFunc(func(w h.ResponseWriter, r *h.Request) {
				got, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.Equal(t, body, got)
				if hits.Add(1) == 1 {
					w.WriteHeader(code)
					return
				}
				w.WriteHeader(h.StatusCreated)
			}))
			t.Cleanup(srv.Close)

			ctx := newRetryCtx(t, srv.URL+"/obj", body, config.Retry{
				Attempts: 3,
				Delay:    time.Millisecond,
				MaxDelay: 5 * time.Millisecond,
			})
			require.NoError(t, Upload(ctx, ctx.Config.Uploads, "upload", statusChecker))
			require.Equal(t, int32(2), hits.Load())
			require.Equal(t, []context.PublishAttempt{
				{
					Publisher: "upload",
					Instance:  "production",
					Target:    srv.URL + "/obj",
					Attempt:   1,
					Status:    context.PublishStatusFailure,
					Error:     "unexpected http response status: " + statusLine(code),
				},
				{
					Publisher: "upload",
					Instance:  "production",
					Target:    srv.URL + "/obj",
					Attempt:   2,
					Status:    context.PublishStatusSuccess,
				},
			}, ctx.Extra.PublishAttempts)
		})
	}
}

func TestUploadDoesNotRetryOtherStatuses(t *testing.T) {
	for _, code := range []int{h.StatusBadRequest, h.StatusUnauthorized, h.StatusNotFound, h.StatusNotImplemented} {
		t.Run(h.StatusText(code), func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(h.HandlerFunc(func(w h.ResponseWriter, _ *h.Request) {
				hits.Add(1)
				w.WriteHeader(code)
			}))
			t.Cleanup(srv.Close)

			ctx := newRetryCtx(t, srv.URL+"/obj", []byte("x"), config.Retry{Attempts: 4, Delay: time.Millisecond})
			err := Upload(ctx, ctx.Config.Uploads, "upload", statusChecker)
			require.Error(t, err)
			require.Equal(t, int32(1), hits.Load())
			require.Len(t, ctx.Extra.PublishAttempts, 1)
			require.Equal(t, context.PublishStatusFailure, ctx.Extra.PublishAttempts[0].Status)
			require.NotEmpty(t, ctx.Extra.PublishAttempts[0].Error)
		})
	}
}

func TestUploadRetryAfterIsCapped(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(h.HandlerFunc(func(w h.ResponseWriter, _ *h.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(h.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(h.StatusOK)
	}))
	t.Cleanup(srv.Close)

	ctx := newRetryCtx(t, srv.URL+"/obj", []byte("x"), config.Retry{
		Attempts: 2,
		Delay:    time.Second,
		MaxDelay: 40 * time.Millisecond,
	})
	start := time.Now()
	require.NoError(t, Upload(ctx, ctx.Config.Uploads, "upload", statusChecker))
	require.Less(t, time.Since(start), time.Second)
	require.Equal(t, int32(2), hits.Load())
}

func TestUploadHonorsHTTPDateRetryAfter(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(h.HandlerFunc(func(w h.ResponseWriter, _ *h.Request) {
		if hits.Add(1) == 1 {
			// HTTP-date resolution is one second, so stay a couple of seconds out.
			when := time.Now().Add(2 * time.Second).UTC().Format(h.TimeFormat)
			w.Header().Set("Retry-After", when)
			w.WriteHeader(h.StatusTooManyRequests)
			return
		}
		w.WriteHeader(h.StatusOK)
	}))
	t.Cleanup(srv.Close)

	ctx := newRetryCtx(t, srv.URL+"/obj", []byte("x"), config.Retry{
		Attempts: 2,
		Delay:    time.Millisecond,
	})
	start := time.Now()
	require.NoError(t, Upload(ctx, ctx.Config.Uploads, "upload", statusChecker))
	elapsed := time.Since(start)
	require.GreaterOrEqual(t, elapsed, 500*time.Millisecond)
	require.Less(t, elapsed, 5*time.Second)
}

func TestUploadIgnoresRetryAfterOn500(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(h.HandlerFunc(func(w h.ResponseWriter, _ *h.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(h.StatusInternalServerError)
			return
		}
		w.WriteHeader(h.StatusOK)
	}))
	t.Cleanup(srv.Close)

	ctx := newRetryCtx(t, srv.URL+"/obj", []byte("x"), config.Retry{
		Attempts: 2,
		Delay:    time.Millisecond,
	})
	start := time.Now()
	require.NoError(t, Upload(ctx, ctx.Config.Uploads, "upload", statusChecker))
	require.Less(t, time.Since(start), time.Second)
	require.Equal(t, int32(2), hits.Load())
}

func TestUploadRetriesTransportErrors(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	var accepts atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			_ = conn.Close()
		}
	}()

	ctx := newRetryCtx(t, "http://"+ln.Addr().String()+"/obj", []byte("x"), config.Retry{
		Attempts: 3,
		Delay:    time.Millisecond,
		MaxDelay: 5 * time.Millisecond,
	})
	err = Upload(ctx, ctx.Config.Uploads, "upload", statusChecker)
	require.Error(t, err)
	require.Len(t, ctx.Extra.PublishAttempts, 3)
	for i, attempt := range ctx.Extra.PublishAttempts {
		require.Equal(t, i+1, attempt.Attempt)
		require.Equal(t, context.PublishStatusFailure, attempt.Status)
		require.NotEmpty(t, attempt.Error)
		require.Equal(t, "upload", attempt.Publisher)
	}
	require.GreaterOrEqual(t, accepts.Load(), int32(3))
}

func TestUploadStopsWhenContextIsCanceled(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(h.HandlerFunc(func(w h.ResponseWriter, _ *h.Request) {
		once.Do(func() { close(started) })
		w.WriteHeader(h.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	parent, cancel := stdctx.WithCancel(t.Context())
	t.Cleanup(cancel)
	ctx := newRetryCtx(t, srv.URL+"/obj", []byte("x"), config.Retry{Attempts: 4, Delay: time.Hour})
	ctx.Context = parent

	done := make(chan error, 1)
	go func() {
		done <- Upload(ctx, ctx.Config.Uploads, "upload", statusChecker)
	}()
	select {
	case <-started:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("upload did not start")
	}
	select {
	case err := <-done:
		require.ErrorIs(t, err, stdctx.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("retry did not stop after cancel")
	}
	require.Len(t, ctx.Extra.PublishAttempts, 1)
	require.Equal(t, context.PublishStatusFailure, ctx.Extra.PublishAttempts[0].Status)
}

func TestUploadRetriesExtraFilesAndSortsAttempts(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	bin := "b.bin"
	require.NoError(t, os.WriteFile(bin, []byte("bbb"), 0o644))
	require.NoError(t, os.WriteFile("a.txt", []byte("aaa"), 0o644))

	var mu sync.Mutex
	seen := map[string][][]byte{}
	srv := httptest.NewServer(h.HandlerFunc(func(w h.ResponseWriter, r *h.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		mu.Lock()
		seen[r.URL.Path] = append(seen[r.URL.Path], body)
		n := len(seen[r.URL.Path])
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(h.StatusBadGateway)
			return
		}
		w.WriteHeader(h.StatusCreated)
	}))
	t.Cleanup(srv.Close)

	ctx := testctx.WrapWithCfg(t.Context(), config.Project{
		Uploads: []config.Upload{{
			Name:   "production",
			Mode:   ModeBinary,
			Method: h.MethodPut,
			Target: srv.URL + "/",
			Retry: config.Retry{
				Attempts: 3,
				Delay:    time.Millisecond,
				MaxDelay: 5 * time.Millisecond,
			},
			ExtraFiles: []config.ExtraFile{{Glob: "./a.txt"}},
		}},
	})
	ctx.Artifacts.Add(&artifact.Artifact{
		Name: "b.bin",
		Path: bin,
		Type: artifact.UploadableBinary,
	})

	require.NoError(t, Upload(ctx, ctx.Config.Uploads, "upload", statusChecker))

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, [][]byte{[]byte("aaa"), []byte("aaa")}, seen["/a.txt"])
	require.Equal(t, [][]byte{[]byte("bbb"), []byte("bbb")}, seen["/b.bin"])

	require.Equal(t, []context.PublishAttempt{
		attemptAt(srv.URL+"/a.txt", 1, context.PublishStatusFailure, "unexpected http response status: "+statusLine(h.StatusBadGateway)),
		attemptAt(srv.URL+"/a.txt", 2, context.PublishStatusSuccess, ""),
		attemptAt(srv.URL+"/b.bin", 1, context.PublishStatusFailure, "unexpected http response status: "+statusLine(h.StatusBadGateway)),
		attemptAt(srv.URL+"/b.bin", 2, context.PublishStatusSuccess, ""),
	}, ctx.Extra.PublishAttempts)

	payload, err := json.Marshal(struct {
		Extra context.Extra `json:"extra"`
	}{Extra: ctx.Extra})
	require.NoError(t, err)
	require.NotContains(t, string(payload), `"status":"success","error"`)
	require.Contains(t, string(payload), `"error":"unexpected http response status: `+statusLine(h.StatusBadGateway)+`"`)
}

func statusLine(code int) string {
	return strconv.Itoa(code) + " " + h.StatusText(code)
}

func statusChecker(res *h.Response) error {
	if c := res.StatusCode; c < 200 || 299 < c {
		return errStatus(res.Status)
	}
	return nil
}

type statusError string

func (e statusError) Error() string { return "unexpected http response status: " + string(e) }

func errStatus(status string) error { return statusError(status) }

func attemptAt(target string, n int, status, errText string) context.PublishAttempt {
	return context.PublishAttempt{
		Publisher: "upload",
		Instance:  "production",
		Target:    target,
		Attempt:   n,
		Status:    status,
		Error:     errText,
	}
}

func newRetryCtx(t *testing.T, target string, body []byte, retry config.Retry) *context.Context {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact.bin")
	require.NoError(t, os.WriteFile(path, body, 0o644))
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{
		Uploads: []config.Upload{{
			Name:               "production",
			Mode:               ModeBinary,
			Method:             h.MethodPut,
			Target:             target,
			CustomArtifactName: true,
			Retry:              retry,
		}},
	})
	ctx.Artifacts.Add(&artifact.Artifact{
		Name: "artifact.bin",
		Path: path,
		Type: artifact.UploadableBinary,
	})
	return ctx
}
