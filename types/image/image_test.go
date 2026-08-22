package image

import (
	"reflect"
	"testing"

	dockerimage "github.com/moby/moby/api/types/image"
)

func TestNewDetailSummary_NilSource(t *testing.T) {
	got := NewDetailSummary(nil)
	want := DetailSummary{}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected zero-value DetailSummary for nil source, got %#v", got)
	}
}

func TestNewDetailSummary_HandlesNilGraphDriver(t *testing.T) {
	src := &dockerimage.InspectResponse{
		ID:           "sha256:test-id",
		RepoTags:     []string{"alpine:3.20"},
		RepoDigests:  []string{"docker.io/library/alpine@sha256:deadbeef"},
		Comment:      "test-comment",
		Created:      "2026-03-05T03:58:10.000000000Z",
		Author:       "arcane-test",
		Architecture: "amd64",
		Os:           "linux",
		Size:         42,
		RootFS: dockerimage.RootFS{
			Type:   "layers",
			Layers: []string{"sha256:l1", "sha256:l2"},
		},
		GraphDriver: nil,
	}

	got := NewDetailSummary(src)

	if got.ID != src.ID {
		t.Fatalf("expected ID %q, got %q", src.ID, got.ID)
	}
	if got.GraphDriver.Name != "" {
		t.Fatalf("expected empty graph driver name when source GraphDriver is nil, got %q", got.GraphDriver.Name)
	}
	if got.Descriptor.Digest != "sha256:deadbeef" {
		t.Fatalf("expected descriptor digest to be parsed from repo digest, got %q", got.Descriptor.Digest)
	}
	if got.RootFs.Type != "layers" {
		t.Fatalf("expected RootFs.Type to be %q, got %q", "layers", got.RootFs.Type)
	}
	if len(got.RootFs.Layers) != 2 {
		t.Fatalf("expected 2 rootfs layers, got %d", len(got.RootFs.Layers))
	}
}
