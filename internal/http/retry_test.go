package http

import (
	stdctx "context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	t.Run("delta-seconds", func(t *testing.T) {
		d, ok := parseRetryAfter(http.Header{"Retry-After": []string{"2"}})
		require.True(t, ok)
		require.Equal(t, 2*time.Second, d)
	})
	t.Run("negative", func(t *testing.T) {
		_, ok := parseRetryAfter(http.Header{"Retry-After": []string{"-1"}})
		require.False(t, ok)
	})
	t.Run("http-date", func(t *testing.T) {
		when := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
		d, ok := parseRetryAfter(http.Header{"Retry-After": []string{when}})
		require.True(t, ok)
		require.Greater(t, d, time.Duration(0))
		require.Less(t, d, time.Minute)
	})
	t.Run("invalid", func(t *testing.T) {
		_, ok := parseRetryAfter(http.Header{"Retry-After": []string{"soon"}})
		require.False(t, ok)
	})
	t.Run("missing", func(t *testing.T) {
		_, ok := parseRetryAfter(http.Header{})
		require.False(t, ok)
	})
}

func TestUploadRetriesTransientHTTP(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, "full-body", string(body))
		n := hits.Add(1)
		if n < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)

	ctx, art := retryUploadCtx(t)
	err := Upload(ctx, []config.Upload{{
		Name:   "prod",
		Mode:   ModeArchive,
		Target: srv.URL + "/",
		Retry:  config.Retry{Attempts: 3, Delay: time.Millisecond, MaxDelay: time.Second},
	}}, "upload", is2xxForRetry)
	require.NoError(t, err)
	require.Equal(t, int32(3), hits.Load())

	got := artifact.ExtraOr(*art, artifact.ExtraPublishAttempts, []artifact.PublishAttempt{})
	require.Equal(t, []artifact.PublishAttempt{
		{Publisher: "upload", Instance: "prod", Target: srv.URL + "/" + art.Name, Attempt: 1, Status: artifact.PublishAttemptFailure, Error: "unexpected http status code: 503"},
		{Publisher: "upload", Instance: "prod", Target: srv.URL + "/" + art.Name, Attempt: 2, Status: artifact.PublishAttemptFailure, Error: "unexpected http status code: 503"},
		{Publisher: "upload", Instance: "prod", Target: srv.URL + "/" + art.Name, Attempt: 3, Status: artifact.PublishAttemptSuccess},
	}, got)
}

func TestUploadDoesNotRetryClientErrors(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	ctx, art := retryUploadCtx(t)
	err := Upload(ctx, []config.Upload{{
		Name:   "prod",
		Mode:   ModeArchive,
		Target: srv.URL + "/",
		Retry:  config.Retry{Attempts: 5, Delay: time.Millisecond},
	}}, "upload", is2xxForRetry)
	require.Error(t, err)
	require.Equal(t, int32(1), hits.Load())

	got := artifact.ExtraOr(*art, artifact.ExtraPublishAttempts, []artifact.PublishAttempt{})
	require.Len(t, got, 1)
	require.Equal(t, artifact.PublishAttemptFailure, got[0].Status)
	require.NotEmpty(t, got[0].Error)
}

func TestUploadRetryStopsOnCancel(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	parent, cancel := stdctx.WithCancel(t.Context())
	ctx, _ := retryUploadCtxWith(t, parent)
	go func() {
		<-started
		cancel()
	}()

	err := Upload(ctx, []config.Upload{{
		Name:   "prod",
		Mode:   ModeArchive,
		Target: srv.URL + "/",
		Retry:  config.Retry{Attempts: 5, Delay: time.Hour},
	}}, "upload", is2xxForRetry)
	require.ErrorIs(t, err, stdctx.Canceled)
}

func TestUploadRetryExtraFilesAndSort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)

	ctx, art := retryUploadCtx(t)
	err := Upload(ctx, []config.Upload{
		{
			Name:   "zulu",
			Mode:   ModeArchive,
			Target: srv.URL + "/z/",
			Retry:  config.Retry{Attempts: 1},
		},
		{
			Name:   "alpha",
			Mode:   ModeArchive,
			Target: srv.URL + "/a/",
			Retry:  config.Retry{Attempts: 1},
			ExtraFiles: []config.ExtraFile{{
				Glob: "testdata/*.txt",
			}},
			ExtraFilesOnly: true,
		},
	}, "artifactory", is2xxForRetry)
	require.NoError(t, err)

	got := artifact.ExtraOr(*art, artifact.ExtraPublishAttempts, []artifact.PublishAttempt{})
	require.Equal(t, "artifactory", got[0].Publisher)
	require.Equal(t, "zulu", got[0].Instance)

	var extraArt *artifact.Artifact
	for _, a := range ctx.Artifacts.List() {
		if a.Type == artifact.UploadableFile {
			extraArt = a
			break
		}
	}
	require.NotNil(t, extraArt)
	extraAttempts := artifact.ExtraOr(*extraArt, artifact.ExtraPublishAttempts, []artifact.PublishAttempt{})
	require.Equal(t, []artifact.PublishAttempt{{
		Publisher: "artifactory",
		Instance:  "alpha",
		Target:    srv.URL + "/a/" + extraArt.Name,
		Attempt:   1,
		Status:    artifact.PublishAttemptSuccess,
	}}, extraAttempts)
}

func TestUploadRetry429UsesRetryAfter(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	ctx, _ := retryUploadCtx(t)
	err := Upload(ctx, []config.Upload{{
		Name:   "prod",
		Mode:   ModeArchive,
		Target: srv.URL + "/",
		Retry:  config.Retry{Attempts: 2, Delay: time.Millisecond, MaxDelay: time.Second},
	}}, "upload", is2xxForRetry)
	require.NoError(t, err)
	require.Equal(t, int32(2), hits.Load())
}

var is2xxForRetry ResponseChecker = func(r *http.Response) error {
	if r.StatusCode/100 == 2 {
		return nil
	}
	return fmt.Errorf("unexpected http status code: %v", r.StatusCode)
}

func retryUploadCtx(t *testing.T) (*context.Context, *artifact.Artifact) {
	t.Helper()
	return retryUploadCtxWith(t, t.Context())
}

func retryUploadCtxWith(t *testing.T, parent stdctx.Context) (*context.Context, *artifact.Artifact) {
	t.Helper()
	ctx := testctx.WrapWithCfg(parent, config.Project{ProjectName: "p"}, testctx.WithVersion("1.0.0"))
	file := filepath.Join(t.TempDir(), "a.tar.gz")
	require.NoError(t, os.WriteFile(file, []byte("full-body"), 0o644))
	art := &artifact.Artifact{
		Name: "a.tar.gz",
		Path: file,
		Type: artifact.UploadableArchive,
	}
	ctx.Artifacts.Add(art)
	return ctx, art
}
