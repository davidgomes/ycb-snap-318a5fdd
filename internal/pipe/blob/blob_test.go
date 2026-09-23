package blob

import (
	stdctx "context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/goreleaser/goreleaser/v2/internal/artifact"
	"github.com/goreleaser/goreleaser/v2/internal/testctx"
	"github.com/goreleaser/goreleaser/v2/internal/testlib"
	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/goreleaser/goreleaser/v2/pkg/context"
	"github.com/stretchr/testify/require"
)

func TestDescription(t *testing.T) {
	require.NotEmpty(t, Pipe{}.String())
}

func TestErrors(t *testing.T) {
	for k, v := range map[string]string{
		"NoSuchBucket":                 "provided bucket does not exist: someurl: NoSuchBucket",
		"ContainerNotFound":            "provided bucket does not exist: someurl: ContainerNotFound",
		"notFound":                     "provided bucket does not exist: someurl: notFound",
		"NoCredentialProviders":        "check credentials and access to bucket: someurl: NoCredentialProviders",
		"InvalidAccessKeyId":           "aws access key id you provided does not exist in our records: InvalidAccessKeyId",
		"AuthenticationFailed":         "azure storage key you provided is not valid: AuthenticationFailed",
		"invalid_grant":                "google app credentials you provided is not valid: invalid_grant",
		"no such host":                 "azure storage account you provided is not valid: no such host",
		"ServiceCode=ResourceNotFound": "missing azure storage key for provided bucket someurl: ServiceCode=ResourceNotFound",
		"other":                        "failed to write to bucket: other",
	} {
		t.Run(k, func(t *testing.T) {
			require.EqualError(t, handleError(errors.New(k), "someurl"), v)
		})
	}
}

func TestDefaultsNoConfig(t *testing.T) {
	errorString := "bucket or provider cannot be empty"
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{
		Blobs: []config.Blob{{}},
	})

	require.EqualError(t, Pipe{}.Default(ctx), errorString)
}

func TestDefaultsNoBucket(t *testing.T) {
	errorString := "bucket or provider cannot be empty"
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{
		Blobs: []config.Blob{
			{
				Provider: "azblob",
			},
		},
	})

	require.EqualError(t, Pipe{}.Default(ctx), errorString)
}

func TestDefaultsNoProvider(t *testing.T) {
	errorString := "bucket or provider cannot be empty"
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{
		Blobs: []config.Blob{
			{
				Bucket: "goreleaser-bucket",
			},
		},
	})

	require.EqualError(t, Pipe{}.Default(ctx), errorString)
}

func TestDefaults(t *testing.T) {
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{
		Blobs: []config.Blob{
			{
				Bucket:             "foo",
				Provider:           "azblob",
				IDs:                []string{"foo", "bar"},
				ContentDisposition: "inline",
			},
			{
				Bucket:   "foobar2",
				Provider: "gcs",
			},
			{
				Bucket:             "foobar",
				Provider:           "gcs",
				ContentDisposition: "-",
			},
		},
	})

	require.NoError(t, Pipe{}.Default(ctx))
	require.Equal(t, []config.Blob{
		{
			Bucket:             "foo",
			Provider:           "azblob",
			Directory:          "{{ .ProjectName }}/{{ .Tag }}",
			IDs:                []string{"foo", "bar"},
			ContentDisposition: "inline",
		},
		{
			Bucket:             "foobar2",
			Provider:           "gcs",
			Directory:          "{{ .ProjectName }}/{{ .Tag }}",
			ContentDisposition: "attachment;filename={{.Filename}}",
		},
		{
			Bucket:             "foobar",
			Provider:           "gcs",
			Directory:          "{{ .ProjectName }}/{{ .Tag }}",
			ContentDisposition: "",
		},
	}, ctx.Config.Blobs)
}

func TestDefaultsWithProvider(t *testing.T) {
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{
		Blobs: []config.Blob{
			{
				Bucket:   "foo",
				Provider: "azblob",
			},
			{
				Bucket:   "foo",
				Provider: "s3",
			},
			{
				Bucket:   "foo",
				Provider: "gs",
			},
		},
	})

	require.NoError(t, Pipe{}.Default(ctx))
}

func TestURL(t *testing.T) {
	t.Run("s3 with opts", func(t *testing.T) {
		url, err := urlFor(testctx.Wrap(t.Context()), config.Blob{
			Bucket:     "foo",
			Provider:   "s3",
			Region:     "us-west-1",
			Directory:  "foo",
			Endpoint:   "s3.foobar.com",
			DisableSSL: true,
		})
		require.NoError(t, err)
		require.Equal(t, "s3://foo?disable_https=true&endpoint=s3.foobar.com&region=us-west-1&s3ForcePathStyle=true", url)
	})

	t.Run("s3 with some opts", func(t *testing.T) {
		url, err := urlFor(testctx.Wrap(t.Context()), config.Blob{
			Bucket:     "foo",
			Provider:   "s3",
			Region:     "us-west-1",
			DisableSSL: true,
		})
		require.NoError(t, err)
		require.Equal(t, "s3://foo?disable_https=true&region=us-west-1", url)
	})

	t.Run("gs with opts", func(t *testing.T) {
		url, err := urlFor(testctx.Wrap(t.Context()), config.Blob{
			Bucket:     "foo",
			Provider:   "gs",
			Region:     "us-west-1",
			Directory:  "foo",
			Endpoint:   "s3.foobar.com",
			DisableSSL: true,
		})
		require.NoError(t, err)
		require.Equal(t, "gs://foo", url)
	})

	t.Run("s3 no opts", func(t *testing.T) {
		url, err := urlFor(testctx.Wrap(t.Context()), config.Blob{
			Bucket:   "foo",
			Provider: "s3",
		})
		require.NoError(t, err)
		require.Equal(t, "s3://foo", url)
	})

	t.Run("gs no opts", func(t *testing.T) {
		url, err := urlFor(testctx.Wrap(t.Context()), config.Blob{
			Bucket:   "foo",
			Provider: "gs",
		})
		require.NoError(t, err)
		require.Equal(t, "gs://foo", url)
	})

	t.Run("template errors", func(t *testing.T) {
		t.Run("provider", func(t *testing.T) {
			_, err := urlFor(testctx.Wrap(t.Context()), config.Blob{
				Provider: "{{ .Nope }}",
			})
			testlib.RequireTemplateError(t, err)
		})
		t.Run("bucket", func(t *testing.T) {
			_, err := urlFor(testctx.Wrap(t.Context()), config.Blob{
				Bucket:   "{{ .Nope }}",
				Provider: "gs",
			})
			testlib.RequireTemplateError(t, err)
		})
		t.Run("endpoint", func(t *testing.T) {
			_, err := urlFor(testctx.Wrap(t.Context()), config.Blob{
				Bucket:   "foobar",
				Endpoint: "{{.Env.NOPE}}",
				Provider: "s3",
			})
			testlib.RequireTemplateError(t, err)
		})
		t.Run("region", func(t *testing.T) {
			_, err := urlFor(testctx.Wrap(t.Context()), config.Blob{
				Bucket:   "foobar",
				Region:   "{{.Env.NOPE}}",
				Provider: "s3",
			})
			testlib.RequireTemplateError(t, err)
		})
	})
}

type fakeUploader struct {
	opens      int
	openErrs   []error
	uploadErrs []error
	payloads   [][]byte
	paths      []string
	uploadFn   func(path string) error
}

func (f *fakeUploader) Open(*context.Context, string) error {
	f.opens++
	if len(f.openErrs) == 0 {
		return nil
	}
	err := f.openErrs[0]
	f.openErrs = f.openErrs[1:]
	return err
}

func (f *fakeUploader) Upload(_ *context.Context, path string, data []byte) error {
	f.paths = append(f.paths, path)
	f.payloads = append(f.payloads, append([]byte(nil), data...))
	if f.uploadFn != nil {
		return f.uploadFn(path)
	}
	if len(f.uploadErrs) == 0 {
		return nil
	}
	err := f.uploadErrs[0]
	f.uploadErrs = f.uploadErrs[1:]
	return err
}

func (f *fakeUploader) Close() error { return nil }

type netErr struct {
	msg       string
	temporary bool
	timeout   bool
}

func (e netErr) Error() string   { return e.msg }
func (e netErr) Temporary() bool { return e.temporary }
func (e netErr) Timeout() bool   { return e.timeout }

func TestUploadRetryAttempts(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	bin := "bin"
	extra := "extra.txt"
	require.NoError(t, os.WriteFile(bin, []byte("bin-bytes"), 0o644))
	require.NoError(t, os.WriteFile(extra, []byte("extra-bytes"), 0o644))

	var binCalls int
	fake := &fakeUploader{
		openErrs: []error{netErr{msg: "open timeout", timeout: true}},
		uploadFn: func(path string) error {
			if strings.Contains(path, "extra") {
				return errors.New("permanent")
			}
			binCalls++
			if binCalls == 1 {
				return netErr{msg: "upload temporary", temporary: true}
			}
			return nil
		},
	}
	prev := newUploader
	newUploader = func(config.Blob) uploader { return fake }
	t.Cleanup(func() { newUploader = prev })

	ctx := testctx.WrapWithCfg(t.Context(), config.Project{
		ProjectName: "proj",
		Blobs: []config.Blob{{
			Provider:  "s3",
			Bucket:    "bucket",
			Directory: "rel",
			Retry: config.Retry{
				Attempts: 3,
				Delay:    time.Millisecond,
				MaxDelay: 2 * time.Millisecond,
			},
			ExtraFiles: []config.ExtraFile{{Glob: extra}},
		}},
	})
	ctx.Artifacts.Add(&artifact.Artifact{
		Name: "a.bin",
		Path: bin,
		Type: artifact.UploadableBinary,
	})
	require.NoError(t, Pipe{}.Default(ctx))
	ctx.Parallelism = 1
	err := Pipe{}.Publish(ctx)
	require.Error(t, err)
	require.Equal(t, 2, fake.opens)
	require.Equal(t, []string{"rel/a.bin", "rel/a.bin", "rel/extra.txt"}, fake.paths)
	require.Equal(t, [][]byte{[]byte("bin-bytes"), []byte("bin-bytes"), []byte("extra-bytes")}, fake.payloads)

	attempts := ctx.Extra[context.PublishAttemptsKey].([]context.PublishAttempt)
	require.Len(t, attempts, 3)
	require.Equal(t, context.PublishAttempt{
		Publisher: "blob", Instance: "s3://bucket", Target: "rel/a.bin", Attempt: 1, Status: "failure", Error: "upload temporary",
	}, attempts[0])
	require.Equal(t, context.PublishAttempt{
		Publisher: "blob", Instance: "s3://bucket", Target: "rel/a.bin", Attempt: 2, Status: "success",
	}, attempts[1])
	require.Equal(t, "rel/extra.txt", attempts[2].Target)
	require.Equal(t, 1, attempts[2].Attempt)
	require.Equal(t, "failure", attempts[2].Status)
	require.Equal(t, "permanent", attempts[2].Error)

	// Context cancellation stops the retry loop.
	fake2 := &fakeUploader{uploadErrs: []error{netErr{msg: "again", temporary: true}}}
	newUploader = func(config.Blob) uploader { return fake2 }
	cancelCtx, cancel := stdctx.WithCancel(t.Context())
	ctx2 := testctx.WrapWithCfg(cancelCtx, config.Project{
		ProjectName: "proj",
		Blobs: []config.Blob{{
			Provider:  "{{ .ProjectName }}",
			Bucket:    "bkt",
			Directory: "rel",
			Retry:     config.Retry{Attempts: 4, Delay: time.Second, MaxDelay: time.Second},
		}},
	})
	ctx2.Artifacts.Add(&artifact.Artifact{Name: "a.bin", Path: bin, Type: artifact.UploadableBinary})
	require.NoError(t, Pipe{}.Default(ctx2))
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	err = Pipe{}.Publish(ctx2)
	require.ErrorIs(t, err, stdctx.Canceled)
	require.Less(t, time.Since(start), time.Second)
	canceledAttempts := ctx2.Extra[context.PublishAttemptsKey].([]context.PublishAttempt)
	require.Len(t, canceledAttempts, 1)
	require.Equal(t, "proj://bkt", canceledAttempts[0].Instance)
	require.Equal(t, "failure", canceledAttempts[0].Status)
}

func TestSkip(t *testing.T) {
	t.Run("skip", func(t *testing.T) {
		require.True(t, Pipe{}.Skip(testctx.Wrap(t.Context())))
	})

	t.Run("dont skip", func(t *testing.T) {
		ctx := testctx.WrapWithCfg(t.Context(), config.Project{
			Blobs: []config.Blob{{}},
		})

		require.False(t, Pipe{}.Skip(ctx))
	})
}
