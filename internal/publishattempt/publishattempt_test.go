package publishattempt

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/goreleaser/goreleaser/v2/internal/artifact"
	"github.com/stretchr/testify/require"
)

func TestRecordSortsAndOmitsSuccessError(t *testing.T) {
	art := &artifact.Artifact{Extra: artifact.Extras{artifact.ExtraID: "keep"}}
	Failure(art, "upload", "b", "https://b/a", 2, errors.New("second"))
	Success(art, "blob", "s3://bucket", "obj", 1)
	Failure(art, "artifactory", "a", "https://a/a", 1, errors.New("first"))
	Success(art, "upload", "b", "https://b/a", 1)
	Failure(art, "upload", "a", "https://a/a", 1, errors.New("other"))

	require.Equal(t, "keep", art.Extra[artifact.ExtraID])
	require.Equal(t, []artifact.PublishAttempt{
		{Publisher: "artifactory", Instance: "a", Target: "https://a/a", Attempt: 1, Status: StatusFailure, Error: "first"},
		{Publisher: "blob", Instance: "s3://bucket", Target: "obj", Attempt: 1, Status: StatusSuccess},
		{Publisher: "upload", Instance: "a", Target: "https://a/a", Attempt: 1, Status: StatusFailure, Error: "other"},
		{Publisher: "upload", Instance: "b", Target: "https://b/a", Attempt: 1, Status: StatusSuccess},
		{Publisher: "upload", Instance: "b", Target: "https://b/a", Attempt: 2, Status: StatusFailure, Error: "second"},
	}, List(art))

	raw, err := json.Marshal(art.Extra[artifact.ExtraPublishAttempts])
	require.NoError(t, err)
	require.NotContains(t, string(raw), `"error":""`)
	require.Contains(t, string(raw), `"error":"first"`)
	var decoded []map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.NotContains(t, decoded[1], "error")
}

func TestFailureNilError(t *testing.T) {
	art := &artifact.Artifact{}
	Failure(art, "upload", "a", "https://a", 1, nil)
	require.Equal(t, "unknown error", List(art)[0].Error)
}

func TestListNil(t *testing.T) {
	require.Empty(t, List(nil))
	require.Empty(t, List(&artifact.Artifact{}))
}
