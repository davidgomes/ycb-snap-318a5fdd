package blob

import (
	stdctx "context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/goreleaser/goreleaser/v2/internal/artifact"
	"github.com/goreleaser/goreleaser/v2/internal/testctx"
	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/goreleaser/goreleaser/v2/pkg/context"
	"github.com/stretchr/testify/require"
)

type tempErr struct{ error }

func (tempErr) Temporary() bool { return true }

type timeoutErr struct{ error }

func (timeoutErr) Timeout() bool { return true }

type mockUploader struct {
	openErrs    []error
	uploadErrs  []error
	openCalls   int
	uploadCalls int
}

func (m *mockUploader) Close() error { return nil }

func (m *mockUploader) Open(_ *context.Context, _ string) error {
	i := m.openCalls
	m.openCalls++
	if i < len(m.openErrs) && m.openErrs[i] != nil {
		return m.openErrs[i]
	}
	return nil
}

func (m *mockUploader) Upload(_ *context.Context, _ string, data []byte) error {
	i := m.uploadCalls
	m.uploadCalls++
	if len(data) == 0 {
		return errors.New("empty payload")
	}
	if i < len(m.uploadErrs) && m.uploadErrs[i] != nil {
		return m.uploadErrs[i]
	}
	return nil
}

func TestIsTransientBlobErr(t *testing.T) {
	require.False(t, isTransientBlobErr(errors.New("nope")))
	require.True(t, isTransientBlobErr(tempErr{errors.New("tmp")}))
	require.True(t, isTransientBlobErr(timeoutErr{errors.New("to")}))
	require.False(t, isTransientBlobErr(stdctx.Canceled))
	require.False(t, isTransientBlobErr(stdctx.DeadlineExceeded))
}

func TestRetryBlobOpOpen(t *testing.T) {
	ctx := testctx.Wrap(t.Context())
	up := &mockUploader{openErrs: []error{tempErr{errors.New("tmp")}, nil}}
	require.NoError(t, retryBlobOp(ctx, config.Retry{Attempts: 3, Delay: time.Millisecond}, func() error {
		return up.Open(ctx, "s3://b")
	}))
	require.Equal(t, 2, up.openCalls)
}

func TestUploadDataRetriesAndRecords(t *testing.T) {
	ctx := testctx.Wrap(t.Context())
	file := filepath.Join(t.TempDir(), "a.bin")
	require.NoError(t, os.WriteFile(file, []byte("full-body"), 0o644))
	art := &artifact.Artifact{Name: "a.bin", Path: file, Type: artifact.UploadableArchive}
	ctx.Artifacts.Add(art)

	up := &mockUploader{uploadErrs: []error{timeoutErr{errors.New("slow")}, nil}}
	err := uploadData(ctx, config.Blob{Retry: config.Retry{Attempts: 3, Delay: time.Millisecond, MaxDelay: time.Second}}, up, art, file, "proj/tag/a.bin", "s3://bucket", "s3://bucket")
	require.NoError(t, err)
	require.Equal(t, 2, up.uploadCalls)

	got := artifact.ExtraOr(*art, artifact.ExtraPublishAttempts, []artifact.PublishAttempt{})
	require.Equal(t, []artifact.PublishAttempt{
		{Publisher: "blob", Instance: "s3://bucket", Target: "proj/tag/a.bin", Attempt: 1, Status: artifact.PublishAttemptFailure, Error: "slow"},
		{Publisher: "blob", Instance: "s3://bucket", Target: "proj/tag/a.bin", Attempt: 2, Status: artifact.PublishAttemptSuccess},
	}, got)
}

func TestUploadDataDoesNotRetryPermanent(t *testing.T) {
	ctx := testctx.Wrap(t.Context())
	file := filepath.Join(t.TempDir(), "a.bin")
	require.NoError(t, os.WriteFile(file, []byte("full-body"), 0o644))
	art := &artifact.Artifact{Name: "a.bin", Path: file}
	up := &mockUploader{uploadErrs: []error{errors.New("NoSuchBucket")}}
	err := uploadData(ctx, config.Blob{Retry: config.Retry{Attempts: 4, Delay: time.Millisecond}}, up, art, file, "a.bin", "gs://b", "gs://b")
	require.Error(t, err)
	require.Equal(t, 1, up.uploadCalls)
	got := artifact.ExtraOr(*art, artifact.ExtraPublishAttempts, []artifact.PublishAttempt{})
	require.Len(t, got, 1)
	require.Equal(t, artifact.PublishAttemptFailure, got[0].Status)
}

func TestUploadDataStopsOnCancel(t *testing.T) {
	parent, cancel := stdctx.WithCancel(t.Context())
	ctx := testctx.Wrap(parent)
	file := filepath.Join(t.TempDir(), "a.bin")
	require.NoError(t, os.WriteFile(file, []byte("full-body"), 0o644))
	art := &artifact.Artifact{Name: "a.bin", Path: file}
	up := &mockUploader{uploadErrs: []error{tempErr{errors.New("tmp")}}}
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	err := uploadData(ctx, config.Blob{Retry: config.Retry{Attempts: 8, Delay: time.Hour}}, up, art, file, "a.bin", "s3://b", "s3://b")
	require.ErrorIs(t, err, stdctx.Canceled)
}

func TestInstanceFromBucketURL(t *testing.T) {
	require.Equal(t, "s3://foo", instanceFromBucketURL("s3://foo?region=us"))
	require.Equal(t, "gs://foo", instanceFromBucketURL("gs://foo"))
}

func TestPublishAttemptsSortedAcrossInstances(t *testing.T) {
	ctx := testctx.Wrap(t.Context())
	file := filepath.Join(t.TempDir(), "a.bin")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
	art := &artifact.Artifact{Name: "a.bin", Path: file}
	up := &mockUploader{}
	require.NoError(t, uploadData(ctx, config.Blob{}, up, art, file, "z/a.bin", "s3://z", "s3://z"))
	require.NoError(t, uploadData(ctx, config.Blob{}, up, art, file, "a/a.bin", "s3://a", "s3://a"))
	got := artifact.ExtraOr(*art, artifact.ExtraPublishAttempts, []artifact.PublishAttempt{})
	require.Equal(t, "s3://a", got[0].Instance)
	require.Equal(t, "s3://z", got[1].Instance)
}
