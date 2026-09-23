package artifact

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPublishAttempts(t *testing.T) {
	a := &Artifact{Name: "a"}
	b := &Artifact{Name: "b", Extra: Extras{ExtraID: "foo"}}
	untouched := &Artifact{Name: "c"}

	var attempts PublishAttempts
	attempts.Record(a, "upload", "prod", "https://z/a", 2, nil)
	attempts.Record(a, "upload", "prod", "https://z/a", 1, errors.New("503"))
	attempts.Record(a, "artifactory", "prod", "https://y/a", 1, nil)
	attempts.Record(a, "upload", "dev", "https://z/a", 1, nil)
	attempts.Record(b, "blob", "s3://bucket", "dir/b", 1, nil)
	attempts.Record(nil, "blob", "s3://bucket", "dir/extra.txt", 1, nil)

	require.NotContains(t, a.Extra, ExtraPublishAttempts, "attempts must only be written on Apply")
	attempts.Apply()

	require.Equal(t, []PublishAttempt{
		{Publisher: "artifactory", Instance: "prod", Target: "https://y/a", Attempt: 1, Status: PublishAttemptSuccess},
		{Publisher: "upload", Instance: "dev", Target: "https://z/a", Attempt: 1, Status: PublishAttemptSuccess},
		{Publisher: "upload", Instance: "prod", Target: "https://z/a", Attempt: 1, Status: PublishAttemptFailure, Error: "503"},
		{Publisher: "upload", Instance: "prod", Target: "https://z/a", Attempt: 2, Status: PublishAttemptSuccess},
	}, MustExtra[[]PublishAttempt](*a, ExtraPublishAttempts))
	require.Equal(t, []PublishAttempt{
		{Publisher: "blob", Instance: "s3://bucket", Target: "dir/b", Attempt: 1, Status: PublishAttemptSuccess},
	}, MustExtra[[]PublishAttempt](*b, ExtraPublishAttempts))
	require.Equal(t, "foo", b.ID())
	require.Nil(t, untouched.Extra)

	t.Run("apply again is a no-op", func(t *testing.T) {
		attempts.Apply()
		require.Len(t, MustExtra[[]PublishAttempt](*a, ExtraPublishAttempts), 4)
	})

	t.Run("merges with previous attempts", func(t *testing.T) {
		var more PublishAttempts
		more.Record(a, "blob", "gs://bucket", "dir/a", 1, nil)
		more.Record(a, "upload", "prod", "https://z/a", 3, errors.New("late"))
		more.Apply()
		got := MustExtra[[]PublishAttempt](*a, ExtraPublishAttempts)
		require.Len(t, got, 6)
		require.Equal(t, "blob", got[1].Publisher)
		require.Equal(t, 3, got[5].Attempt)
	})
}

func TestPublishAttemptsMergesDecodedAttempts(t *testing.T) {
	a := &Artifact{
		Name: "a",
		Extra: Extras{
			ExtraPublishAttempts: []any{
				map[string]any{
					"publisher": "upload",
					"instance":  "prod",
					"target":    "https://z/a",
					"attempt":   1,
					"status":    "success",
				},
			},
		},
	}
	var attempts PublishAttempts
	attempts.Record(a, "blob", "s3://bucket", "dir/a", 1, nil)
	attempts.Apply()
	require.Equal(t, []PublishAttempt{
		{Publisher: "blob", Instance: "s3://bucket", Target: "dir/a", Attempt: 1, Status: PublishAttemptSuccess},
		{Publisher: "upload", Instance: "prod", Target: "https://z/a", Attempt: 1, Status: PublishAttemptSuccess},
	}, MustExtra[[]PublishAttempt](*a, ExtraPublishAttempts))
}

func TestPublishAttemptsDeterministic(t *testing.T) {
	var expected []byte
	for i := range 20 {
		a := &Artifact{Name: "a"}
		var attempts PublishAttempts
		var wg sync.WaitGroup
		for _, instance := range []string{"s3://one", "s3://two", "gs://three"} {
			for _, target := range []string{"dir/a", "other/a"} {
				wg.Go(func() {
					for attempt := 1; attempt <= 3; attempt++ {
						var err error
						if attempt < 3 {
							err = fmt.Errorf("attempt %d failed", attempt)
						}
						attempts.Record(a, "blob", instance, target, attempt, err)
					}
				})
			}
		}
		wg.Wait()
		attempts.Apply()

		bts, err := json.Marshal(a)
		require.NoError(t, err)
		if i == 0 {
			expected = bts
			continue
		}
		require.JSONEq(t, string(expected), string(bts))
		require.Equal(t, string(expected), string(bts))
	}
}

func TestPublishAttemptsEmptyError(t *testing.T) {
	a := &Artifact{Name: "a"}
	var attempts PublishAttempts
	attempts.Record(a, "blob", "s3://bucket", "dir/a", 1, errors.New(""))
	attempts.Apply()
	got := MustExtra[[]PublishAttempt](*a, ExtraPublishAttempts)
	require.Len(t, got, 1)
	require.Equal(t, PublishAttemptFailure, got[0].Status)
	require.Equal(t, "unknown error", got[0].Error)
}

func TestPublishAttemptJSON(t *testing.T) {
	a := &Artifact{Name: "a"}
	var attempts PublishAttempts
	attempts.Record(a, "upload", "prod", "https://z/a", 1, errors.New("boom"))
	attempts.Record(a, "upload", "prod", "https://z/a", 2, nil)
	attempts.Apply()

	bts, err := json.Marshal(a)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"name": "a",
		"extra": {
			"publish_attempts": [
				{"publisher":"upload","instance":"prod","target":"https://z/a","attempt":1,"status":"failure","error":"boom"},
				{"publisher":"upload","instance":"prod","target":"https://z/a","attempt":2,"status":"success"}
			]
		}
	}`, string(bts))
}
