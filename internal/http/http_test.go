package http

import (
	"bytes"
	stdctx "context"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goreleaser/goreleaser/v2/internal/artifact"
	"github.com/goreleaser/goreleaser/v2/internal/pipe"
	"github.com/goreleaser/goreleaser/v2/internal/testctx"
	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/goreleaser/goreleaser/v2/pkg/context"
	"github.com/stretchr/testify/require"
)

func TestAssetOpenDefault(t *testing.T) {
	tf := filepath.Join(t.TempDir(), "asset")
	require.NoError(t, os.WriteFile(tf, []byte("a"), 0o765))

	a, err := assetOpenDefault("blah", &artifact.Artifact{
		Path: tf,
	})
	if err != nil {
		t.Fatalf("can not open asset: %v", err)
	}
	t.Cleanup(func() {
		require.NoError(t, a.ReadCloser.Close())
	})
	bs, err := io.ReadAll(a.ReadCloser)
	if err != nil {
		t.Fatalf("can not read asset: %v", err)
	}
	if string(bs) != "a" {
		t.Fatalf("unexpected read content")
	}
	_, err = assetOpenDefault("blah", &artifact.Artifact{
		Path: "blah",
	})
	if err == nil {
		t.Fatalf("should fail on missing file")
	}
	_, err = assetOpenDefault("blah", &artifact.Artifact{
		Path: t.TempDir(),
	})
	if err == nil {
		t.Fatalf("should fail on existing dir")
	}
}

func TestDefaults(t *testing.T) {
	type args struct {
		uploads []config.Upload
	}
	tests := []struct {
		name     string
		args     args
		wantErr  bool
		wantMode string
	}{
		{"set default", args{[]config.Upload{{Name: "a", Target: "http://"}}}, false, ModeArchive},
		{"keep value", args{[]config.Upload{{Name: "a", Target: "http://...", Mode: ModeBinary}}}, false, ModeBinary},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Defaults(tt.args.uploads); (err != nil) != tt.wantErr {
				t.Errorf("Defaults() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantMode != tt.args.uploads[0].Mode {
				t.Errorf("Incorrect Defaults() mode %q , wanted %q", tt.args.uploads[0].Mode, tt.wantMode)
			}
		})
	}
}

func TestCheckConfig(t *testing.T) {
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{
		ProjectName: "blah",
		Env:         []string{"TEST_A_SECRET=x"},
	})

	type args struct {
		ctx    *context.Context
		upload *config.Upload
		kind   string
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{"ok", args{ctx, &config.Upload{Name: "a", Target: "http://blabla", Username: "pepe", Mode: ModeArchive}, "test"}, false},
		{"ok password", args{ctx, &config.Upload{Name: "a", Target: "http://blabla", Username: "pepe", Password: "pass", Mode: ModeArchive}, "test"}, false},
		{"secret missing", args{ctx, &config.Upload{Name: "b", Target: "http://blabla", Username: "pepe", Mode: ModeArchive}, "test"}, true},
		{"target missing", args{ctx, &config.Upload{Name: "a", Username: "pepe", Mode: ModeArchive}, "test"}, true},
		{"name missing", args{ctx, &config.Upload{Target: "http://blabla", Username: "pepe", Mode: ModeArchive}, "test"}, true},
		{"username missing", args{ctx, &config.Upload{Name: "a", Target: "http://blabla", Mode: ModeArchive}, "test"}, true},
		{"username present", args{ctx, &config.Upload{Name: "a", Target: "http://blabla", Username: "pepe", Mode: ModeArchive}, "test"}, false},
		{"invalid username template", args{ctx, &config.Upload{Name: "a", Target: "http://blabla", Username: "{{ .pepe }}", Mode: ModeArchive}, "test"}, true},
		{"invalid password template", args{ctx, &config.Upload{Name: "a", Target: "http://blabla", Password: "{{ .pepe }}", Mode: ModeArchive}, "test"}, true},
		{"mode missing", args{ctx, &config.Upload{Name: "a", Target: "http://blabla", Username: "pepe"}, "test"}, true},
		{"mode invalid", args{ctx, &config.Upload{Name: "a", Target: "http://blabla", Username: "pepe", Mode: "blabla"}, "test"}, true},
		{"cert invalid", args{ctx, &config.Upload{Name: "a", Target: "http://blabla", Username: "pepe", Mode: ModeBinary, TrustedCerts: "bad cert!"}, "test"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := CheckConfig(tt.args.ctx, tt.args.upload, tt.args.kind); (err != nil) != tt.wantErr {
				t.Errorf("CheckConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}

	delete(ctx.Env, "TEST_A_SECRET")

	tests = []struct {
		name    string
		args    args
		wantErr bool
	}{
		{"username missing", args{ctx, &config.Upload{Name: "a", Target: "http://blabla", Mode: ModeArchive}, "test"}, false},
		{"username present", args{ctx, &config.Upload{Name: "a", Target: "http://blabla", Username: "pepe", Mode: ModeArchive}, "test"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := CheckConfig(tt.args.ctx, tt.args.upload, tt.args.kind); (err != nil) != tt.wantErr {
				t.Errorf("CheckConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

type check struct {
	path    string
	user    string
	pass    string
	content []byte
	headers map[string]string
}

func checks(checks ...check) func(rs []*http.Request) error {
	return func(rs []*http.Request) error {
		for _, r := range rs {
			found := false
			for _, c := range checks {
				if c.path == r.RequestURI {
					found = true
					err := doCheck(c, r)
					if err != nil {
						return err
					}
					break
				}
			}
			if !found {
				return fmt.Errorf("check not found for request %+v", r)
			}
		}
		if len(rs) != len(checks) {
			return fmt.Errorf("expected %d requests, got %d", len(checks), len(rs))
		}
		return nil
	}
}

func doCheck(c check, r *http.Request) error {
	contentLength := int64(len(c.content))
	if r.ContentLength != contentLength {
		return fmt.Errorf("request content-length header value %v unexpected, wanted %v", r.ContentLength, contentLength)
	}
	bs, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("reading request body: %v", err)
	}
	if !bytes.Equal(bs, c.content) {
		return errors.New("content does not match")
	}
	if int64(len(bs)) != contentLength {
		return fmt.Errorf("request content length %v unexpected, wanted %v", int64(len(bs)), contentLength)
	}
	if r.RequestURI != c.path {
		return fmt.Errorf("bad request uri %q, expecting %q", r.RequestURI, c.path)
	}
	if u, p, ok := r.BasicAuth(); !ok || u != c.user || p != c.pass {
		return fmt.Errorf("bad basic auth credentials: %s/%s", u, p)
	}
	for k, v := range c.headers {
		if r.Header.Get(k) != v {
			return fmt.Errorf("bad header value for %s: expected %s, got %s", k, v, r.Header.Get(k))
		}
	}
	return nil
}

func TestUpload(t *testing.T) {
	content := []byte("blah!")
	requests := []*http.Request{}
	var m sync.Mutex
	mux := http.NewServeMux()
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bs, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "reading request body: %v", err)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(bs))
		m.Lock()
		requests = append(requests, r)
		m.Unlock()
		w.WriteHeader(http.StatusCreated)
		w.Header().Set("Location", r.URL.RequestURI())
	}))
	assetOpen = func(_ string, _ *artifact.Artifact) (*asset, error) {
		return &asset{
			ReadCloser: io.NopCloser(bytes.NewReader(content)),
			Size:       int64(len(content)),
		}, nil
	}
	defer assetOpenReset()
	var is2xx ResponseChecker = func(r *http.Response) error {
		if r.StatusCode/100 == 2 {
			return nil
		}
		return fmt.Errorf("unexpected http status code: %v", r.StatusCode)
	}
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{
		ProjectName: "blah",
		Env: []string{
			"TEST_A_SECRET=x",
			"TEST_A_USERNAME=u2",
		},
	}, testctx.WithVersion("2.1.0"))

	folder := t.TempDir()
	for _, a := range []struct {
		ext, format string
		typ         artifact.Type
	}{
		{"", "", artifact.DockerImage},
		{".deb", "", artifact.LinuxPackage},
		{".bin", "", artifact.Binary},
		{".tar", "tar", artifact.UploadableArchive},
		{".tar.gz", "tar.gz", artifact.UploadableSourceArchive},
		{".ubi", "", artifact.UploadableBinary},
		{".sum", "", artifact.Checksum},
		{".meta", "", artifact.Metadata},
		{".sig", "", artifact.Signature},
		{".pem", "", artifact.Certificate},
	} {
		file := filepath.Join(folder, "a"+a.ext)
		require.NoError(t, os.WriteFile(file, []byte("lorem ipsum"), 0o644))
		extra := map[string]any{
			artifact.ExtraID: "foo",
		}
		if a.format != "" {
			extra[artifact.ExtraFormat] = a.format
		} else if a.ext != "" {
			extra[artifact.ExtraExt] = a.ext
		}
		ctx.Artifacts.Add(&artifact.Artifact{
			Name:   "a" + a.ext,
			Goos:   "linux",
			Goarch: "amd64",
			Path:   file,
			Type:   a.typ,
			Extra:  extra,
		})
	}

	tests := []struct {
		name         string
		tryPlain     bool
		tryTLS       bool
		wantErrPlain bool
		wantErrTLS   bool
		setup        func(*httptest.Server) (*context.Context, config.Upload)
		check        func(r []*http.Request) error
	}{
		{
			"wrong-mode", true, true, true, true,
			func(s *httptest.Server) (*context.Context, config.Upload) {
				return ctx, config.Upload{
					Mode:         "wrong-mode",
					Name:         "a",
					Target:       s.URL + "/{{.ProjectName}}/{{.Version}}/",
					Username:     "u1",
					TrustedCerts: cert(s),
				}
			},
			checks(),
		},
		{
			"username-from-env", true, true, false, false,
			func(s *httptest.Server) (*context.Context, config.Upload) {
				return ctx, config.Upload{
					Mode:         ModeArchive,
					Name:         "a",
					Target:       s.URL + "/{{.ProjectName}}/{{.Version}}/",
					TrustedCerts: cert(s),
				}
			},
			checks(
				check{"/blah/2.1.0/a.deb", "u2", "x", content, map[string]string{}},
				check{"/blah/2.1.0/a.tar", "u2", "x", content, map[string]string{}},
				check{"/blah/2.1.0/a.tar.gz", "u2", "x", content, map[string]string{}},
			),
		},
		{
			"post", true, true, false, false,
			func(s *httptest.Server) (*context.Context, config.Upload) {
				return ctx, config.Upload{
					Method:       http.MethodPost,
					Mode:         ModeArchive,
					Name:         "a",
					Target:       s.URL + "/{{.ProjectName}}/{{.Version}}/",
					Username:     "u1",
					TrustedCerts: cert(s),
				}
			},
			checks(
				check{"/blah/2.1.0/a.deb", "u1", "x", content, map[string]string{}},
				check{"/blah/2.1.0/a.tar", "u1", "x", content, map[string]string{}},
				check{"/blah/2.1.0/a.tar.gz", "u1", "x", content, map[string]string{}},
			),
		},
		{
			"archive", true, true, false, false,
			func(s *httptest.Server) (*context.Context, config.Upload) {
				return ctx, config.Upload{
					Mode:         ModeArchive,
					Name:         "a",
					Target:       s.URL + "/{{.ProjectName}}/{{.Version}}/",
					Username:     "u1",
					TrustedCerts: cert(s),
				}
			},
			checks(
				check{"/blah/2.1.0/a.deb", "u1", "x", content, map[string]string{}},
				check{"/blah/2.1.0/a.tar", "u1", "x", content, map[string]string{}},
				check{"/blah/2.1.0/a.tar.gz", "u1", "x", content, map[string]string{}},
			),
		},
		{
			"archive_with_os_tmpl", true, true, false, false,
			func(s *httptest.Server) (*context.Context, config.Upload) {
				return ctx, config.Upload{
					Mode:         ModeArchive,
					Name:         "a",
					Target:       s.URL + "/{{.ProjectName}}/{{.Version}}/{{.Os}}/{{.Arch}}",
					Username:     "u1",
					TrustedCerts: cert(s),
				}
			},
			checks(
				check{"/blah/2.1.0/linux/amd64/a.deb", "u1", "x", content, map[string]string{}},
				check{"/blah/2.1.0/linux/amd64/a.tar", "u1", "x", content, map[string]string{}},
				check{"/blah/2.1.0/linux/amd64/a.tar.gz", "u1", "x", content, map[string]string{}},
			),
		},
		{
			"archive_with_ids", true, true, false, false,
			func(s *httptest.Server) (*context.Context, config.Upload) {
				return ctx, config.Upload{
					Mode:         ModeArchive,
					Name:         "a",
					Target:       s.URL + "/{{.ProjectName}}/{{.Version}}/",
					Username:     "u1",
					TrustedCerts: cert(s),
					IDs:          []string{"foo"},
				}
			},
			checks(
				check{"/blah/2.1.0/a.deb", "u1", "x", content, map[string]string{}},
				check{"/blah/2.1.0/a.tar", "u1", "x", content, map[string]string{}},
				check{"/blah/2.1.0/a.tar.gz", "u1", "x", content, map[string]string{}},
			),
		},
		{
			"binary", true, true, false, false,
			func(s *httptest.Server) (*context.Context, config.Upload) {
				return ctx, config.Upload{
					Mode:         ModeBinary,
					Name:         "a",
					Target:       s.URL + "/{{.ProjectName}}/{{.Version}}/",
					Username:     "u2",
					TrustedCerts: cert(s),
				}
			},
			checks(check{"/blah/2.1.0/a.ubi", "u2", "x", content, map[string]string{}}),
		},
		{
			"binary_with_os_tmpl", true, true, false, false,
			func(s *httptest.Server) (*context.Context, config.Upload) {
				return ctx, config.Upload{
					Mode:         ModeBinary,
					Name:         "a",
					Target:       s.URL + "/{{.ProjectName}}/{{.Version}}/{{.Os}}/{{.Arch}}",
					Username:     "u2",
					TrustedCerts: cert(s),
				}
			},
			checks(check{"/blah/2.1.0/linux/amd64/a.ubi", "u2", "x", content, map[string]string{}}),
		},
		{
			"binary_with_ids", true, true, false, false,
			func(s *httptest.Server) (*context.Context, config.Upload) {
				return ctx, config.Upload{
					Mode:         ModeBinary,
					Name:         "a",
					Target:       s.URL + "/{{.ProjectName}}/{{.Version}}/",
					Username:     "u2",
					TrustedCerts: cert(s),
					IDs:          []string{"foo"},
				}
			},
			checks(check{"/blah/2.1.0/a.ubi", "u2", "x", content, map[string]string{}}),
		},
		{
			"binary-add-ending-bar", true, true, false, false,
			func(s *httptest.Server) (*context.Context, config.Upload) {
				return ctx, config.Upload{
					Mode:         ModeBinary,
					Name:         "a",
					Target:       s.URL + "/{{.ProjectName}}/{{.Version}}",
					Username:     "u2",
					TrustedCerts: cert(s),
				}
			},
			checks(check{"/blah/2.1.0/a.ubi", "u2", "x", content, map[string]string{}}),
		},
		{
			"archive-with-checksum-and-signature", true, true, false, false,
			func(s *httptest.Server) (*context.Context, config.Upload) {
				return ctx, config.Upload{
					Mode:         ModeArchive,
					Name:         "a",
					Target:       s.URL + "/{{.ProjectName}}/{{.Version}}/",
					Username:     "u3",
					Checksum:     true,
					Signature:    true,
					TrustedCerts: cert(s),
				}
			},
			checks(
				check{"/blah/2.1.0/a.deb", "u3", "x", content, map[string]string{}},
				check{"/blah/2.1.0/a.tar", "u3", "x", content, map[string]string{}},
				check{"/blah/2.1.0/a.tar.gz", "u3", "x", content, map[string]string{}},
				check{"/blah/2.1.0/a.sum", "u3", "x", content, map[string]string{}},
				check{"/blah/2.1.0/a.sig", "u3", "x", content, map[string]string{}},
				check{"/blah/2.1.0/a.pem", "u3", "x", content, map[string]string{}},
			),
		},
		{
			"metadata", true, true, false, false,
			func(s *httptest.Server) (*context.Context, config.Upload) {
				return ctx, config.Upload{
					Mode:         ModeArchive,
					Name:         "a",
					Target:       s.URL + "/{{.ProjectName}}/{{.Version}}/",
					Username:     "u3",
					Meta:         true,
					TrustedCerts: cert(s),
				}
			},
			checks(
				check{"/blah/2.1.0/a.deb", "u3", "x", content, map[string]string{}},
				check{"/blah/2.1.0/a.tar", "u3", "x", content, map[string]string{}},
				check{"/blah/2.1.0/a.tar.gz", "u3", "x", content, map[string]string{}},
				check{"/blah/2.1.0/a.meta", "u3", "x", content, map[string]string{}},
			),
		},
		{
			"bad-template", true, true, true, true,
			func(s *httptest.Server) (*context.Context, config.Upload) {
				return ctx, config.Upload{
					Mode:         ModeBinary,
					Name:         "a",
					Target:       s.URL + "/{{.ProjectNameXXX}}/{{.VersionXXX}}/",
					Username:     "u3",
					Checksum:     true,
					Signature:    true,
					TrustedCerts: cert(s),
				}
			},
			checks(),
		},
		{
			"failed-request", true, true, true, true,
			func(s *httptest.Server) (*context.Context, config.Upload) {
				return ctx, config.Upload{
					Mode:         ModeBinary,
					Name:         "a",
					Target:       s.URL[0:strings.LastIndex(s.URL, ":")] + "/{{.ProjectName}}/{{.Version}}/",
					Username:     "u3",
					Checksum:     true,
					Signature:    true,
					TrustedCerts: cert(s),
				}
			},
			checks(),
		},
		{
			"broken-cert", false, true, false, true,
			func(s *httptest.Server) (*context.Context, config.Upload) {
				return ctx, config.Upload{
					Mode:         ModeBinary,
					Name:         "a",
					Target:       s.URL + "/{{.ProjectName}}/{{.Version}}/",
					Username:     "u3",
					Checksum:     false,
					Signature:    false,
					TrustedCerts: "bad certs!",
				}
			},
			checks(),
		},
		{
			"checksumheader", true, true, false, false,
			func(s *httptest.Server) (*context.Context, config.Upload) {
				return ctx, config.Upload{
					Mode:           ModeBinary,
					Name:           "a",
					Target:         s.URL + "/{{.ProjectName}}/{{.Version}}/",
					Username:       "u2",
					ChecksumHeader: "-x-sha256",
					TrustedCerts:   cert(s),
				}
			},
			checks(check{"/blah/2.1.0/a.ubi", "u2", "x", content, map[string]string{"-x-sha256": "5e2bf57d3f40c4b6df69daf1936cb766f832374b4fc0259a7cbff06e2f70f269"}}),
		},
		{
			"custom-headers", true, true, false, false,
			func(s *httptest.Server) (*context.Context, config.Upload) {
				return ctx, config.Upload{
					Mode:     ModeBinary,
					Name:     "a",
					Target:   s.URL + "/{{.ProjectName}}/{{.Version}}/",
					Username: "u2",
					CustomHeaders: map[string]string{
						"x-custom-header-name": "custom-header-value",
					},
					TrustedCerts: cert(s),
				}
			},
			checks(check{"/blah/2.1.0/a.ubi", "u2", "x", content, map[string]string{"x-custom-header-name": "custom-header-value"}}),
		},
		{
			"custom-headers-with-template", true, true, false, false,
			func(s *httptest.Server) (*context.Context, config.Upload) {
				return ctx, config.Upload{
					Mode:     ModeBinary,
					Name:     "a",
					Target:   s.URL + "/{{.ProjectName}}/{{.Version}}/",
					Username: "u2",
					CustomHeaders: map[string]string{
						"x-project-name": "{{ .ProjectName }}",
					},
					TrustedCerts: cert(s),
				}
			},
			checks(check{"/blah/2.1.0/a.ubi", "u2", "x", content, map[string]string{"x-project-name": "blah"}}),
		},
		{
			"invalid-template-in-custom-headers", true, true, true, true,
			func(s *httptest.Server) (*context.Context, config.Upload) {
				return ctx, config.Upload{
					Mode:     ModeBinary,
					Name:     "a",
					Target:   s.URL + "/{{.ProjectName}}/{{.Version}}/",
					Username: "u2",
					CustomHeaders: map[string]string{
						"x-custom-header-name": "{{ .Env.NONEXISTINGVARIABLE and some bad expressions }}",
					},
					TrustedCerts: cert(s),
				}
			},
			checks(),
		},
		{
			"extra files", true, true, false, false,
			func(s *httptest.Server) (*context.Context, config.Upload) {
				return ctx, config.Upload{
					Mode:           ModeArchive,
					Name:           "a",
					Target:         s.URL + "/{{.ProjectName}}/{{.Version}}/",
					Username:       "u3",
					TrustedCerts:   cert(s),
					ExtraFilesOnly: true,
					ExtraFiles: []config.ExtraFile{
						{
							Glob: "testdata/*.txt",
						},
					},
				}
			},
			checks(
				check{"/blah/2.1.0/foo.txt", "u3", "x", content, map[string]string{}},
			),
		},
		{
			"filtering-by-ext", true, true, false, false,
			func(s *httptest.Server) (*context.Context, config.Upload) {
				return ctx, config.Upload{
					Mode:         ModeArchive,
					Name:         "a",
					Target:       s.URL + "/{{.ProjectName}}/{{.Version}}/",
					Username:     "u3",
					TrustedCerts: cert(s),
					Exts:         []string{"deb", "rpm", "tar.gz"},
				}
			},
			checks(
				check{"/blah/2.1.0/a.deb", "u3", "x", content, map[string]string{}},
				check{"/blah/2.1.0/a.tar.gz", "u3", "x", content, map[string]string{}},
			),
		},
		{
			name: "given a server with ClientAuth = RequireAnyClientCert, " +
				"and an Upload with ClientX509Cert and ClientX509Key set, " +
				"then the response should pass",
			tryTLS: true,
			setup: func(s *httptest.Server) (*context.Context, config.Upload) {
				s.TLS.ClientAuth = tls.RequireAnyClientCert
				return ctx, config.Upload{
					Mode:           ModeArchive,
					Name:           "a",
					Target:         s.URL + "/{{.ProjectName}}/{{.Version}}/",
					Username:       "u3",
					TrustedCerts:   cert(s),
					ClientX509Cert: "testcert.pem",
					ClientX509Key:  "testkey.pem",
					Exts:           []string{"deb", "rpm"},
				}
			},
			check: checks(
				check{"/blah/2.1.0/a.deb", "u3", "x", content, map[string]string{}},
			),
		},
		{
			name: "given a server with ClientAuth = RequireAnyClientCert, " +
				"and an Upload without either ClientX509Cert or ClientX509Key set, " +
				"then the response should fail",
			tryTLS: true,
			setup: func(s *httptest.Server) (*context.Context, config.Upload) {
				s.TLS.ClientAuth = tls.RequireAnyClientCert
				return ctx, config.Upload{
					Mode:         ModeArchive,
					Name:         "a",
					Target:       s.URL + "/{{.ProjectName}}/{{.Version}}/",
					Username:     "u3",
					TrustedCerts: cert(s),
					Exts:         []string{"deb", "rpm"},
				}
			},
			wantErrTLS: true,
			check:      checks(),
		},
	}

	uploadAndCheck := func(t *testing.T, setup func(*httptest.Server) (*context.Context, config.Upload), wantErrPlain, wantErrTLS bool, check func(r []*http.Request) error, srv *httptest.Server) {
		t.Helper()
		requests = nil
		ctx, upload := setup(srv)
		wantErr := wantErrPlain
		if srv.Certificate() != nil {
			wantErr = wantErrTLS
		}
		if err := Upload(ctx, []config.Upload{upload}, "test", is2xx); (err != nil) != wantErr {
			t.Errorf("Upload() error = %v, wantErr %v", err, wantErr)
		}
		if err := check(requests); err != nil {
			t.Errorf("Upload() request invalid. Error: %v", err)
		}
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.tryPlain {
				t.Run(tt.name, func(t *testing.T) {
					srv := httptest.NewServer(mux)
					defer srv.Close()
					uploadAndCheck(t, tt.setup, tt.wantErrPlain, tt.wantErrTLS, tt.check, srv)
				})
			}
			if tt.tryTLS {
				t.Run(tt.name+"-tls", func(t *testing.T) {
					srv := httptest.NewUnstartedServer(mux)
					srv.StartTLS()
					defer srv.Close()
					uploadAndCheck(t, tt.setup, tt.wantErrPlain, tt.wantErrTLS, tt.check, srv)
				})
			}
		})
	}
}

func cert(srv *httptest.Server) string {
	if srv == nil || srv.Certificate() == nil {
		return ""
	}
	block := &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: srv.Certificate().Raw,
	}
	return string(pem.EncodeToMemory(block))
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	d, ok := parseRetryAfter("2", now)
	require.True(t, ok)
	require.Equal(t, 2*time.Second, d)

	d, ok = parseRetryAfter(now.Add(3*time.Second).Format(http.TimeFormat), now)
	require.True(t, ok)
	require.Equal(t, 3*time.Second, d)

	d, ok = parseRetryAfter(now.Add(-time.Second).Format(http.TimeFormat), now)
	require.True(t, ok)
	require.Equal(t, time.Duration(0), d)

	_, ok = parseRetryAfter("nope", now)
	require.False(t, ok)
	_, ok = parseRetryAfter("", now)
	require.False(t, ok)
}

func TestUploadRetryAndAttempts(t *testing.T) {
	payload := []byte("artifact-bytes")
	var calls atomic.Int32
	var got [][]byte
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		mu.Lock()
		got = append(got, body)
		mu.Unlock()
		n := calls.Add(1)
		switch {
		case strings.Contains(r.URL.Path, "limited"):
			w.WriteHeader(http.StatusBadRequest)
		case strings.Contains(r.URL.Path, "later") && n == 1:
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
		case n < 3:
			w.WriteHeader(http.StatusBadGateway)
		default:
			w.WriteHeader(http.StatusCreated)
		}
	}))
	t.Cleanup(srv.Close)

	assetOpen = func(string, *artifact.Artifact) (*asset, error) {
		return &asset{ReadCloser: io.NopCloser(bytes.NewReader(payload)), Size: int64(len(payload))}, nil
	}
	t.Cleanup(assetOpenReset)

	ctx := testctx.WrapWithCfg(t.Context(), config.Project{ProjectName: "p"})
	dir := t.TempDir()
	t.Chdir(dir)
	extraPath := "notes.txt"
	require.NoError(t, os.WriteFile(extraPath, payload, 0o644))
	ctx.Artifacts.Add(&artifact.Artifact{
		Name: "b.bin",
		Path: extraPath,
		Type: artifact.UploadableBinary,
	})
	ctx.Artifacts.Add(&artifact.Artifact{
		Name: "a.bin",
		Path: extraPath,
		Type: artifact.UploadableBinary,
	})

	check := func(r *http.Response) error {
		if r.StatusCode/100 == 2 {
			return nil
		}
		return fmt.Errorf("status %d", r.StatusCode)
	}
	upload := config.Upload{
		Name:   "prod",
		Mode:   ModeBinary,
		Target: srv.URL + "/dest/",
		Retry:  config.Retry{Attempts: 3, Delay: time.Millisecond, MaxDelay: 5 * time.Millisecond},
		ExtraFiles: []config.ExtraFile{{
			Glob: extraPath,
		}},
	}
	require.NoError(t, Upload(ctx, []config.Upload{upload}, "upload", check))

	mu.Lock()
	bodies := append([][]byte(nil), got...)
	mu.Unlock()
	require.NotEmpty(t, bodies)
	for _, body := range bodies {
		require.Equal(t, payload, body)
	}

	attempts := ctx.Extra[context.PublishAttemptsKey].([]context.PublishAttempt)
	require.GreaterOrEqual(t, len(attempts), 3)
	for i := 1; i < len(attempts); i++ {
		prev, cur := attempts[i-1], attempts[i]
		require.False(t, prev.Publisher > cur.Publisher)
		if prev.Publisher == cur.Publisher && prev.Instance == cur.Instance && prev.Target == cur.Target {
			require.LessOrEqual(t, prev.Attempt, cur.Attempt)
		}
	}
	var sawFail, sawOK bool
	for _, a := range attempts {
		require.Equal(t, "upload", a.Publisher)
		require.Equal(t, "prod", a.Instance)
		require.NotEmpty(t, a.Target)
		if a.Status == "failure" {
			require.NotEmpty(t, a.Error)
			sawFail = true
		} else {
			require.Empty(t, a.Error)
			require.Equal(t, "success", a.Status)
			sawOK = true
		}
	}
	require.True(t, sawFail)
	require.True(t, sawOK)

	// Non-retriable status is a single failure.
	ctx2 := testctx.WrapWithCfg(t.Context(), config.Project{})
	ctx2.Artifacts.Add(&artifact.Artifact{Name: "a.bin", Path: extraPath, Type: artifact.UploadableBinary})
	limited := upload
	limited.Target = srv.URL + "/limited/"
	limited.ExtraFiles = nil
	err := Upload(ctx2, []config.Upload{limited}, "upload", check)
	require.Error(t, err)
	limitedAttempts := ctx2.Extra[context.PublishAttemptsKey].([]context.PublishAttempt)
	require.Len(t, limitedAttempts, 1)
	require.Equal(t, "failure", limitedAttempts[0].Status)

	// Retry-After is honored, then capped by max_delay.
	var hits atomic.Int32
	var first, second time.Time
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, payload, body)
		n := hits.Add(1)
		if n == 1 {
			first = time.Now()
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		second = time.Now()
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(slow.Close)
	ctx3 := testctx.WrapWithCfg(t.Context(), config.Project{})
	ctx3.Artifacts.Add(&artifact.Artifact{Name: "a.bin", Path: extraPath, Type: artifact.UploadableBinary})
	capped := upload
	capped.ExtraFiles = nil
	capped.Target = slow.URL + "/"
	capped.Retry = config.Retry{Attempts: 2, Delay: 50 * time.Millisecond, MaxDelay: 80 * time.Millisecond}
	require.NoError(t, Upload(ctx3, []config.Upload{capped}, "artifactory", check))
	require.Equal(t, int32(2), hits.Load())
	gap := second.Sub(first)
	require.GreaterOrEqual(t, gap, 60*time.Millisecond)
	require.Less(t, gap, 500*time.Millisecond)
	artAttempts := ctx3.Extra[context.PublishAttemptsKey].([]context.PublishAttempt)
	require.Equal(t, "artifactory", artAttempts[0].Publisher)
	require.Equal(t, 1, artAttempts[0].Attempt)
	require.Equal(t, "failure", artAttempts[0].Status)
	require.Equal(t, "success", artAttempts[1].Status)

	cancelCtx, cancel := stdctx.WithCancel(t.Context())
	ctx4 := testctx.WrapWithCfg(cancelCtx, config.Project{})
	ctx4.Artifacts.Add(&artifact.Artifact{Name: "a.bin", Path: extraPath, Type: artifact.UploadableBinary})
	blocker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusGatewayTimeout)
		cancel()
	}))
	t.Cleanup(blocker.Close)
	canceled := upload
	canceled.ExtraFiles = nil
	canceled.Target = blocker.URL + "/"
	canceled.Retry = config.Retry{Attempts: 5, Delay: time.Second, MaxDelay: time.Second}
	err = Upload(ctx4, []config.Upload{canceled}, "upload", check)
	require.ErrorIs(t, err, stdctx.Canceled)
	cancelAttempts := ctx4.Extra[context.PublishAttemptsKey].([]context.PublishAttempt)
	require.Len(t, cancelAttempts, 1)
	require.Equal(t, "failure", cancelAttempts[0].Status)
}

func TestManyUploads(t *testing.T) {
	var uploaded atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		uploaded.Store(true)
	}))
	t.Cleanup(srv.Close)
	assetOpen = func(string, *artifact.Artifact) (*asset, error) {
		return &asset{
			ReadCloser: io.NopCloser(strings.NewReader("a")),
			Size:       1,
		}, nil
	}
	defer assetOpenReset()
	ctx := testctx.WrapWithCfg(t.Context(), config.Project{
		ProjectName: "blah",
		Env:         []string{"FOO=1"},
		Uploads: []config.Upload{
			{
				Name: "skip1",
				Skip: "true",
			},
			{
				Name:     "real",
				Mode:     "archive",
				Checksum: true,
				Target:   srv.URL,
			},
			{
				Name: "skip1",
				Skip: `{{ eq .Env.FOO "1" }}`,
			},
		},
	}, testctx.WithVersion("2.1.0"))

	ctx.Artifacts.Add(&artifact.Artifact{
		Name: "checksums.txt",
		Path: "doesnt-matter",
		Type: artifact.Checksum,
	})
	err := Upload(ctx, ctx.Config.Uploads, "test", func(*http.Response) error { return nil })
	require.Error(t, err)
	require.True(t, pipe.IsSkip(err), err)
	require.True(t, uploaded.Load(), "should have uploaded")
}
