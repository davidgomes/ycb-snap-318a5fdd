package artifactory

import (
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

func TestPublishRetriesServiceUnavailable(t *testing.T) {
	body := []byte("art-bytes")
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, body, got)
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"errors":[{"status":503,"message":"busy"}]}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"repo":"example"}`)
	}))
	t.Cleanup(srv.Close)

	ctx := artifactoryCtx(t, srv.URL+"/repo/obj", body, config.Retry{
		Attempts: 3,
		Delay:    time.Millisecond,
		MaxDelay: 20 * time.Millisecond,
	})
	require.NoError(t, Pipe{}.Publish(ctx))
	require.Equal(t, int32(2), hits.Load())
	require.Equal(t, "artifactory", ctx.Extra.PublishAttempts[0].Publisher)
	require.Equal(t, "production", ctx.Extra.PublishAttempts[0].Instance)
	require.Equal(t, srv.URL+"/repo/obj", ctx.Extra.PublishAttempts[0].Target)
	require.Equal(t, context.PublishStatusFailure, ctx.Extra.PublishAttempts[0].Status)
	require.NotEmpty(t, ctx.Extra.PublishAttempts[0].Error)
	require.Equal(t, context.PublishStatusSuccess, ctx.Extra.PublishAttempts[1].Status)
	require.Empty(t, ctx.Extra.PublishAttempts[1].Error)
}

func TestPublishDoesNotRetryUnauthorized(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"errors":[{"status":401,"message":"nope"}]}`)
	}))
	t.Cleanup(srv.Close)

	ctx := artifactoryCtx(t, srv.URL+"/repo/obj", []byte("x"), config.Retry{Attempts: 4, Delay: time.Millisecond})
	require.Error(t, Pipe{}.Publish(ctx))
	require.Equal(t, int32(1), hits.Load())
	require.Len(t, ctx.Extra.PublishAttempts, 1)
	require.Equal(t, context.PublishStatusFailure, ctx.Extra.PublishAttempts[0].Status)
}

func artifactoryCtx(t *testing.T, target string, body []byte, retry config.Retry) *context.Context {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact.bin")
	require.NoError(t, os.WriteFile(path, body, 0o644))
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{
		Artifactories: []config.Upload{{
			Name:               "production",
			Mode:               "binary",
			Method:             http.MethodPut,
			Target:             target,
			CustomArtifactName: true,
			Retry:              retry,
			Username:           "deploy",
			Password:           "secret",
		}},
	})
	ctx.Artifacts.Add(&artifact.Artifact{
		Name: "artifact.bin",
		Path: path,
		Type: artifact.UploadableBinary,
	})
	return ctx
}
