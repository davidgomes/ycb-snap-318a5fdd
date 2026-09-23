package blob

import (
	stdctx "context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/goreleaser/goreleaser/v2/internal/artifact"
	"github.com/goreleaser/goreleaser/v2/internal/testctx"
	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/goreleaser/goreleaser/v2/pkg/context"
	"github.com/stretchr/testify/require"
)

type timeoutErr struct{}

func (timeoutErr) Error() string { return "i/o timeout" }
func (timeoutErr) Timeout() bool { return true }

type fakeUploader struct {
	mu          sync.Mutex
	openFails   int
	opens       int
	uploadFails map[string]int
	uploadErr   error
	uploads     map[string][]string
	onUpload    func()
}

func (f *fakeUploader) Close() error { return nil }

func (f *fakeUploader) Open(_ *context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opens++
	if f.opens <= f.openFails {
		return timeoutErr{}
	}
	return nil
}

func (f *fakeUploader) Upload(_ *context.Context, path string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.uploads == nil {
		f.uploads = map[string][]string{}
	}
	f.uploads[path] = append(f.uploads[path], string(data))
	if f.onUpload != nil {
		f.onUpload()
	}
	if len(f.uploads[path]) <= f.uploadFails[path] {
		if f.uploadErr != nil {
			return f.uploadErr
		}
		return timeoutErr{}
	}
	return nil
}

func setupBlobRetry(t *testing.T, parent stdctx.Context, retry config.Retry) (*context.Context, config.Blob, *artifact.Artifact) {
	t.Helper()
	t.Chdir(t.TempDir())
	path := "a.tar.gz"
	require.NoError(t, os.WriteFile(path, []byte("full-content"), 0o644))
	extra := "extra.txt"
	require.NoError(t, os.WriteFile(extra, []byte("extra-content"), 0o644))
	conf := config.Blob{
		Provider:   "s3",
		Bucket:     "{{ .ProjectName }}-bucket",
		Directory:  "dir",
		Retry:      retry,
		ExtraFiles: []config.ExtraFile{{Glob: extra}},
	}
	ctx := testctx.WrapWithCfg(parent, config.Project{ProjectName: "proj", Blobs: []config.Blob{conf}})
	a := &artifact.Artifact{Name: "a.tar.gz", Path: path, Type: artifact.UploadableArchive}
	ctx.Artifacts.Add(a)
	return ctx, conf, a
}

func TestBlobRetry(t *testing.T) {
	ctx, conf, a := setupBlobRetry(t, t.Context(), config.Retry{Attempts: 4, Delay: time.Millisecond})
	up := &fakeUploader{
		openFails:   2,
		uploadFails: map[string]int{"dir/a.tar.gz": 2, "dir/extra.txt": 1},
	}
	instance, err := instanceFor(ctx, conf)
	require.NoError(t, err)
	require.Equal(t, "s3://proj-bucket", instance)
	require.NoError(t, publish(ctx, conf, up, "dir", "s3://proj-bucket", instance))

	require.Equal(t, 3, up.opens)
	require.Equal(t, []string{"full-content", "full-content", "full-content"}, up.uploads["dir/a.tar.gz"])
	require.Equal(t, []string{"extra-content", "extra-content"}, up.uploads["dir/extra.txt"])

	attempts := artifact.MustExtra[[]artifact.PublishAttempt](*a, artifact.ExtraPublishAttempts)
	require.Equal(t, []artifact.PublishAttempt{
		{Publisher: "blob", Instance: "s3://proj-bucket", Target: "dir/a.tar.gz", Attempt: 1, Status: "failure", Error: "i/o timeout"},
		{Publisher: "blob", Instance: "s3://proj-bucket", Target: "dir/a.tar.gz", Attempt: 2, Status: "failure", Error: "i/o timeout"},
		{Publisher: "blob", Instance: "s3://proj-bucket", Target: "dir/a.tar.gz", Attempt: 3, Status: "success"},
	}, attempts)
}

func TestBlobNoRetryOnPermanentError(t *testing.T) {
	ctx, conf, a := setupBlobRetry(t, t.Context(), config.Retry{Attempts: 4})
	up := &fakeUploader{
		uploadFails: map[string]int{"dir/a.tar.gz": 5},
		uploadErr:   errors.New("AccessDenied"),
	}
	require.Error(t, publish(ctx, conf, up, "dir", "s3://proj-bucket", "s3://proj-bucket"))
	require.Len(t, up.uploads["dir/a.tar.gz"], 1)
	attempts := artifact.MustExtra[[]artifact.PublishAttempt](*a, artifact.ExtraPublishAttempts)
	require.Len(t, attempts, 1)
	require.Equal(t, "AccessDenied", attempts[0].Error)
}

func TestBlobRetryContextCanceled(t *testing.T) {
	parent, cancel := stdctx.WithCancel(t.Context())
	ctx, conf, _ := setupBlobRetry(t, parent, config.Retry{Attempts: 4, Delay: time.Hour})
	conf.ExtraFiles = nil
	up := &fakeUploader{
		uploadFails: map[string]int{"dir/a.tar.gz": 5, "dir/extra.txt": 5},
		onUpload:    cancel,
	}
	err := publish(ctx, conf, up, "dir", "s3://proj-bucket", "s3://proj-bucket")
	require.ErrorIs(t, err, stdctx.Canceled)
}
