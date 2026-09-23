package blob

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	stdctx "context"

	"github.com/goreleaser/goreleaser/v2/internal/artifact"
	"github.com/goreleaser/goreleaser/v2/internal/retry"
	"github.com/goreleaser/goreleaser/v2/internal/testctx"
	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/goreleaser/goreleaser/v2/pkg/context"
	"github.com/stretchr/testify/require"
	"gocloud.dev/blob"

	_ "gocloud.dev/blob/fileblob"
)

type fakeUploader struct {
	mu         sync.Mutex
	openErrs   []error
	uploadErrs []error
	opens      int
	uploads    [][]byte
	paths      []string
	onUpload   func()
}

func (f *fakeUploader) Close() error { return nil }

func (f *fakeUploader) Open(*context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opens++
	if len(f.openErrs) == 0 {
		return nil
	}
	err := f.openErrs[0]
	f.openErrs = f.openErrs[1:]
	return err
}

func (f *fakeUploader) Upload(_ *context.Context, object string, data []byte) error {
	if f.onUpload != nil {
		f.onUpload()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, object)
	f.uploads = append(f.uploads, append([]byte(nil), data...))
	if len(f.uploadErrs) == 0 {
		return nil
	}
	err := f.uploadErrs[0]
	f.uploadErrs = f.uploadErrs[1:]
	return err
}

type temporaryError struct{ msg string }

func (e temporaryError) Error() string   { return e.msg }
func (e temporaryError) Temporary() bool { return true }

type timeoutError struct{ msg string }

func (e timeoutError) Error() string { return e.msg }
func (e timeoutError) Timeout() bool { return true }

type permanentNetError struct{ msg string }

func (e permanentNetError) Error() string   { return e.msg }
func (e permanentNetError) Timeout() bool   { return false }
func (e permanentNetError) Temporary() bool { return false }

func TestBlobRetriesOpenWithoutAuditAndResendsUpload(t *testing.T) {
	orig := retry.Sleep
	t.Cleanup(func() { retry.Sleep = orig })
	var waits []time.Duration
	retry.Sleep = func(stdctx.Context, time.Duration) error {
		return nil
	}
	retry.Sleep = func(_ stdctx.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "bin.tar.gz")
	payload := []byte("blob-body-v1")
	require.NoError(t, os.WriteFile(src, payload, 0o644))

	up := &fakeUploader{
		openErrs: []error{
			temporaryError{msg: "open temp"},
			fmtWrap{temporaryError{msg: "open wrapped"}},
		},
		uploadErrs: []error{
			timeoutError{msg: "upload timeout"},
			fmtWrap{temporaryError{msg: "upload wrapped"}},
		},
	}
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{ProjectName: "proj"})
	art := &artifact.Artifact{Name: "bin.tar.gz", Path: src, Type: artifact.UploadableArchive}
	ctx.Artifacts.Add(art)
	conf := config.Blob{
		Provider: "{{ .ProjectName }}-provider",
		Bucket:   "my-bucket",
		Retry:    config.Retry{Attempts: 5, Delay: time.Second, MaxDelay: 1500 * time.Millisecond},
	}
	require.NoError(t, openBucket(ctx, conf, up, "unused"))
	require.NoError(t, uploadData(ctx, conf, up, art, src, "proj/v1/bin.tar.gz", "unused", "proj-provider://my-bucket"))

	require.Equal(t, 3, up.opens)
	require.Equal(t, [][]byte{payload, payload, payload}, up.uploads)
	require.Equal(t, []string{"proj/v1/bin.tar.gz", "proj/v1/bin.tar.gz", "proj/v1/bin.tar.gz"}, up.paths)
	require.Equal(t, []time.Duration{
		time.Second,
		1500 * time.Millisecond,
		time.Second,
		1500 * time.Millisecond,
	}, waits)

	attempts := ctx.PublishAttempts()
	require.Len(t, attempts, 3)
	require.Equal(t, "blob", attempts[0].Publisher)
	require.Equal(t, "proj-provider://my-bucket", attempts[0].Instance)
	require.Equal(t, "proj/v1/bin.tar.gz", attempts[0].Target)
	require.Equal(t, 1, attempts[0].Attempt)
	require.Equal(t, context.PublishStatusFailure, attempts[0].Status)
	require.Equal(t, "upload timeout", attempts[0].Error)
	require.Equal(t, context.PublishStatusFailure, attempts[1].Status)
	require.Contains(t, attempts[1].Error, "upload wrapped")
	require.Equal(t, context.PublishStatusSuccess, attempts[2].Status)
	require.Empty(t, attempts[2].Error)

	perArt := art.Extra[context.ExtraPublishAttempts].([]context.PublishAttempt)
	require.Equal(t, attempts, perArt)

	raw, err := json.Marshal(attempts)
	require.NoError(t, err)
	require.NotContains(t, string(raw), `"error":""`)
}

func TestBlobDoesNotRetryPermanentOrFalseTransient(t *testing.T) {
	orig := retry.Sleep
	t.Cleanup(func() { retry.Sleep = orig })
	retry.Sleep = func(stdctx.Context, time.Duration) error {
		t.Fatal("sleep should not be called")
		return nil
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "a")
	require.NoError(t, os.WriteFile(src, []byte("a"), 0o644))
	up := &fakeUploader{uploadErrs: []error{permanentNetError{msg: "nope"}, errors.New("plain")}}
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{})
	art := &artifact.Artifact{Name: "a", Path: src, Type: artifact.UploadableArchive}
	conf := config.Blob{Provider: "s3", Bucket: "b", Directory: "dir", Retry: config.Retry{Attempts: 4, Delay: time.Second}}
	err := uploadData(ctx, conf, up, art, src, "dir/a", "s3://b", "s3://b")
	require.Error(t, err)
	require.Len(t, up.uploads, 1)
	require.Len(t, ctx.PublishAttempts(), 1)
	require.Equal(t, "nope", ctx.PublishAttempts()[0].Error)
}

func TestBlobOpenCancel(t *testing.T) {
	parent, cancel := stdctx.WithCancel(t.Context())
	cancel()
	ctx := testctx.WrapWithCfg(parent, config.Project{})
	up := &fakeUploader{openErrs: []error{temporaryError{msg: "later"}}}
	err := openBucket(ctx, config.Blob{Retry: config.Retry{Attempts: 3}}, up, "s3://b")
	require.ErrorIs(t, err, stdctx.Canceled)
	require.Empty(t, ctx.PublishAttempts())
	require.Equal(t, 0, up.opens)
}

func TestBlobUploadCancelDuringAttempt(t *testing.T) {
	parent, cancel := stdctx.WithCancel(t.Context())
	ctx := testctx.WrapWithCfg(parent, config.Project{})
	dir := t.TempDir()
	src := filepath.Join(dir, "a")
	require.NoError(t, os.WriteFile(src, []byte("abc"), 0o644))
	up := &fakeUploader{
		onUpload:   cancel,
		uploadErrs: []error{errors.New("canceled upload")},
	}
	err := uploadData(ctx, config.Blob{
		Provider: "gs",
		Bucket:   "buck",
		Retry:    config.Retry{Attempts: 4, Delay: time.Hour},
	}, up, nil, src, "obj", "gs://buck", "gs://buck")
	require.ErrorIs(t, err, stdctx.Canceled)
	require.Len(t, up.uploads, 1)
	require.Equal(t, []byte("abc"), up.uploads[0])
	attempts := ctx.PublishAttempts()
	require.Len(t, attempts, 1)
	require.Equal(t, context.PublishStatusFailure, attempts[0].Status)
	require.Equal(t, stdctx.Canceled.Error(), attempts[0].Error)
	require.Equal(t, "gs://buck", attempts[0].Instance)
	require.Equal(t, "obj", attempts[0].Target)
}

func TestBlobFileBucketAttemptsIncludeExtraFiles(t *testing.T) {
	bucketDir := t.TempDir()
	work := t.TempDir()
	payload := []byte("hello-blob")
	bin := filepath.Join(work, "bin.tar.gz")
	extra := filepath.Join(work, "extra.txt")
	require.NoError(t, os.WriteFile(bin, payload, 0o644))
	require.NoError(t, os.WriteFile(extra, []byte("extra"), 0o644))
	t.Chdir(work)

	ctx := testctx.WrapWithCfg(t.Context(), config.Project{
		ProjectName: "demo",
		Blobs: []config.Blob{{
			Provider:  "file",
			Bucket:    bucketDir,
			Directory: "{{ .ProjectName }}/{{ .Version }}",
			ExtraFiles: []config.ExtraFile{{
				Glob: "extra.txt",
			}},
		}},
	}, testctx.WithVersion("9.9.9"))
	art := &artifact.Artifact{
		Name: "bin.tar.gz",
		Path: bin,
		Type: artifact.UploadableArchive,
	}
	ctx.Artifacts.Add(art)
	require.NoError(t, Pipe{}.Publish(ctx))

	attempts := ctx.PublishAttempts()
	require.Len(t, attempts, 2)
	require.Equal(t, []context.PublishAttempt{
		{
			Publisher: "blob",
			Instance:  "file://" + bucketDir,
			Target:    "demo/9.9.9/bin.tar.gz",
			Attempt:   1,
			Status:    context.PublishStatusSuccess,
		},
		{
			Publisher: "blob",
			Instance:  "file://" + bucketDir,
			Target:    "demo/9.9.9/extra.txt",
			Attempt:   1,
			Status:    context.PublishStatusSuccess,
		},
	}, attempts)

	got, err := os.ReadFile(filepath.Join(bucketDir, "demo/9.9.9/bin.tar.gz"))
	require.NoError(t, err)
	require.Equal(t, payload, got)
	got, err = os.ReadFile(filepath.Join(bucketDir, "demo/9.9.9/extra.txt"))
	require.NoError(t, err)
	require.Equal(t, []byte("extra"), got)

	// Bucket open is not an upload attempt, including when it is retried.
	up := &fakeUploader{openErrs: []error{temporaryError{msg: "temp"}}}
	require.NoError(t, openBucket(ctx, config.Blob{Retry: config.Retry{Attempts: 2}}, up, "file://"+bucketDir))
	require.Len(t, ctx.PublishAttempts(), 2)
	require.Equal(t, 2, up.opens)
}

func TestPublishAttemptSortAcrossPublishers(t *testing.T) {
	ctx := testctx.Wrap(t.Context())
	ctx.AddPublishAttempt(context.PublishAttempt{Publisher: "upload", Instance: "b", Target: "t", Attempt: 2, Status: context.PublishStatusSuccess})
	ctx.AddPublishAttempt(context.PublishAttempt{Publisher: "blob", Instance: "s3://b", Target: "a", Attempt: 1, Status: context.PublishStatusFailure, Error: "x"})
	ctx.AddPublishAttempt(context.PublishAttempt{Publisher: "artifactory", Instance: "a", Target: "z", Attempt: 1, Status: context.PublishStatusSuccess})
	ctx.AddPublishAttempt(context.PublishAttempt{Publisher: "upload", Instance: "b", Target: "t", Attempt: 1, Status: context.PublishStatusFailure, Error: "nope"})
	got := ctx.PublishAttempts()
	require.Equal(t, []context.PublishAttempt{
		{Publisher: "artifactory", Instance: "a", Target: "z", Attempt: 1, Status: context.PublishStatusSuccess},
		{Publisher: "blob", Instance: "s3://b", Target: "a", Attempt: 1, Status: context.PublishStatusFailure, Error: "x"},
		{Publisher: "upload", Instance: "b", Target: "t", Attempt: 1, Status: context.PublishStatusFailure, Error: "nope"},
		{Publisher: "upload", Instance: "b", Target: "t", Attempt: 2, Status: context.PublishStatusSuccess},
	}, got)
	require.Equal(t, got, ctx.Extra[context.ExtraPublishAttempts])
}

type fmtWrap struct{ error }

func (e fmtWrap) Error() string { return "wrap: " + e.error.Error() }
func (e fmtWrap) Unwrap() error { return e.error }

func TestIsTransient(t *testing.T) {
	require.True(t, isTransient(temporaryError{msg: "t"}))
	require.True(t, isTransient(timeoutError{msg: "t"}))
	require.True(t, isTransient(fmtWrap{temporaryError{msg: "t"}}))
	require.False(t, isTransient(permanentNetError{msg: "p"}))
	require.False(t, isTransient(errors.New("plain")))
	require.False(t, isTransient(nil))
}

func TestFileBucketRoundTripReader(t *testing.T) {
	// Keep the file driver referenced for the publish test above.
	b, err := blob.OpenBucket(t.Context(), "file://"+t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, b.Close()) })
	w, err := b.NewWriter(t.Context(), "k", nil)
	require.NoError(t, err)
	_, err = io.Copy(w, bytes.NewReader([]byte("v")))
	require.NoError(t, err)
	require.NoError(t, w.Close())
}
