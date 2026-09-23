package http

import (
	stdctx "context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/goreleaser/goreleaser/v2/internal/artifact"
	"github.com/goreleaser/goreleaser/v2/internal/testctx"
	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/goreleaser/goreleaser/v2/pkg/context"
	"github.com/stretchr/testify/require"
)

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	for name, tt := range map[string]struct {
		value  string
		expect time.Duration
		valid  bool
	}{
		"empty":             {"", 0, false},
		"blank":             {"   ", 0, false},
		"seconds":           {"120", 2 * time.Minute, true},
		"zero":              {"0", 0, true},
		"padded":            {" 5 ", 5 * time.Second, true},
		"overflow":          {"99999999999999999999999", math.MaxInt64, true},
		"huge":              {"9223372036854775807", math.MaxInt64, true},
		"negative":          {"-5", 0, false},
		"decimal":           {"1.5", 0, false},
		"garbage":           {"soon", 0, false},
		"http date":         {"Thu, 02 Jan 2025 03:04:35 GMT", 30 * time.Second, true},
		"rfc850 date":       {"Thursday, 02-Jan-25 03:05:05 GMT", time.Minute, true},
		"ansi c date":       {"Thu Jan  2 03:04:15 2025", 10 * time.Second, true},
		"http date in past": {"Thu, 02 Jan 2025 03:00:00 GMT", 0, true},
		"bad date":          {"Thu, 32 Jan 2025 03:04:35 GMT", 0, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := parseRetryAfter(tt.value, now)
			require.Equal(t, tt.valid, ok)
			require.Equal(t, tt.expect, got)
		})
	}
}

func TestIsRetriable(t *testing.T) {
	respErr := func(status int, retryAfter string) error {
		return fmt.Errorf("wrapped: %w", &responseError{
			err:        errors.New("bad status"),
			statusCode: status,
			retryAfter: retryAfter,
		})
	}
	for name, tt := range map[string]struct {
		err   error
		retry bool
		wait  time.Duration
	}{
		"transport":               {&transportError{err: errors.New("conn reset")}, true, 0},
		"wrapped transport":       {fmt.Errorf("a: %w", &transportError{err: errors.New("conn reset")}), true, 0},
		"other error":             {errors.New("nope"), false, 0},
		"context canceled":        {stdctx.Canceled, false, 0},
		"408":                     {respErr(http.StatusRequestTimeout, ""), true, 0},
		"429":                     {respErr(http.StatusTooManyRequests, ""), true, 0},
		"429 with retry after":    {respErr(http.StatusTooManyRequests, "7"), true, 7 * time.Second},
		"429 with bad retryafter": {respErr(http.StatusTooManyRequests, "later"), true, 0},
		"500":                     {respErr(http.StatusInternalServerError, ""), true, 0},
		"500 ignores retry after": {respErr(http.StatusInternalServerError, "7"), true, 0},
		"502":                     {respErr(http.StatusBadGateway, ""), true, 0},
		"503":                     {respErr(http.StatusServiceUnavailable, ""), true, 0},
		"503 with retry after":    {respErr(http.StatusServiceUnavailable, "3"), true, 3 * time.Second},
		"504":                     {respErr(http.StatusGatewayTimeout, "7"), true, 0},
		"400":                     {respErr(http.StatusBadRequest, ""), false, 0},
		"401":                     {respErr(http.StatusUnauthorized, ""), false, 0},
		"404":                     {respErr(http.StatusNotFound, ""), false, 0},
		"501":                     {respErr(http.StatusNotImplemented, ""), false, 0},
		"201 rejected by checker": {respErr(http.StatusCreated, ""), false, 0},
	} {
		t.Run(name, func(t *testing.T) {
			retry, wait := isRetriable(tt.err)
			require.Equal(t, tt.retry, retry)
			require.Equal(t, tt.wait, wait)
		})
	}
}

// recordingServer is an HTTP server that records the bodies it received for
// each path, and lets the test decide how to respond to each request.
type recordingServer struct {
	*httptest.Server
	mu     sync.Mutex
	bodies map[string][]string
}

func newRecordingServer(t *testing.T, respond func(w http.ResponseWriter, r *http.Request, attempt int)) *recordingServer {
	t.Helper()
	s := &recordingServer{bodies: map[string][]string{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bts, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.bodies[r.URL.Path] = append(s.bodies[r.URL.Path], string(bts))
		attempt := len(s.bodies[r.URL.Path])
		s.mu.Unlock()
		respond(w, r, attempt)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *recordingServer) received(path string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bodies[path]
}

var is2xx ResponseChecker = func(r *http.Response) error {
	if r.StatusCode/100 == 2 {
		return nil
	}
	return fmt.Errorf("unexpected http status code: %v", r.StatusCode)
}

// retryCtx creates a context with two archives with different contents.
func retryCtx(t *testing.T) (*context.Context, *artifact.Artifact, *artifact.Artifact) {
	t.Helper()
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{ProjectName: "proj"})
	dir := t.TempDir()
	add := func(name, content string) *artifact.Artifact {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
		a := &artifact.Artifact{
			Name: name,
			Path: path,
			Type: artifact.UploadableArchive,
		}
		ctx.Artifacts.Add(a)
		return a
	}
	return ctx, add("a.tar.gz", "content of a"), add("b.tar.gz", "content of b, which is longer")
}

func retryUpload(srv *httptest.Server, retry config.Retry) config.Upload {
	return config.Upload{
		Name:   "prod",
		Mode:   ModeArchive,
		Method: http.MethodPut,
		Target: srv.URL + "/{{ .ProjectName }}/",
		Retry:  retry,
	}
}

func publishAttempts(tb testing.TB, a *artifact.Artifact) []artifact.PublishAttempt {
	tb.Helper()
	return artifact.ExtraOr(*a, artifact.ExtraPublishAttempts, []artifact.PublishAttempt(nil))
}

func TestUploadRetries(t *testing.T) {
	srv := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request, attempt int) {
		switch {
		case r.URL.Path == "/proj/a.tar.gz" && attempt == 1:
			w.WriteHeader(http.StatusServiceUnavailable)
		case r.URL.Path == "/proj/a.tar.gz" && attempt == 2:
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusCreated)
		}
	})
	ctx, a, b := retryCtx(t)

	require.NoError(t, Upload(ctx, []config.Upload{retryUpload(srv.Server, config.Retry{
		Attempts: 3,
		Delay:    time.Millisecond,
	})}, "upload", is2xx))

	require.Equal(t, []string{"content of a", "content of a", "content of a"}, srv.received("/proj/a.tar.gz"))
	require.Equal(t, []string{"content of b, which is longer"}, srv.received("/proj/b.tar.gz"))

	require.Equal(t, []artifact.PublishAttempt{
		{
			Publisher: "upload",
			Instance:  "prod",
			Target:    srv.URL + "/proj/a.tar.gz",
			Attempt:   1,
			Status:    artifact.PublishAttemptFailure,
			Error:     "unexpected http status code: 503",
		},
		{
			Publisher: "upload",
			Instance:  "prod",
			Target:    srv.URL + "/proj/a.tar.gz",
			Attempt:   2,
			Status:    artifact.PublishAttemptFailure,
			Error:     "unexpected http status code: 500",
		},
		{
			Publisher: "upload",
			Instance:  "prod",
			Target:    srv.URL + "/proj/a.tar.gz",
			Attempt:   3,
			Status:    artifact.PublishAttemptSuccess,
		},
	}, publishAttempts(t, a))
	require.Equal(t, []artifact.PublishAttempt{
		{
			Publisher: "upload",
			Instance:  "prod",
			Target:    srv.URL + "/proj/b.tar.gz",
			Attempt:   1,
			Status:    artifact.PublishAttemptSuccess,
		},
	}, publishAttempts(t, b))
}

func TestUploadRetriesAllStatuses(t *testing.T) {
	for _, status := range []int{
		http.StatusRequestTimeout,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			srv := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request, attempt int) {
				if attempt == 1 {
					w.WriteHeader(status)
					return
				}
				w.WriteHeader(http.StatusCreated)
			})
			ctx, a, _ := retryCtx(t)
			require.NoError(t, Upload(ctx, []config.Upload{retryUpload(srv.Server, config.Retry{
				Attempts: 2,
			})}, "upload", is2xx))
			require.Len(t, srv.received("/proj/a.tar.gz"), 2)
			require.Len(t, publishAttempts(t, a), 2)
		})
	}
}

func TestUploadDoesNotRetry(t *testing.T) {
	for name, tt := range map[string]struct {
		status int
		retry  config.Retry
	}{
		"no retry config":       {http.StatusServiceUnavailable, config.Retry{}},
		"single attempt":        {http.StatusServiceUnavailable, config.Retry{Attempts: 1}},
		"bad request":           {http.StatusBadRequest, config.Retry{Attempts: 3}},
		"unauthorized":          {http.StatusUnauthorized, config.Retry{Attempts: 3}},
		"not found":             {http.StatusNotFound, config.Retry{Attempts: 3}},
		"conflict":              {http.StatusConflict, config.Retry{Attempts: 3}},
		"not implemented":       {http.StatusNotImplemented, config.Retry{Attempts: 3}},
		"version not supported": {http.StatusHTTPVersionNotSupported, config.Retry{Attempts: 3}},
	} {
		t.Run(name, func(t *testing.T) {
			srv := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
				w.WriteHeader(tt.status)
			})
			ctx, a, _ := retryCtx(t)
			err := Upload(ctx, []config.Upload{retryUpload(srv.Server, tt.retry)}, "upload", is2xx)
			require.EqualError(t, err, fmt.Sprintf("prod: upload: upload failed: unexpected http status code: %d", tt.status))
			require.Len(t, srv.received("/proj/a.tar.gz"), 1)
			require.Equal(t, []artifact.PublishAttempt{{
				Publisher: "upload",
				Instance:  "prod",
				Target:    srv.URL + "/proj/a.tar.gz",
				Attempt:   1,
				Status:    artifact.PublishAttemptFailure,
				Error:     fmt.Sprintf("unexpected http status code: %d", tt.status),
			}}, publishAttempts(t, a))
		})
	}
}

func TestUploadRetriesExhausted(t *testing.T) {
	srv := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.WriteHeader(http.StatusBadGateway)
	})
	ctx, a, b := retryCtx(t)
	err := Upload(ctx, []config.Upload{retryUpload(srv.Server, config.Retry{
		Attempts: 3,
		Delay:    time.Millisecond,
	})}, "upload", is2xx)
	require.EqualError(t, err, "prod: upload: upload failed: unexpected http status code: 502")
	for _, art := range []*artifact.Artifact{a, b} {
		require.Len(t, srv.received("/proj/"+art.Name), 3)
		attempts := publishAttempts(t, art)
		require.Len(t, attempts, 3)
		for i, attempt := range attempts {
			require.Equal(t, i+1, attempt.Attempt)
			require.Equal(t, artifact.PublishAttemptFailure, attempt.Status)
			require.Equal(t, "unexpected http status code: 502", attempt.Error)
		}
	}
}

func TestUploadRetriesTransportErrors(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())

	ctx, a, _ := retryCtx(t)
	err = Upload(ctx, []config.Upload{{
		Name:   "prod",
		Mode:   ModeArchive,
		Method: http.MethodPut,
		Target: "http://" + addr + "/{{ .ProjectName }}/",
		Retry:  config.Retry{Attempts: 3},
	}}, "upload", is2xx)
	require.ErrorContains(t, err, "prod: upload: upload failed: Put \"http://"+addr+"/proj/")

	attempts := publishAttempts(t, a)
	require.Len(t, attempts, 3)
	for i, attempt := range attempts {
		require.Equal(t, "http://"+addr+"/proj/a.tar.gz", attempt.Target)
		require.Equal(t, i+1, attempt.Attempt)
		require.Equal(t, artifact.PublishAttemptFailure, attempt.Status)
		require.Contains(t, attempt.Error, "connect")
	}
}

func TestUploadRetriesExtraFiles(t *testing.T) {
	srv := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request, attempt int) {
		if attempt < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})
	ctx, _, _ := retryCtx(t)
	upload := retryUpload(srv.Server, config.Retry{Attempts: 3})
	upload.ExtraFilesOnly = true
	upload.ExtraFiles = []config.ExtraFile{{Glob: "testdata/foo.txt"}}
	require.NoError(t, Upload(ctx, []config.Upload{upload}, "upload", is2xx))

	content, err := os.ReadFile("testdata/foo.txt")
	require.NoError(t, err)
	require.Equal(t, []string{string(content), string(content), string(content)}, srv.received("/proj/foo.txt"))
}

func TestUploadRetryAfter(t *testing.T) {
	t.Run("is honored", func(t *testing.T) {
		srv := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request, attempt int) {
			if attempt == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.WriteHeader(http.StatusCreated)
		})
		ctx, a, _ := retryCtx(t)
		start := time.Now()
		require.NoError(t, Upload(ctx, []config.Upload{retryUpload(srv.Server, config.Retry{
			Attempts: 2,
			Delay:    time.Millisecond,
		})}, "upload", is2xx))
		require.GreaterOrEqual(t, time.Since(start), time.Second)
		require.Len(t, publishAttempts(t, a), 2)
	})

	t.Run("is capped by max delay", func(t *testing.T) {
		srv := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request, attempt int) {
			if attempt == 1 {
				w.Header().Set("Retry-After", "3600")
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusCreated)
		})
		ctx, a, _ := retryCtx(t)
		start := time.Now()
		require.NoError(t, Upload(ctx, []config.Upload{retryUpload(srv.Server, config.Retry{
			Attempts: 2,
			MaxDelay: 10 * time.Millisecond,
		})}, "upload", is2xx))
		require.Less(t, time.Since(start), time.Minute)
		require.Len(t, publishAttempts(t, a), 2)
	})

	t.Run("is ignored for other statuses", func(t *testing.T) {
		srv := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request, attempt int) {
			if attempt == 1 {
				w.Header().Set("Retry-After", "3600")
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			w.WriteHeader(http.StatusCreated)
		})
		ctx, a, _ := retryCtx(t)
		start := time.Now()
		require.NoError(t, Upload(ctx, []config.Upload{retryUpload(srv.Server, config.Retry{
			Attempts: 2,
		})}, "upload", is2xx))
		require.Less(t, time.Since(start), time.Minute)
		require.Len(t, publishAttempts(t, a), 2)
	})
}

func TestUploadRetryContextCanceled(t *testing.T) {
	cctx, cancel := stdctx.WithCancel(t.Context())
	defer cancel()
	srv := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		cancel()
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	ctx := testctx.WrapWithCfg(cctx, config.Project{ProjectName: "proj"})
	path := filepath.Join(t.TempDir(), "a.tar.gz")
	require.NoError(t, os.WriteFile(path, []byte("a"), 0o644))
	a := &artifact.Artifact{Name: "a.tar.gz", Path: path, Type: artifact.UploadableArchive}
	ctx.Artifacts.Add(a)

	start := time.Now()
	err := Upload(ctx, []config.Upload{retryUpload(srv.Server, config.Retry{
		Attempts: 10,
		Delay:    time.Hour,
	})}, "upload", is2xx)
	require.ErrorIs(t, err, stdctx.Canceled)
	require.Less(t, time.Since(start), time.Minute)
	require.Len(t, srv.received("/proj/a.tar.gz"), 1)

	attempts := publishAttempts(t, a)
	require.Len(t, attempts, 1)
	require.Equal(t, artifact.PublishAttemptFailure, attempts[0].Status)
}
