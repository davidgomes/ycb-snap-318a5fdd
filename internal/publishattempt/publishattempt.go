// Package publishattempt records publish tries on an artifact.
package publishattempt

import (
	"cmp"
	"slices"
	"strings"
	"sync"

	"github.com/goreleaser/goreleaser/v2/internal/artifact"
)

const (
	// StatusSuccess is a publish try that completed.
	StatusSuccess = "success"
	// StatusFailure is a publish try that returned an error.
	StatusFailure = "failure"
)

// mu guards read-modify-write of extra.publish_attempts.
// Several artifacts can be published at once, and one artifact can be sent
// to more than one publisher at once.
var mu sync.Mutex

// Success appends a successful try and keeps the slice sorted.
func Success(art *artifact.Artifact, publisher, instance, target string, attempt int) {
	record(art, artifact.PublishAttempt{
		Publisher: publisher,
		Instance:  instance,
		Target:    target,
		Attempt:   attempt,
		Status:    StatusSuccess,
	})
}

// Failure appends a failed try and keeps the slice sorted.
// A nil or empty error is stored as "unknown error" so the field is present.
func Failure(art *artifact.Artifact, publisher, instance, target string, attempt int, err error) {
	msg := "unknown error"
	if err != nil && err.Error() != "" {
		msg = err.Error()
	}
	record(art, artifact.PublishAttempt{
		Publisher: publisher,
		Instance:  instance,
		Target:    target,
		Attempt:   attempt,
		Status:    StatusFailure,
		Error:     msg,
	})
}

// List returns a copy of the recorded tries, already sorted.
func List(art *artifact.Artifact) []artifact.PublishAttempt {
	if art == nil || art.Extra == nil {
		return nil
	}
	mu.Lock()
	defer mu.Unlock()
	cur, _ := art.Extra[artifact.ExtraPublishAttempts].([]artifact.PublishAttempt)
	out := make([]artifact.PublishAttempt, len(cur))
	copy(out, cur)
	return out
}

func record(art *artifact.Artifact, attempt artifact.PublishAttempt) {
	if art == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	if art.Extra == nil {
		art.Extra = artifact.Extras{}
	}
	cur, _ := art.Extra[artifact.ExtraPublishAttempts].([]artifact.PublishAttempt)
	next := make([]artifact.PublishAttempt, len(cur)+1)
	copy(next, cur)
	next[len(cur)] = attempt
	slices.SortStableFunc(next, func(a, b artifact.PublishAttempt) int {
		if c := strings.Compare(a.Publisher, b.Publisher); c != 0 {
			return c
		}
		if c := strings.Compare(a.Instance, b.Instance); c != 0 {
			return c
		}
		if c := strings.Compare(a.Target, b.Target); c != 0 {
			return c
		}
		return cmp.Compare(a.Attempt, b.Attempt)
	})
	art.Extra[artifact.ExtraPublishAttempts] = next
}
