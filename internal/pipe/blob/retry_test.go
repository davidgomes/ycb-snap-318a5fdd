package blob

import (
	stdctx "context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/goreleaser/goreleaser/v2/internal/artifact"
	"github.com/goreleaser/goreleaser/v2/internal/testctx"
	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/goreleaser/goreleaser/v2/pkg/context"
	"github.com/stretchr/testify/require"
)

type timeoutErr struct{ msg string }

func (e timeoutErr) Error() string { return e.msg }
func (e timeoutErr) Timeout() bool { return true }

type temporaryErr struct{ msg string }

func (e temporaryErr) Error() string   { return e.msg }
func (e temporaryErr) Temporary() bool { return true }

type steadyErr struct{ msg string }

func (e steadyErr) Error() string   { return e.msg }
func (e steadyErr) Timeout() bool   { return false }
func (e steadyErr) Temporary() bool { return false }

type scriptedUploader struct {
	mu         sync.Mutex
	openErrs   []error
	uploadErrs []error
	opens      int
	uploads    int
	bodies     [][]byte
	paths      []string
	onUpload   func(n int)
}

func (u *scriptedUploader) Close() error { return nil }

func (u *scriptedUploader) Open(*context.Context, string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.opens++
	if len(u.openErrs) == 0 {
		return nil
	}
	err := u.openErrs[0]
	u.openErrs = u.openErrs[1:]
	return err
}

func (u *scriptedUploader) Upload(_ *context.Context, object string, data []byte) error {
	u.mu.Lock()
	n := u.uploads + 1
	u.uploads = n
	u.paths = append(u.paths, object)
	u.bodies = append(u.bodies, append([]byte(nil), data...))
	var err error
	if len(u.uploadErrs) > 0 {
		err = u.uploadErrs[0]
		u.uploadErrs = u.uploadErrs[1:]
	}
	hook := u.onUpload
	u.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	return err
}

func TestBlobOpenRetriesAreNotPublishAttempts(t *testing.T) {
	up := &scriptedUploader{
		openErrs: []error{timeoutErr{"timed out"}, timeoutErr{"timed out"}, nil},
	}
	ctx, bin := blobCtx(t, config.Retry{Attempts: 5, Delay: time.Millisecond})
	require.NoError(t, publishBlob(ctx, ctx.Config.Blobs[0], up, "s3://rel?region=us-east-1", "dist", "s3://rel"))
	require.Equal(t, 3, up.opens)
	require.Equal(t, 1, up.uploads)
	require.Equal(t, []context.PublishAttempt{
		{
			Publisher: "blob",
			Instance:  "s3://rel",
			Target:    path.Join("dist", "app.bin"),
			Attempt:   1,
			Status:    context.PublishStatusSuccess,
		},
	}, ctx.Extra.PublishAttempts)
	require.Equal(t, string(mustRead(t, bin)), string(up.bodies[0]))
}

func TestBlobUploadRetriesTransientErrorsAndResendsBody(t *testing.T) {
	up := &scriptedUploader{
		uploadErrs: []error{
			fmt.Errorf("wrapped: %w", timeoutErr{"timed out"}),
			temporaryErr{"temporary"},
			nil,
		},
	}
	ctx, bin := blobCtx(t, config.Retry{Attempts: 4, Delay: time.Millisecond, MaxDelay: 5 * time.Millisecond})
	require.NoError(t, publishBlob(ctx, ctx.Config.Blobs[0], up, "s3://rel", "dist", "s3://rel"))
	require.Equal(t, 3, up.uploads)
	payload := mustRead(t, bin)
	for _, body := range up.bodies {
		require.Equal(t, payload, body)
	}
	require.Equal(t, context.PublishStatusFailure, ctx.Extra.PublishAttempts[0].Status)
	require.Contains(t, ctx.Extra.PublishAttempts[0].Error, "timed out")
	require.Equal(t, context.PublishStatusFailure, ctx.Extra.PublishAttempts[1].Status)
	require.Contains(t, ctx.Extra.PublishAttempts[1].Error, "temporary")
	require.Equal(t, context.PublishStatusSuccess, ctx.Extra.PublishAttempts[2].Status)
	require.Empty(t, ctx.Extra.PublishAttempts[2].Error)
	require.Equal(t, 2, ctx.Extra.PublishAttempts[1].Attempt)
	require.Equal(t, 3, ctx.Extra.PublishAttempts[2].Attempt)
}

func TestBlobDoesNotRetrySteadyErrors(t *testing.T) {
	up := &scriptedUploader{uploadErrs: []error{steadyErr{"nope"}}}
	ctx, _ := blobCtx(t, config.Retry{Attempts: 5, Delay: time.Millisecond})
	err := publishBlob(ctx, ctx.Config.Blobs[0], up, "s3://rel", "dist", "s3://rel")
	require.ErrorContains(t, err, "nope")
	require.Equal(t, 1, up.uploads)
	require.Len(t, ctx.Extra.PublishAttempts, 1)
	require.Equal(t, context.PublishStatusFailure, ctx.Extra.PublishAttempts[0].Status)
}

func TestBlobOpenSteadyErrorIsNotAPublishAttempt(t *testing.T) {
	up := &scriptedUploader{openErrs: []error{steadyErr{"missing"}}}
	ctx, _ := blobCtx(t, config.Retry{Attempts: 4, Delay: time.Millisecond})
	err := publishBlob(ctx, ctx.Config.Blobs[0], up, "gs://rel", "dist", "gs://rel")
	require.ErrorContains(t, err, "missing")
	require.Equal(t, 1, up.opens)
	require.Empty(t, ctx.Extra.PublishAttempts)
	require.Zero(t, up.uploads)
}

func TestBlobMaxDelayCapsUploadWait(t *testing.T) {
	up := &scriptedUploader{uploadErrs: []error{timeoutErr{"timed out"}, nil}}
	ctx, _ := blobCtx(t, config.Retry{Attempts: 2, Delay: time.Minute, MaxDelay: 30 * time.Millisecond})
	start := time.Now()
	require.NoError(t, publishBlob(ctx, ctx.Config.Blobs[0], up, "s3://rel", "dist", "s3://rel"))
	require.Less(t, time.Since(start), time.Second)
	require.Equal(t, 2, up.uploads)
}

func TestBlobUploadStopsOnContextCancel(t *testing.T) {
	parent, cancel := stdctx.WithCancel(t.Context())
	t.Cleanup(cancel)
	up := &scriptedUploader{
		uploadErrs: []error{timeoutErr{"timed out"}},
		onUpload: func(int) {
			cancel()
		},
	}
	ctx, _ := blobCtx(t, config.Retry{Attempts: 4, Delay: time.Hour})
	ctx.Context = parent
	err := publishBlob(ctx, ctx.Config.Blobs[0], up, "s3://rel", "dist", "s3://rel")
	require.ErrorIs(t, err, stdctx.Canceled)
	require.Equal(t, 1, up.uploads)
	require.Len(t, ctx.Extra.PublishAttempts, 1)
}

func TestBlobOpenStopsOnContextCancel(t *testing.T) {
	parent, cancel := stdctx.WithCancel(t.Context())
	up := &scriptedUploader{openErrs: []error{stdctx.Canceled}}
	ctx, _ := blobCtx(t, config.Retry{Attempts: 4, Delay: time.Hour})
	ctx.Context = parent
	cancel()
	err := publishBlob(ctx, ctx.Config.Blobs[0], up, "s3://rel", "dist", "s3://rel")
	require.ErrorIs(t, err, stdctx.Canceled)
	require.Empty(t, ctx.Extra.PublishAttempts)
}

func TestBlobExtraFileAndInstanceAreRecordedInOrder(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("notes.txt", []byte("notes"), 0o644))

	up := &scriptedUploader{}
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{
		Blobs: []config.Blob{{
			Provider: "{{ .Env.PROVIDER }}",
			Bucket:   "{{ .Env.BUCKET }}",
			ExtraFiles: []config.ExtraFile{{
				Glob: "./notes.txt",
			}},
			Retry: config.Retry{Attempts: 1},
		}},
	})
	ctx.Env["PROVIDER"] = "azblob"
	ctx.Env["BUCKET"] = "releases"
	bin := filepath.Join(dir, "app.bin")
	require.NoError(t, os.WriteFile(bin, []byte("bin"), 0o644))
	ctx.Artifacts.Add(&artifact.Artifact{
		Name: "app.bin",
		Path: bin,
		Type: artifact.UploadableBinary,
	})

	instance, err := blobInstance(ctx, ctx.Config.Blobs[0])
	require.NoError(t, err)
	require.Equal(t, "azblob://releases", instance)
	require.NoError(t, publishBlob(ctx, ctx.Config.Blobs[0], up, instance, "out", instance))

	require.Equal(t, []context.PublishAttempt{
		{Publisher: "blob", Instance: instance, Target: path.Join("out", "app.bin"), Attempt: 1, Status: context.PublishStatusSuccess},
		{Publisher: "blob", Instance: instance, Target: path.Join("out", "notes.txt"), Attempt: 1, Status: context.PublishStatusSuccess},
	}, ctx.Extra.PublishAttempts)
}

func blobCtx(t *testing.T, retry config.Retry) (*context.Context, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "app.bin")
	require.NoError(t, os.WriteFile(bin, []byte("payload-v1"), 0o644))
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{
		Blobs: []config.Blob{{
			Provider: "s3",
			Bucket:   "rel",
			Retry:    retry,
		}},
	})
	ctx.Artifacts.Add(&artifact.Artifact{
		Name: "app.bin",
		Path: bin,
		Type: artifact.UploadableBinary,
	})
	return ctx, bin
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

func TestIsTransient(t *testing.T) {
	require.True(t, isTransient(timeoutErr{"x"}))
	require.True(t, isTransient(fmt.Errorf("wrap: %w", temporaryErr{"y"})))
	require.False(t, isTransient(steadyErr{"z"}))
	require.False(t, isTransient(errors.New("plain")))
	require.False(t, isTransient(nil))
}
