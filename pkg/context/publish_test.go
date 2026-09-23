package context

import (
	"encoding/json"
	"testing"

	"github.com/goreleaser/goreleaser/v2/internal/artifact"
	"github.com/goreleaser/goreleaser/v2/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestPublishAttemptsAreSorted(t *testing.T) {
	t.Parallel()
	ctx := Wrap(t.Context(), config.Project{})
	art := &artifact.Artifact{Name: "a"}
	ctx.RecordPublishAttempt(PublishAttempt{
		Publisher: "upload", Instance: "zeta", Target: "https://example/b", Attempt: 2, Status: PublishStatusFailure, Error: "nope",
	}, art)
	ctx.RecordPublishAttempt(PublishAttempt{
		Publisher: "blob", Instance: "s3://bucket", Target: "obj", Attempt: 1, Status: PublishStatusSuccess,
	}, nil)
	ctx.RecordPublishAttempt(PublishAttempt{
		Publisher: "upload", Instance: "zeta", Target: "https://example/b", Attempt: 1, Status: PublishStatusFailure, Error: "earlier",
	}, art)
	ctx.RecordPublishAttempt(PublishAttempt{
		Publisher: "artifactory", Instance: "prod", Target: "https://example/a", Attempt: 1, Status: PublishStatusSuccess,
	}, nil)

	want := []PublishAttempt{
		{Publisher: "artifactory", Instance: "prod", Target: "https://example/a", Attempt: 1, Status: PublishStatusSuccess},
		{Publisher: "blob", Instance: "s3://bucket", Target: "obj", Attempt: 1, Status: PublishStatusSuccess},
		{Publisher: "upload", Instance: "zeta", Target: "https://example/b", Attempt: 1, Status: PublishStatusFailure, Error: "earlier"},
		{Publisher: "upload", Instance: "zeta", Target: "https://example/b", Attempt: 2, Status: PublishStatusFailure, Error: "nope"},
	}
	require.Equal(t, want, ctx.Extra.PublishAttempts)

	onArtifact, ok := art.Extra[extraPublishAttempts].([]PublishAttempt)
	require.True(t, ok)
	require.Equal(t, want[2:], onArtifact)

	payload, err := json.Marshal(struct {
		Extra Extra `json:"extra"`
	}{Extra: ctx.Extra})
	require.NoError(t, err)

	var decoded struct {
		Extra struct {
			PublishAttempts []map[string]any `json:"publish_attempts"`
		} `json:"extra"`
	}
	require.NoError(t, json.Unmarshal(payload, &decoded))
	require.Len(t, decoded.Extra.PublishAttempts, 4)
	_, hasError := decoded.Extra.PublishAttempts[0]["error"]
	require.False(t, hasError)
	require.Equal(t, "earlier", decoded.Extra.PublishAttempts[2]["error"])
}
