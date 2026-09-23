package blob

import (
	"bytes"
	stdctx "context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/goreleaser/goreleaser/v2/internal/artifact"
	"github.com/goreleaser/goreleaser/v2/internal/testctx"
	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/goreleaser/goreleaser/v2/pkg/context"
	"github.com/stretchr/testify/require"
	"gocloud.dev/blob"
	"gocloud.dev/blob/driver"
	"gocloud.dev/gcerrors"
)

type temporaryError struct{ temporary bool }

func (e temporaryError) Error() string   { return fmt.Sprintf("temporary error (%v)", e.temporary) }
func (e temporaryError) Temporary() bool { return e.temporary }

type timeoutError struct{ timeout bool }

func (e timeoutError) Error() string { return fmt.Sprintf("timeout error (%v)", e.timeout) }
func (e timeoutError) Timeout() bool { return e.timeout }

func TestIsTransient(t *testing.T) {
	for name, tt := range map[string]struct {
		err    error
		expect bool
	}{
		"temporary":         {temporaryError{true}, true},
		"not temporary":     {temporaryError{false}, false},
		"timeout":           {timeoutError{true}, true},
		"not timeout":       {timeoutError{false}, false},
		"wrapped temporary": {fmt.Errorf("a: %w", temporaryError{true}), true},
		"wrapped timeout":   {fmt.Errorf("a: %w", timeoutError{true}), true},
		"joined":            {errors.Join(errors.New("a"), timeoutError{true}), true},
		"plain":             {errors.New("nope"), false},
		"context canceled":  {stdctx.Canceled, false},
	} {
		t.Run(name, func(t *testing.T) {
			retry, wait := isTransient(tt.err)
			require.Equal(t, tt.expect, retry)
			require.Zero(t, wait)
		})
	}
}

func TestInstanceFor(t *testing.T) {
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{ProjectName: "proj"})
	for name, tt := range map[string]struct {
		conf   config.Blob
		expect string
	}{
		"templates": {
			conf:   config.Blob{Provider: "{{ .ProjectName }}", Bucket: "{{ .ProjectName }}-bucket"},
			expect: "proj://proj-bucket",
		},
		"s3 without query params": {
			conf: config.Blob{
				Provider:   "s3",
				Bucket:     "bucket",
				Endpoint:   "https://minio.foo.bar",
				Region:     "us-west-1",
				DisableSSL: true,
			},
			expect: "s3://bucket",
		},
		"azblob with params in the bucket": {
			conf:   config.Blob{Provider: "azblob", Bucket: "releases?storage_account=foo"},
			expect: "azblob://releases?storage_account=foo",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := instanceFor(ctx, tt.conf)
			require.NoError(t, err)
			require.Equal(t, tt.expect, got)
		})
	}

	t.Run("bad templates", func(t *testing.T) {
		_, err := instanceFor(ctx, config.Blob{Provider: "s3", Bucket: "{{ .Nope }}"})
		require.Error(t, err)
		_, err = instanceFor(ctx, config.Blob{Provider: "{{ .Nope }}", Bucket: "bucket"})
		require.Error(t, err)
	})
}

// flakyBucket is an in-memory bucket driver that fails opens and writes with
// the configured errors, in order, and records every write it gets.
type flakyBucket struct {
	driver.Bucket

	mu        sync.Mutex
	opens     int
	openErrs  []error
	writeErrs map[string][]error
	writes    map[string][]string
	onWrite   func()
}

func (b *flakyBucket) ErrorCode(error) gcerrors.ErrorCode { return gcerrors.Unknown }
func (b *flakyBucket) Close() error                       { return nil }

func (b *flakyBucket) NewTypedWriter(_ stdctx.Context, key, _ string, _ *driver.WriterOptions) (driver.Writer, error) {
	return &flakyWriter{b: b, key: key}, nil
}

func (b *flakyBucket) received(key string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.writes[key]
}

func (b *flakyBucket) openCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.opens
}

type flakyWriter struct {
	b      *flakyBucket
	key    string
	buf    bytes.Buffer
	once   sync.Once
	result error
}

func (w *flakyWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }

// Close is idempotent, as the uploader closes writers twice.
func (w *flakyWriter) Close() error {
	w.once.Do(func() {
		w.b.mu.Lock()
		defer w.b.mu.Unlock()
		w.b.writes[w.key] = append(w.b.writes[w.key], w.buf.String())
		if w.b.onWrite != nil {
			w.b.onWrite()
		}
		if errs := w.b.writeErrs[w.key]; len(errs) > 0 {
			w.b.writeErrs[w.key] = errs[1:]
			w.result = errs[0]
		}
	})
	return w.result
}

type flakyOpener struct {
	mu      sync.Mutex
	buckets map[string]*flakyBucket
}

func (o *flakyOpener) OpenBucketURL(_ stdctx.Context, u *url.URL) (*blob.Bucket, error) {
	o.mu.Lock()
	b, ok := o.buckets[u.Host]
	o.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown bucket: %s", u.Host)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.opens++
	if len(b.openErrs) > 0 {
		err := b.openErrs[0]
		b.openErrs = b.openErrs[1:]
		return nil, err
	}
	return blob.NewBucket(b), nil
}

var (
	flaky         = &flakyOpener{buckets: map[string]*flakyBucket{}}
	registerFlaky sync.Once
)

// newFlakyBucket creates a bucket reachable at flaky://name.
func newFlakyBucket(t *testing.T, name string) *flakyBucket {
	t.Helper()
	registerFlaky.Do(func() {
		blob.DefaultURLMux().RegisterBucket("flaky", flaky)
	})
	b := &flakyBucket{
		writeErrs: map[string][]error{},
		writes:    map[string][]string{},
	}
	flaky.mu.Lock()
	flaky.buckets[name] = b
	flaky.mu.Unlock()
	t.Cleanup(func() {
		flaky.mu.Lock()
		delete(flaky.buckets, name)
		flaky.mu.Unlock()
	})
	return b
}

func blobRetryCtx(t *testing.T, blobs ...config.Blob) (*context.Context, *artifact.Artifact, *artifact.Artifact) {
	t.Helper()
	dir := t.TempDir()
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{
		ProjectName: "proj",
		Blobs:       blobs,
	}, testctx.WithCurrentTag("v1.0.0"))
	add := func(name, content string) *artifact.Artifact {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
		a := &artifact.Artifact{
			Name:  name,
			Path:  path,
			Type:  artifact.UploadableArchive,
			Extra: artifact.Extras{artifact.ExtraID: "default"},
		}
		ctx.Artifacts.Add(a)
		return a
	}
	a := add("a.tar.gz", "content of a")
	b := add("b.tar.gz", "content of b")
	require.NoError(t, Pipe{}.Default(ctx))
	return ctx, a, b
}

func blobAttempts(tb testing.TB, a *artifact.Artifact) []artifact.PublishAttempt {
	tb.Helper()
	return artifact.ExtraOr(*a, artifact.ExtraPublishAttempts, []artifact.PublishAttempt(nil))
}

func TestPublishRetries(t *testing.T) {
	bucket := newFlakyBucket(t, "retries")
	bucket.openErrs = []error{temporaryError{true}, timeoutError{true}}
	bucket.writeErrs["proj/v1.0.0/a.tar.gz"] = []error{timeoutError{true}, temporaryError{true}}
	bucket.writeErrs["proj/v1.0.0/file.golden"] = []error{temporaryError{true}}

	extra, err := os.ReadFile("testdata/file.golden")
	require.NoError(t, err)

	ctx, a, b := blobRetryCtx(t, config.Blob{
		Provider:   "flaky",
		Bucket:     "{{ .Env.BUCKET }}",
		ExtraFiles: []config.ExtraFile{{Glob: "testdata/file.golden"}},
		Retry: config.Retry{
			Attempts: 3,
			Delay:    time.Millisecond,
		},
	})
	ctx.Env["BUCKET"] = "retries"
	require.NoError(t, Pipe{}.Publish(ctx))

	require.Equal(t, 3, bucket.openCount())
	require.Equal(t, []string{"content of a", "content of a", "content of a"}, bucket.received("proj/v1.0.0/a.tar.gz"))
	require.Equal(t, []string{"content of b"}, bucket.received("proj/v1.0.0/b.tar.gz"))
	require.Equal(t, []string{string(extra), string(extra)}, bucket.received("proj/v1.0.0/file.golden"))

	attempts := blobAttempts(t, a)
	require.Len(t, attempts, 3, "bucket open retries must not be recorded")
	for i, attempt := range attempts {
		require.Equal(t, "blob", attempt.Publisher)
		require.Equal(t, "flaky://retries", attempt.Instance)
		require.Equal(t, "proj/v1.0.0/a.tar.gz", attempt.Target)
		require.Equal(t, i+1, attempt.Attempt)
	}
	require.Equal(t, artifact.PublishAttemptFailure, attempts[0].Status)
	require.Contains(t, attempts[0].Error, "timeout error (true)")
	require.Equal(t, artifact.PublishAttemptFailure, attempts[1].Status)
	require.Contains(t, attempts[1].Error, "temporary error (true)")
	require.Equal(t, artifact.PublishAttemptSuccess, attempts[2].Status)
	require.Empty(t, attempts[2].Error)

	require.Equal(t, []artifact.PublishAttempt{{
		Publisher: "blob",
		Instance:  "flaky://retries",
		Target:    "proj/v1.0.0/b.tar.gz",
		Attempt:   1,
		Status:    artifact.PublishAttemptSuccess,
	}}, blobAttempts(t, b))
}

func TestPublishDoesNotRetryPermanentErrors(t *testing.T) {
	for name, err := range map[string]error{
		"plain":         errors.New("access denied"),
		"not temporary": temporaryError{false},
		"not timeout":   timeoutError{false},
	} {
		t.Run(name, func(t *testing.T) {
			bucket := newFlakyBucket(t, "permanent")
			bucket.writeErrs["proj/v1.0.0/a.tar.gz"] = []error{err}
			ctx, a, _ := blobRetryCtx(t, config.Blob{
				Provider: "flaky",
				Bucket:   "permanent",
				Retry:    config.Retry{Attempts: 5},
			})
			require.ErrorContains(t, Pipe{}.Publish(ctx), err.Error())
			require.Len(t, bucket.received("proj/v1.0.0/a.tar.gz"), 1)

			attempts := blobAttempts(t, a)
			require.Len(t, attempts, 1)
			require.Equal(t, artifact.PublishAttemptFailure, attempts[0].Status)
			require.Contains(t, attempts[0].Error, err.Error())
		})
	}
}

func TestPublishRetriesExhausted(t *testing.T) {
	bucket := newFlakyBucket(t, "exhausted")
	bucket.writeErrs["proj/v1.0.0/a.tar.gz"] = []error{
		temporaryError{true},
		temporaryError{true},
		temporaryError{true},
	}
	ctx, a, _ := blobRetryCtx(t, config.Blob{
		Provider: "flaky",
		Bucket:   "exhausted",
		Retry:    config.Retry{Attempts: 2},
	})
	require.ErrorContains(t, Pipe{}.Publish(ctx), "failed to write to bucket")
	require.Len(t, bucket.received("proj/v1.0.0/a.tar.gz"), 2)

	attempts := blobAttempts(t, a)
	require.Len(t, attempts, 2)
	for _, attempt := range attempts {
		require.Equal(t, artifact.PublishAttemptFailure, attempt.Status)
	}
}

func TestPublishNoRetryConfig(t *testing.T) {
	bucket := newFlakyBucket(t, "noretry")
	bucket.openErrs = []error{temporaryError{true}}
	ctx, a, _ := blobRetryCtx(t, config.Blob{
		Provider: "flaky",
		Bucket:   "noretry",
	})
	require.ErrorContains(t, Pipe{}.Publish(ctx), "temporary error (true)")
	require.Equal(t, 1, bucket.openCount())
	require.Empty(t, blobAttempts(t, a))
}

func TestPublishOpenRetries(t *testing.T) {
	t.Run("exhausted", func(t *testing.T) {
		bucket := newFlakyBucket(t, "open-exhausted")
		bucket.openErrs = []error{timeoutError{true}, timeoutError{true}, timeoutError{true}}
		ctx, a, b := blobRetryCtx(t, config.Blob{
			Provider: "flaky",
			Bucket:   "open-exhausted",
			Retry:    config.Retry{Attempts: 3},
		})
		require.ErrorContains(t, Pipe{}.Publish(ctx), "timeout error (true)")
		require.Equal(t, 3, bucket.openCount())
		require.Empty(t, blobAttempts(t, a))
		require.Empty(t, blobAttempts(t, b))
	})

	t.Run("permanent", func(t *testing.T) {
		bucket := newFlakyBucket(t, "open-permanent")
		bucket.openErrs = []error{errors.New("NoSuchBucket")}
		ctx, _, _ := blobRetryCtx(t, config.Blob{
			Provider: "flaky",
			Bucket:   "open-permanent",
			Retry:    config.Retry{Attempts: 3},
		})
		require.ErrorContains(t, Pipe{}.Publish(ctx), "provided bucket does not exist")
		require.Equal(t, 1, bucket.openCount())
	})
}

func TestPublishRetryContextCanceled(t *testing.T) {
	bucket := newFlakyBucket(t, "canceled")
	bucket.writeErrs["proj/v1.0.0/a.tar.gz"] = []error{
		temporaryError{true},
		temporaryError{true},
		temporaryError{true},
	}
	ctx, a, _ := blobRetryCtx(t, config.Blob{
		Provider: "flaky",
		Bucket:   "canceled",
		IDs:      []string{"default"},
		Retry: config.Retry{
			Attempts: 3,
			Delay:    time.Hour,
		},
	})
	cctx, cancel := stdctx.WithCancel(ctx.Context)
	defer cancel()
	ctx.Context = cctx
	bucket.onWrite = cancel

	start := time.Now()
	err := Pipe{}.Publish(ctx)
	require.ErrorIs(t, err, stdctx.Canceled)
	require.Less(t, time.Since(start), time.Minute)

	attempts := blobAttempts(t, a)
	require.Len(t, attempts, 1)
	require.Equal(t, artifact.PublishAttemptFailure, attempts[0].Status)
}

func TestPublishAttemptsMultipleConfigs(t *testing.T) {
	one := newFlakyBucket(t, "one")
	one.writeErrs["proj/v1.0.0/a.tar.gz"] = []error{temporaryError{true}}
	two := newFlakyBucket(t, "two")
	two.writeErrs["other/a.tar.gz"] = []error{timeoutError{true}, timeoutError{true}}

	retry := config.Retry{Attempts: 3}
	ctx, a, b := blobRetryCtx(t,
		config.Blob{Provider: "flaky", Bucket: "two", Directory: "other", IDs: []string{"default"}, Retry: retry},
		config.Blob{Provider: "flaky", Bucket: "one", IDs: []string{"default"}, Retry: retry},
		config.Blob{Provider: "flaky", Bucket: "two", IDs: []string{"default"}, Retry: retry},
	)
	require.NoError(t, Pipe{}.Publish(ctx))

	type key struct {
		instance, target string
		attempt          int
		status           string
	}
	keys := func(attempts []artifact.PublishAttempt) []key {
		var result []key
		for _, a := range attempts {
			result = append(result, key{a.Instance, a.Target, a.Attempt, a.Status})
		}
		return result
	}
	require.Equal(t, []key{
		{"flaky://one", "proj/v1.0.0/a.tar.gz", 1, artifact.PublishAttemptFailure},
		{"flaky://one", "proj/v1.0.0/a.tar.gz", 2, artifact.PublishAttemptSuccess},
		{"flaky://two", "other/a.tar.gz", 1, artifact.PublishAttemptFailure},
		{"flaky://two", "other/a.tar.gz", 2, artifact.PublishAttemptFailure},
		{"flaky://two", "other/a.tar.gz", 3, artifact.PublishAttemptSuccess},
		{"flaky://two", "proj/v1.0.0/a.tar.gz", 1, artifact.PublishAttemptSuccess},
	}, keys(blobAttempts(t, a)))
	require.Equal(t, []key{
		{"flaky://one", "proj/v1.0.0/b.tar.gz", 1, artifact.PublishAttemptSuccess},
		{"flaky://two", "other/b.tar.gz", 1, artifact.PublishAttemptSuccess},
		{"flaky://two", "proj/v1.0.0/b.tar.gz", 1, artifact.PublishAttemptSuccess},
	}, keys(blobAttempts(t, b)))
}
