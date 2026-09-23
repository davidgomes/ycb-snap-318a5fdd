package blob

import (
	stdctx "context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/goreleaser/goreleaser/v2/internal/artifact"
	"github.com/goreleaser/goreleaser/v2/internal/publishattempt"
	"github.com/goreleaser/goreleaser/v2/internal/testctx"
	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/goreleaser/goreleaser/v2/pkg/context"
	"github.com/stretchr/testify/require"
)

func TestBlobRetriesTransientUpload(t *testing.T) {
	payload := []byte("blob-body-full")
	art := newBlobArtifact(t, payload)
	fake := &fakeUploader{
		uploadErrs: []error{timeoutErr("try again"), nil},
	}
	useUploader(t, fake)

	ctx := blobCtx(t, art, config.Retry{Attempts: 4}, nil)
	require.NoError(t, Pipe{}.Publish(ctx))
	require.Equal(t, 1, fake.opens())
	require.Equal(t, [][]byte{payload, payload}, fake.bodies())
	require.Equal(t, []string{"dist/artifact.bin", "dist/artifact.bin"}, fake.paths())
	require.Equal(t, []artifact.PublishAttempt{
		{Publisher: "blob", Instance: "gs://my-bucket", Target: "dist/artifact.bin", Attempt: 1, Status: publishattempt.StatusFailure, Error: "try again"},
		{Publisher: "blob", Instance: "gs://my-bucket", Target: "dist/artifact.bin", Attempt: 2, Status: publishattempt.StatusSuccess},
	}, publishattempt.List(art))
}

func TestBlobOpenRetriesAreNotPublishAttempts(t *testing.T) {
	payload := []byte("opened")
	art := newBlobArtifact(t, payload)
	fake := &fakeUploader{
		openErrs: []error{timeoutErr("open timeout"), tempErr("open temp"), nil},
	}
	useUploader(t, fake)

	ctx := blobCtx(t, art, config.Retry{Attempts: 5}, nil)
	require.NoError(t, Pipe{}.Publish(ctx))
	require.Equal(t, 3, fake.opens())
	require.Equal(t, [][]byte{payload}, fake.bodies())
	require.Equal(t, []artifact.PublishAttempt{
		{Publisher: "blob", Instance: "gs://my-bucket", Target: "dist/artifact.bin", Attempt: 1, Status: publishattempt.StatusSuccess},
	}, publishattempt.List(art))
}

func TestBlobDoesNotRetryPermanentErrors(t *testing.T) {
	art := newBlobArtifact(t, []byte("x"))
	fake := &fakeUploader{uploadErrs: []error{errors.New("NoSuchBucket")}}
	useUploader(t, fake)

	ctx := blobCtx(t, art, config.Retry{Attempts: 4, Delay: time.Hour}, nil)
	err := Pipe{}.Publish(ctx)
	require.ErrorContains(t, err, "provided bucket does not exist")
	require.Equal(t, 1, fake.uploads())
	attempts := publishattempt.List(art)
	require.Len(t, attempts, 1)
	require.Equal(t, "NoSuchBucket", attempts[0].Error)
	require.Equal(t, publishattempt.StatusFailure, attempts[0].Status)
}

func TestBlobIgnoresNegativeTimeout(t *testing.T) {
	art := newBlobArtifact(t, []byte("x"))
	fake := &fakeUploader{uploadErrs: []error{netErr{msg: "steady"}}}
	useUploader(t, fake)
	ctx := blobCtx(t, art, config.Retry{Attempts: 3}, nil)
	err := Pipe{}.Publish(ctx)
	require.ErrorContains(t, err, "failed to write to bucket: steady")
	require.Equal(t, 1, fake.uploads())
}

func TestBlobContextCancel(t *testing.T) {
	t.Run("during upload", func(t *testing.T) {
		art := newBlobArtifact(t, []byte("x"))
		fake := &fakeUploader{uploadErrs: []error{stdctx.Canceled}}
		useUploader(t, fake)
		ctx := blobCtx(t, art, config.Retry{Attempts: 4}, nil)
		err := Pipe{}.Publish(ctx)
		require.Equal(t, stdctx.Canceled, err)
		require.Equal(t, 1, fake.uploads())
		require.Equal(t, "context canceled", publishattempt.List(art)[0].Error)
	})

	t.Run("during open", func(t *testing.T) {
		art := newBlobArtifact(t, []byte("x"))
		fake := &fakeUploader{openErrs: []error{stdctx.DeadlineExceeded}}
		useUploader(t, fake)
		ctx := blobCtx(t, art, config.Retry{Attempts: 4}, nil)
		err := Pipe{}.Publish(ctx)
		require.Equal(t, stdctx.DeadlineExceeded, err)
		require.Equal(t, 0, fake.uploads())
		require.Empty(t, publishattempt.List(art))
	})

	t.Run("during open backoff", func(t *testing.T) {
		art := newBlobArtifact(t, []byte("x"))
		parent, cancel := stdctx.WithCancel(t.Context())
		t.Cleanup(cancel)
		fake := &fakeUploader{openErrs: []error{timeoutErr("later")}}
		useUploader(t, fake)
		ctx := blobCtx(t, art, config.Retry{Attempts: 4, Delay: time.Hour}, nil)
		ctx.Context = parent

		errCh := make(chan error, 1)
		go func() {
			errCh <- Pipe{}.Publish(ctx)
		}()
		require.Eventually(t, func() bool { return fake.opens() >= 1 }, time.Second, 5*time.Millisecond)
		cancel()
		select {
		case err := <-errCh:
			require.Equal(t, stdctx.Canceled, err)
		case <-time.After(2 * time.Second):
			t.Fatal("publish did not stop")
		}
		require.Equal(t, 0, fake.uploads())
		require.Empty(t, publishattempt.List(art))
	})
}

func TestBlobExtraFileAndTemplateInstance(t *testing.T) {
	// fileglob rejects absolute paths. Keep this out of testdata/*.golden.
	extraPath := filepath.Join("testdata", "retry", "notes.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(extraPath), 0o755))
	payload := []byte("notes-body")
	require.NoError(t, os.WriteFile(extraPath, payload, 0o644))
	t.Cleanup(func() {
		_ = os.RemoveAll(filepath.Join("testdata", "retry"))
	})

	fake := &fakeUploader{uploadErrs: []error{tempErr("temp"), nil}}
	useUploader(t, fake)

	ctx := testctx.WrapWithCfg(t.Context(), config.Project{
		ProjectName: "demo",
		Env:         []string{"BLOB_PROVIDER=s3"},
		Blobs: []config.Blob{{
			Provider:       "{{ .Env.BLOB_PROVIDER }}",
			Bucket:         "{{ .ProjectName }}",
			Directory:      "rel",
			ExtraFilesOnly: true,
			ExtraFiles:     []config.ExtraFile{{Glob: extraPath}},
			Retry:          config.Retry{Attempts: 3},
		}},
	})
	require.NoError(t, Pipe{}.Publish(ctx))
	require.Equal(t, [][]byte{payload, payload}, fake.bodies())
	require.Equal(t, []string{"rel/notes.txt", "rel/notes.txt"}, fake.paths())

	var found *artifact.Artifact
	for _, art := range ctx.Artifacts.List() {
		if art.Name == "notes.txt" {
			found = art
		}
	}
	require.NotNil(t, found)
	require.Equal(t, []artifact.PublishAttempt{
		{Publisher: "blob", Instance: "s3://demo", Target: "rel/notes.txt", Attempt: 1, Status: publishattempt.StatusFailure, Error: "temp"},
		{Publisher: "blob", Instance: "s3://demo", Target: "rel/notes.txt", Attempt: 2, Status: publishattempt.StatusSuccess},
	}, publishattempt.List(found))
}

func TestBlobAttemptsSortedAcrossBuckets(t *testing.T) {
	art := newBlobArtifact(t, []byte("multi"))
	var mu sync.Mutex
	var fakes []*fakeUploader
	orig := newUploader
	t.Cleanup(func() { newUploader = orig })
	newUploader = func(config.Blob) uploader {
		fake := &fakeUploader{}
		mu.Lock()
		fakes = append(fakes, fake)
		mu.Unlock()
		return fake
	}

	ctx := testctx.WrapWithCfg(t.Context(), config.Project{
		Blobs: []config.Blob{
			{Provider: "gs", Bucket: "zeta", Directory: "d", Retry: config.Retry{Attempts: 1}},
			{Provider: "s3", Bucket: "alpha", Directory: "d", Retry: config.Retry{Attempts: 1}},
		},
	})
	ctx.Artifacts.Add(art)
	require.NoError(t, Pipe{}.Publish(ctx))
	require.Equal(t, []artifact.PublishAttempt{
		{Publisher: "blob", Instance: "gs://zeta", Target: "d/artifact.bin", Attempt: 1, Status: publishattempt.StatusSuccess},
		{Publisher: "blob", Instance: "s3://alpha", Target: "d/artifact.bin", Attempt: 1, Status: publishattempt.StatusSuccess},
	}, publishattempt.List(art))
}

func blobCtx(t *testing.T, art *artifact.Artifact, retry config.Retry, extra []config.ExtraFile) *context.Context {
	t.Helper()
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{
		Blobs: []config.Blob{{
			Provider:   "gs",
			Bucket:     "my-bucket",
			Directory:  "dist",
			Retry:      retry,
			ExtraFiles: extra,
		}},
	})
	ctx.Artifacts.Add(art)
	return ctx
}

func newBlobArtifact(t *testing.T, payload []byte) *artifact.Artifact {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact.bin")
	require.NoError(t, os.WriteFile(path, payload, 0o644))
	return &artifact.Artifact{
		Name: "artifact.bin",
		Path: path,
		Type: artifact.UploadableArchive,
	}
}

func useUploader(t *testing.T, fake *fakeUploader) {
	t.Helper()
	orig := newUploader
	t.Cleanup(func() { newUploader = orig })
	newUploader = func(config.Blob) uploader { return fake }
}

type fakeUploader struct {
	mu         sync.Mutex
	openErrs   []error
	uploadErrs []error
	openCalls  int
	uploaded   []fakeUpload
}

type fakeUpload struct {
	path string
	data []byte
}

func (f *fakeUploader) Open(*context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.openCalls++
	if len(f.openErrs) == 0 {
		return nil
	}
	err := f.openErrs[0]
	f.openErrs = f.openErrs[1:]
	return err
}

func (f *fakeUploader) Upload(_ *context.Context, path string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	copied := append([]byte(nil), data...)
	f.uploaded = append(f.uploaded, fakeUpload{path: path, data: copied})
	if len(f.uploadErrs) == 0 {
		return nil
	}
	err := f.uploadErrs[0]
	f.uploadErrs = f.uploadErrs[1:]
	return err
}

func (f *fakeUploader) Close() error { return nil }

func (f *fakeUploader) opens() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.openCalls
}

func (f *fakeUploader) uploads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.uploaded)
}

func (f *fakeUploader) bodies() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]byte, len(f.uploaded))
	for i, up := range f.uploaded {
		out[i] = up.data
	}
	return out
}

func (f *fakeUploader) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.uploaded))
	for i, up := range f.uploaded {
		out[i] = up.path
	}
	return out
}

type netErr struct {
	msg       string
	timeout   bool
	temporary bool
}

func (e netErr) Error() string   { return e.msg }
func (e netErr) Timeout() bool   { return e.timeout }
func (e netErr) Temporary() bool { return e.temporary }

func timeoutErr(msg string) error { return netErr{msg: msg, timeout: true} }
func tempErr(msg string) error    { return netErr{msg: msg, temporary: true} }
