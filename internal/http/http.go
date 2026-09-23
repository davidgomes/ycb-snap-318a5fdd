// Package http implements functionality common to HTTP uploading pipelines.
package http

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"math"
	h "net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/caarlos0/log"
	"github.com/goreleaser/goreleaser/v2/internal/artifact"
	"github.com/goreleaser/goreleaser/v2/internal/extrafiles"
	"github.com/goreleaser/goreleaser/v2/internal/pipe"
	"github.com/goreleaser/goreleaser/v2/internal/retry"
	"github.com/goreleaser/goreleaser/v2/internal/semerrgroup"
	"github.com/goreleaser/goreleaser/v2/internal/tmpl"
	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/goreleaser/goreleaser/v2/pkg/context"
)

const (
	// ModeBinary uploads only compiled binaries.
	ModeBinary = "binary"
	// ModeArchive uploads release archives.
	ModeArchive = "archive"
)

type asset struct {
	ReadCloser io.ReadCloser
	Size       int64
}

type assetOpenFunc func(string, *artifact.Artifact) (*asset, error)

//nolint:gochecknoglobals
var assetOpen assetOpenFunc

// TODO: fix this.
//
//nolint:gochecknoinits
func init() {
	assetOpenReset()
}

func assetOpenReset() {
	assetOpen = assetOpenDefault
}

// TODO: this should probably return a func()error always so we can properly
// handle closing the file.
func assetOpenDefault(kind string, a *artifact.Artifact) (*asset, error) {
	f, err := os.Open(a.Path)
	if err != nil {
		return nil, err
	}
	s, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if s.IsDir() {
		_ = f.Close()
		return nil, fmt.Errorf("%s: upload failed: the asset to upload can't be a directory", kind)
	}
	return &asset{
		// Wrap the file so it only exposes io.Reader and io.Closer.
		// This prevents Go's HTTP client from detecting *os.File and
		// using sendFile/TransmitFile on Windows, which has a known
		// data race between the connection's readLoop and writeBody
		// goroutines on the underlying TCP socket FD.
		// See: https://github.com/golang/go/issues/78015
		ReadCloser: struct {
			io.Reader
			io.Closer
		}{Reader: f, Closer: f},
		Size: s.Size(),
	}, nil
}

// Defaults sets default configuration options on upload structs.
func Defaults(uploads []config.Upload) error {
	for i := range uploads {
		defaults(&uploads[i])
	}
	return nil
}

func defaults(upload *config.Upload) {
	if upload.Mode == "" {
		upload.Mode = ModeArchive
	}
	if upload.Method == "" {
		upload.Method = h.MethodPut
	}
}

// CheckConfig validates an upload configuration returning a descriptive error when appropriate.
func CheckConfig(ctx *context.Context, upload *config.Upload, kind string) error {
	if upload.Target == "" {
		return misconfigured(kind, upload, "missing target")
	}

	if upload.Name == "" {
		return misconfigured(kind, upload, "missing name")
	}

	if upload.Mode != ModeArchive && upload.Mode != ModeBinary {
		return misconfigured(kind, upload, "mode must be 'binary' or 'archive'")
	}

	username, err := getUsername(ctx, upload, kind)
	if err != nil {
		return fmt.Errorf("%s: could not get username: %w", upload.Name, err)
	}

	password, err := getPassword(ctx, upload, kind)
	if err != nil {
		return fmt.Errorf("%s: could not get password: %w", upload.Name, err)
	}

	passwordEnv := fmt.Sprintf("%s_%s_SECRET", strings.ToUpper(kind), strings.ToUpper(upload.Name))

	if password != "" && username == "" {
		return misconfigured(kind, upload, fmt.Sprintf("'username' is required when 'password' or the '%s' environment variable are set", passwordEnv))
	}

	if username != "" && password == "" {
		return misconfigured(kind, upload, fmt.Sprintf("either 'password' or environment variable '%s' are required when 'username' is set", passwordEnv))
	}

	if upload.TrustedCerts != "" && !x509.NewCertPool().AppendCertsFromPEM([]byte(upload.TrustedCerts)) {
		return misconfigured(kind, upload, "no certificate could be added from the specified trusted_certificates configuration")
	}

	if upload.ClientX509Cert != "" && upload.ClientX509Key == "" {
		return misconfigured(kind, upload, "'client_x509_key' must be set when 'client_x509_cert' is set")
	}
	if upload.ClientX509Key != "" && upload.ClientX509Cert == "" {
		return misconfigured(kind, upload, "'client_x509_cert' must be set when 'client_x509_key' is set")
	}
	if upload.ClientX509Cert != "" && upload.ClientX509Key != "" {
		if _, err := tls.LoadX509KeyPair(upload.ClientX509Cert, upload.ClientX509Key); err != nil {
			return misconfigured(kind, upload,
				"client x509 certificate could not be loaded from the specified 'client_x509_cert' and 'client_x509_key'")
		}
	}

	return nil
}

// username is optional
func getUsername(ctx *context.Context, upload *config.Upload, kind string) (string, error) {
	username, err := tmpl.New(ctx).Apply(upload.Username)
	if err != nil {
		return "", err
	}
	if username != "" {
		return username, nil
	}
	key := fmt.Sprintf("%s_%s_USERNAME", strings.ToUpper(kind), strings.ToUpper(upload.Name))
	return ctx.Env[key], nil
}

// password is optional
func getPassword(ctx *context.Context, upload *config.Upload, kind string) (string, error) {
	password, err := tmpl.New(ctx).Apply(upload.Password)
	if err != nil {
		return "", err
	}
	if password != "" {
		return password, nil
	}
	key := fmt.Sprintf("%s_%s_SECRET", strings.ToUpper(kind), strings.ToUpper(upload.Name))
	return ctx.Env[key], nil
}

func misconfigured(kind string, upload *config.Upload, reason string) error {
	return pipe.Skipf("%s section '%s' is not configured properly (%s)", kind, upload.Name, reason)
}

// ResponseChecker is a function capable of validating an http server response.
// It must return and error when the response must be considered a failure.
type ResponseChecker func(*h.Response) error

// Upload does the actual uploading work.
func Upload(ctx *context.Context, uploads []config.Upload, kind string, check ResponseChecker) error {
	skips := &pipe.SkipMemento{}
	// Handle every configured upload
	for _, upload := range uploads {
		err := uploadOne(ctx, upload, kind, check)
		if pipe.IsSkip(err) {
			skips.Remember(err)
			continue
		}
		if err != nil {
			return err
		}
	}

	return skips.Evaluate()
}

func uploadOne(ctx *context.Context, upload config.Upload, kind string, check ResponseChecker) error {
	skip, err := tmpl.New(ctx).Bool(upload.Skip)
	if err != nil {
		return err
	}
	if skip {
		return pipe.Skip("skip evaluates to true")
	}

	types := []artifact.Type{}
	if upload.Checksum {
		types = append(types, artifact.Checksum)
	}
	if upload.Meta {
		types = append(types, artifact.Metadata)
	}
	if upload.Signature {
		types = append(types, artifact.Signature, artifact.Certificate)
	}
	// We support two different modes
	//	- "archive": Upload all artifacts
	//	- "binary": Upload only the raw binaries
	switch v := strings.ToLower(upload.Mode); v {
	case ModeArchive:
		types = append(
			types,
			artifact.UploadableArchive,
			artifact.UploadableSourceArchive,
			artifact.Makeself,
			artifact.LinuxPackage,
			artifact.Flatpak,
			artifact.PySdist,
			artifact.PyWheel,
		)
	case ModeBinary:
		types = append(types, artifact.UploadableBinary)
	default:
		return fmt.Errorf("%s: %s: mode \"%s\" not supported", upload.Name, kind, v)
	}

	filter := artifact.And(
		artifact.ByTypes(types...),
		artifact.ByIDs(upload.IDs...),
		artifact.Or(
			artifact.ByExts(upload.Exts...),
			artifact.ByFormats(upload.Exts...),
		),
	)
	if err := uploadWithFilter(ctx, &upload, filter, kind, check); err != nil {
		return err
	}
	return nil
}

func uploadWithFilter(ctx *context.Context, upload *config.Upload, filter artifact.Filter, kind string, check ResponseChecker) error {
	var artifacts []*artifact.Artifact
	extraFiles, err := extrafiles.Find(ctx, upload.ExtraFiles)
	if err != nil {
		return err
	}

	for name, path := range extraFiles {
		artifacts = append(artifacts, &artifact.Artifact{
			Name: name,
			Path: path,
			Type: artifact.UploadableFile,
		})
	}

	if !upload.ExtraFilesOnly {
		artifacts = append(artifacts, ctx.Artifacts.Filter(filter).List()...)
	}

	if len(artifacts) == 0 {
		log.Info("no artifacts found")
	}
	log.Debugf("will upload %d artifacts", len(artifacts))
	attempts := &artifact.PublishAttempts{}
	g := semerrgroup.New(ctx.Parallelism)
	for _, artifact := range artifacts {
		g.Go(func() error {
			return uploadAsset(ctx, upload, artifact, kind, check, attempts)
		})
	}
	err = g.Wait()
	attempts.Apply()
	return err
}

// uploadAsset uploads file to target and logs all actions.
func uploadAsset(ctx *context.Context, upload *config.Upload, artifact *artifact.Artifact, kind string, check ResponseChecker, attempts *artifact.PublishAttempts) error {
	// username and secret are optional since the server may not support/need
	// basic authentication always
	username, err := getUsername(ctx, upload, kind)
	if err != nil {
		return fmt.Errorf("%s: could not get username: %w", upload.Name, err)
	}
	secret, err := getPassword(ctx, upload, kind)
	if err != nil {
		return fmt.Errorf("%s: could not get password: %w", upload.Name, err)
	}

	// Generate the target url
	targetURL, err := tmpl.New(ctx).WithArtifact(artifact).Apply(upload.Target)
	if err != nil {
		return fmt.Errorf("%s: %s: error while building target URL: %w", upload.Name, kind, err)
	}

	// Handle the artifact
	asset, err := assetOpen(kind, artifact)
	if err != nil {
		return err
	}
	defer asset.ReadCloser.Close()

	// target url need to contain the artifact name unless the custom
	// artifact name is used
	if !upload.CustomArtifactName {
		if !strings.HasSuffix(targetURL, "/") {
			targetURL += "/"
		}
		targetURL += artifact.Name
	}
	log.Debugf("generated target url: %s", targetURL)

	headers := make(map[string]string, len(upload.CustomHeaders))
	for name, value := range upload.CustomHeaders {
		resolvedValue, err := tmpl.New(ctx).WithArtifact(artifact).Apply(value)
		if err != nil {
			return fmt.Errorf("%s: %s: failed to resolve custom_headers template: %w", upload.Name, kind, err)
		}
		headers[name] = resolvedValue
	}
	if upload.ChecksumHeader != "" {
		sum, err := artifact.Checksum("sha256")
		if err != nil {
			return err
		}
		headers[upload.ChecksumHeader] = sum
	}

	log.WithField("instance", upload.Name).
		WithField("mode", upload.Mode).
		WithField("file", artifact.Name).
		Info("uploading")

	var lastErr error
	err = retry.Do(ctx, upload.Retry, isRetriable, func(attempt int) error {
		if attempt > 1 {
			log.WithField("instance", upload.Name).
				WithField("file", artifact.Name).
				WithField("attempt", attempt).
				WithError(lastErr).
				Warn("retrying upload")
		}
		lastErr = uploadAttempt(ctx, upload, artifact, kind, targetURL, username, secret, headers, asset, attempt, check)
		attempts.Record(artifact, kind, upload.Name, targetURL, attempt, lastErr)
		return lastErr
	})
	if err != nil {
		return fmt.Errorf("%s: %s: upload failed: %w", upload.Name, kind, err)
	}
	return nil
}

// uploadAttempt makes a single attempt at uploading the artifact.
// The first attempt uses the already opened asset, the next ones reopen it so
// the whole content is sent again.
func uploadAttempt(ctx *context.Context, upload *config.Upload, a *artifact.Artifact, kind, target, username, secret string, headers map[string]string, first *asset, attempt int, check ResponseChecker) error {
	asset := first
	if attempt > 1 {
		var err error
		asset, err = assetOpen(kind, a)
		if err != nil {
			return err
		}
		defer asset.ReadCloser.Close()
	}
	res, err := uploadAssetToServer(ctx, upload, target, username, secret, headers, asset, check)
	if err != nil {
		return err
	}
	if err := res.Body.Close(); err != nil {
		log.WithError(err).Warn("failed to close response body")
	}
	return nil
}

// uploadAssetToServer uploads the asset file to target.
func uploadAssetToServer(ctx *context.Context, upload *config.Upload, target, username, secret string, headers map[string]string, a *asset, check ResponseChecker) (*h.Response, error) {
	req, err := newUploadRequest(ctx, upload.Method, target, username, secret, headers, a)
	if err != nil {
		return nil, err
	}

	return executeHTTPRequest(ctx, upload, req, check)
}

// newUploadRequest creates a new h.Request for uploading.
func newUploadRequest(ctx *context.Context, method, target, username, secret string, headers map[string]string, a *asset) (*h.Request, error) {
	req, err := h.NewRequestWithContext(ctx, method, target, a.ReadCloser)
	if err != nil {
		return nil, err
	}
	req.ContentLength = a.Size

	if username != "" && secret != "" {
		req.SetBasicAuth(username, secret)
	}

	for k, v := range headers {
		req.Header.Add(k, v)
	}

	return req, err
}

func getHTTPClient(upload *config.Upload) (*h.Client, error) {
	if upload.TrustedCerts == "" && upload.ClientX509Cert == "" && upload.ClientX509Key == "" {
		return h.DefaultClient, nil
	}
	transport := &h.Transport{
		Proxy:           h.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{},
	}
	if upload.TrustedCerts != "" {
		pool, err := x509.SystemCertPool()
		if err != nil {
			if runtime.GOOS == "windows" {
				// on windows ignore errors until golang issues #16736 & #18609 get fixed
				pool = x509.NewCertPool()
			} else {
				return nil, err
			}
		}
		pool.AppendCertsFromPEM([]byte(upload.TrustedCerts)) // already validated certs checked by CheckConfig
		transport.TLSClientConfig.RootCAs = pool
	}
	if upload.ClientX509Cert != "" && upload.ClientX509Key != "" {
		cert, err := tls.LoadX509KeyPair(upload.ClientX509Cert, upload.ClientX509Key)
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig.Certificates = []tls.Certificate{cert}
	}
	return &h.Client{Transport: transport}, nil
}

// executeHTTPRequest processes the http call with respect of context ctx.
func executeHTTPRequest(ctx *context.Context, upload *config.Upload, req *h.Request, check ResponseChecker) (*h.Response, error) {
	client, err := getHTTPClient(upload)
	if err != nil {
		return nil, err
	}
	log.Debugf("executing request: %s %s (headers: %v)", req.Method, req.URL, req.Header)
	resp, err := client.Do(req)
	if err != nil {
		// If we got an error, and the context has been canceled,
		// the context's error is probably more useful.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		return nil, &transportError{err: err}
	}

	defer resp.Body.Close()

	err = check(resp)
	if err != nil {
		// even though there was an error, we still return the response
		// in case the caller wants to inspect it further
		return resp, &responseError{
			err:        err,
			statusCode: resp.StatusCode,
			retryAfter: resp.Header.Get("Retry-After"),
		}
	}

	return resp, err
}

// transportError is an error that happened while sending the request, before
// getting any response.
type transportError struct {
	err error
}

func (e *transportError) Error() string { return e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

// responseError is an error returned by a [ResponseChecker].
type responseError struct {
	err        error
	statusCode int
	retryAfter string
}

func (e *responseError) Error() string { return e.err.Error() }
func (e *responseError) Unwrap() error { return e.err }

// isRetriable tells whether a failed upload should be retried, and how long
// the server asked us to wait before doing so.
func isRetriable(err error) (bool, time.Duration) {
	if terr := (*transportError)(nil); errors.As(err, &terr) {
		return true, 0
	}
	rerr := (*responseError)(nil)
	if !errors.As(err, &rerr) {
		return false, 0
	}
	switch rerr.statusCode {
	case h.StatusTooManyRequests, h.StatusServiceUnavailable:
		wait, _ := parseRetryAfter(rerr.retryAfter, time.Now())
		return true, wait
	case h.StatusRequestTimeout,
		h.StatusInternalServerError,
		h.StatusBadGateway,
		h.StatusGatewayTimeout:
		return true, 0
	default:
		return false, 0
	}
}

// parseRetryAfter parses a Retry-After header value, which can be either
// delta-seconds or an HTTP-date.
func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if strings.Trim(value, "0123456789") == "" {
		secs, err := strconv.ParseInt(value, 10, 64)
		if err != nil || secs > math.MaxInt64/int64(time.Second) {
			// all digits, so the only possible error is overflow.
			return math.MaxInt64, true
		}
		return time.Duration(secs) * time.Second, true
	}
	date, err := h.ParseTime(value)
	if err != nil {
		return 0, false
	}
	return max(date.Sub(now), 0), true
}
