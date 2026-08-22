package blob

import (
	stdctx "context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/caarlos0/log"
	"github.com/goreleaser/goreleaser/v2/internal/artifact"
	"github.com/goreleaser/goreleaser/v2/internal/extrafiles"
	"github.com/goreleaser/goreleaser/v2/internal/semerrgroup"
	"github.com/goreleaser/goreleaser/v2/internal/tmpl"
	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/goreleaser/goreleaser/v2/pkg/context"
	"gocloud.dev/blob"
	"gocloud.dev/secrets"

	// Import the blob packages we want to be able to open.
	_ "gocloud.dev/blob/azureblob"
	_ "gocloud.dev/blob/gcsblob"
	_ "gocloud.dev/blob/s3blob"

	// import the secrets packages we want to be able to be used.
	_ "gocloud.dev/secrets/awskms"
	_ "gocloud.dev/secrets/azurekeyvault"
	_ "gocloud.dev/secrets/gcpkms"
)

func urlFor(ctx *context.Context, conf config.Blob) (string, error) {
	bucket, err := tmpl.New(ctx).Apply(conf.Bucket)
	if err != nil {
		return "", err
	}

	provider, err := tmpl.New(ctx).Apply(conf.Provider)
	if err != nil {
		return "", err
	}

	bucketURL := fmt.Sprintf("%s://%s", provider, bucket)
	if provider != "s3" {
		return bucketURL, nil
	}

	query := url.Values{}

	endpoint, err := tmpl.New(ctx).Apply(conf.Endpoint)
	if err != nil {
		return "", err
	}
	if endpoint != "" {
		query.Add("endpoint", endpoint)
		if conf.S3ForcePathStyle == nil {
			query.Add("s3ForcePathStyle", "true")
		} else {
			query.Add("s3ForcePathStyle", strconv.FormatBool(*conf.S3ForcePathStyle))
		}
	}

	region, err := tmpl.New(ctx).Apply(conf.Region)
	if err != nil {
		return "", err
	}
	if region != "" {
		query.Add("region", region)
	}

	if conf.DisableSSL {
		query.Add("disable_https", "true")
	}

	if len(query) > 0 {
		bucketURL = bucketURL + "?" + query.Encode()
	}

	return bucketURL, nil
}

// Takes goreleaser context(which includes artifacts) and bucketURL for
// upload to destination (eg: gs://gorelease-bucket) using the given uploader
// implementation.
func doUpload(ctx *context.Context, conf config.Blob) error {
	dir, err := tmpl.New(ctx).Apply(conf.Directory)
	if err != nil {
		return err
	}
	dir = strings.TrimPrefix(dir, "/")

	bucketURL, err := urlFor(ctx, conf)
	if err != nil {
		return err
	}

	up := &productionUploader{
		cacheControl:       conf.CacheControl,
		contentDisposition: conf.ContentDisposition,
	}
	if conf.Provider == "s3" && conf.ACL != "" {
		up.beforeWrite = func(asFunc func(any) bool) error {
			req := &s3.PutObjectInput{}
			if !asFunc(&req) {
				return errors.New("could not apply before write")
			}
			acl := types.ObjectCannedACL(conf.ACL)
			switch acl {
			case types.ObjectCannedACLPrivate,
				types.ObjectCannedACLPublicRead,
				types.ObjectCannedACLPublicReadWrite,
				types.ObjectCannedACLAuthenticatedRead,
				types.ObjectCannedACLAwsExecRead,
				types.ObjectCannedACLBucketOwnerRead,
				types.ObjectCannedACLBucketOwnerFullControl:
				req.ACL = acl
				return nil
			default:
				return fmt.Errorf("invalid ACL %q", conf.ACL)
			}
		}
	}

	if err := retryBlobOp(ctx, conf.Retry, func() error {
		return up.Open(ctx, bucketURL)
	}); err != nil {
		return handleError(err, bucketURL)
	}
	defer up.Close()

	instance := instanceFromBucketURL(bucketURL)

	g := semerrgroup.New(ctx.Parallelism)
	for _, art := range artifactList(ctx, conf) {
		g.Go(func() error {
			// TODO: replace this with ?prefix=folder on the bucket url
			uploadFile := path.Join(dir, art.Name)
			return uploadData(ctx, conf, up, art, art.Path, uploadFile, instance, bucketURL)
		})
	}

	files, err := extrafiles.Find(ctx, conf.ExtraFiles)
	if err != nil {
		return err
	}
	for name, fullpath := range files {
		art := &artifact.Artifact{
			Name: name,
			Path: fullpath,
			Type: artifact.UploadableFile,
		}
		ctx.Artifacts.Add(art)
		g.Go(func() error {
			uploadFile := path.Join(dir, art.Name)
			return uploadData(ctx, conf, up, art, fullpath, uploadFile, instance, bucketURL)
		})
	}

	return g.Wait()
}

func instanceFromBucketURL(bucketURL string) string {
	if i := strings.Index(bucketURL, "?"); i >= 0 {
		return bucketURL[:i]
	}
	return bucketURL
}

func artifactList(ctx *context.Context, conf config.Blob) []*artifact.Artifact {
	if conf.ExtraFilesOnly {
		return nil
	}
	types := []artifact.Type{
		artifact.UploadableArchive,
		artifact.UploadableBinary,
		artifact.UploadableSourceArchive,
		artifact.Makeself,
		artifact.Checksum,
		artifact.Signature,
		artifact.Certificate,
		artifact.LinuxPackage,
		artifact.Flatpak,
		artifact.SBOM,
		artifact.PySdist,
		artifact.PyWheel,
	}
	if conf.IncludeMeta {
		types = append(types, artifact.Metadata)
	}
	return ctx.Artifacts.Filter(artifact.And(
		artifact.ByTypes(types...),
		artifact.ByIDs(conf.IDs...),
	)).List()
}

func uploadData(ctx *context.Context, conf config.Blob, up uploader, art *artifact.Artifact, dataFile, uploadFile, instance, bucketURL string) error {
	data, err := getData(ctx, conf, dataFile)
	if err != nil {
		return err
	}

	attempts := conf.Retry.MaxAttempts()
	var lastErr error
	for n := uint(1); n <= attempts; n++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = up.Upload(ctx, uploadFile, data)
		if lastErr == nil {
			art.RecordPublishAttempt(artifact.PublishAttempt{
				Publisher: "blob",
				Instance:  instance,
				Target:    uploadFile,
				Attempt:   int(n),
				Status:    artifact.PublishAttemptSuccess,
			})
			return nil
		}

		art.RecordPublishAttempt(artifact.PublishAttempt{
			Publisher: "blob",
			Instance:  instance,
			Target:    uploadFile,
			Attempt:   int(n),
			Status:    artifact.PublishAttemptFailure,
			Error:     lastErr.Error(),
		})

		if err := ctx.Err(); err != nil {
			return err
		}
		if n == attempts || !isTransientBlobErr(lastErr) {
			return handleError(lastErr, bucketURL)
		}
		if err := conf.Retry.Sleep(ctx, n, 0); err != nil {
			return err
		}
	}
	if lastErr != nil {
		return handleError(lastErr, bucketURL)
	}
	return nil
}

func retryBlobOp(ctx *context.Context, cfg config.Retry, fn func() error) error {
	attempts := cfg.MaxAttempts()
	var err error
	for n := uint(1); n <= attempts; n++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		err = fn()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if n == attempts || !isTransientBlobErr(err) {
			return err
		}
		if serr := cfg.Sleep(ctx, n, 0); serr != nil {
			return serr
		}
	}
	return err
}

type timeoutError interface {
	Timeout() bool
}

type temporaryError interface {
	Temporary() bool
}

func isTransientBlobErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, stdctx.Canceled) || errors.Is(err, stdctx.DeadlineExceeded) {
		return false
	}
	var te timeoutError
	if errors.As(err, &te) && te.Timeout() {
		return true
	}
	var tmp temporaryError
	if errors.As(err, &tmp) && tmp.Temporary() {
		return true
	}
	return false
}

// errorContains check if error contains specific string.
func errorContains(err error, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(err.Error(), sub) {
			return true
		}
	}
	return false
}

func handleError(err error, url string) error {
	switch {
	case errorContains(err, "NoSuchBucket", "ContainerNotFound", "notFound"):
		return fmt.Errorf("provided bucket does not exist: %s: %w", url, err)
	case errorContains(err, "NoCredentialProviders"):
		return fmt.Errorf("check credentials and access to bucket: %s: %w", url, err)
	case errorContains(err, "InvalidAccessKeyId"):
		return fmt.Errorf("aws access key id you provided does not exist in our records: %w", err)
	case errorContains(err, "AuthenticationFailed"):
		return fmt.Errorf("azure storage key you provided is not valid: %w", err)
	case errorContains(err, "invalid_grant"):
		return fmt.Errorf("google app credentials you provided is not valid: %w", err)
	case errorContains(err, "no such host"):
		return fmt.Errorf("azure storage account you provided is not valid: %w", err)
	case errorContains(err, "ServiceCode=ResourceNotFound"):
		return fmt.Errorf("missing azure storage key for provided bucket %s: %w", url, err)
	default:
		return fmt.Errorf("failed to write to bucket: %w", err)
	}
}

func getData(ctx *context.Context, conf config.Blob, path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return data, fmt.Errorf("failed to open file %s: %w", path, err)
	}
	if conf.KMSKey == "" {
		return data, nil
	}
	keeper, err := secrets.OpenKeeper(ctx, conf.KMSKey)
	if err != nil {
		return data, fmt.Errorf("failed to open kms %s: %w", conf.KMSKey, err)
	}
	defer keeper.Close()
	data, err = keeper.Encrypt(ctx, data)
	if err != nil {
		return data, fmt.Errorf("failed to encrypt with kms: %w", err)
	}
	return data, err
}

// uploader implements upload.
type uploader interface {
	io.Closer
	Open(ctx *context.Context, url string) error
	Upload(ctx *context.Context, path string, data []byte) error
}

// productionUploader actually do upload to.
type productionUploader struct {
	bucket             *blob.Bucket
	beforeWrite        func(asFunc func(any) bool) error
	cacheControl       []string
	contentDisposition string
}

func (u *productionUploader) Close() error {
	if u.bucket == nil {
		return nil
	}
	return u.bucket.Close()
}

func (u *productionUploader) Open(ctx *context.Context, bucket string) error {
	log.WithField("bucket", bucket).Debug("uploading")

	conn, err := blob.OpenBucket(ctx, bucket)
	if err != nil {
		return err
	}
	u.bucket = conn
	return nil
}

func (u *productionUploader) Upload(ctx *context.Context, filepath string, data []byte) error {
	log.WithField("path", filepath).Info("uploading")

	disp, err := tmpl.New(ctx).WithExtraFields(tmpl.Fields{
		"Filename": path.Base(filepath),
	}).Apply(u.contentDisposition)
	if err != nil {
		return err
	}

	opts := &blob.WriterOptions{
		ContentDisposition: disp,
		BeforeWrite:        u.beforeWrite,
		CacheControl:       strings.Join(u.cacheControl, ", "),
	}
	w, err := u.bucket.NewWriter(ctx, filepath, opts)
	if err != nil {
		return err
	}
	defer func() { _ = w.Close() }()
	if _, err = w.Write(data); err != nil {
		return err
	}
	return w.Close()
}
