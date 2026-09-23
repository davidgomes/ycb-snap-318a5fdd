package context

import (
	"cmp"
	"slices"

	"github.com/goreleaser/goreleaser/v2/internal/artifact"
)

const extraPublishAttempts = "publish_attempts"

const (
	// PublishStatusSuccess is a completed publish attempt.
	PublishStatusSuccess = "success"
	// PublishStatusFailure is a publish attempt that returned an error.
	PublishStatusFailure = "failure"
)

// PublishAttempt is one try to publish a single artifact.
//
// The collection is exposed as extra.publish_attempts and is kept sorted by
// publisher, instance, target, then attempt.
type PublishAttempt struct {
	Publisher string `json:"publisher"`
	Instance  string `json:"instance"`
	Target    string `json:"target"`
	Attempt   int    `json:"attempt"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}

// Extra is supplemental data produced while publishing.
type Extra struct {
	PublishAttempts []PublishAttempt `json:"publish_attempts,omitempty"`
}

// RecordPublishAttempt appends one attempt to extra.publish_attempts.
//
// When art is non-nil, the same attempt is also stored on that artifact's
// extra.publish_attempts. Callers must not record into the same artifact from
// multiple goroutines. The global log is safe for concurrent publishers.
func (c *Context) RecordPublishAttempt(a PublishAttempt, art *artifact.Artifact) {
	c.extraMu.Lock()
	defer c.extraMu.Unlock()

	c.Extra.PublishAttempts = append(c.Extra.PublishAttempts, a)
	sortPublishAttempts(c.Extra.PublishAttempts)

	if art == nil {
		return
	}
	if art.Extra == nil {
		art.Extra = artifact.Extras{}
	}
	existing, _ := art.Extra[extraPublishAttempts].([]PublishAttempt)
	existing = append(append([]PublishAttempt{}, existing...), a)
	sortPublishAttempts(existing)
	art.Extra[extraPublishAttempts] = existing
}

func sortPublishAttempts(items []PublishAttempt) {
	slices.SortFunc(items, func(a, b PublishAttempt) int {
		if c := cmp.Compare(a.Publisher, b.Publisher); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Instance, b.Instance); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Target, b.Target); c != 0 {
			return c
		}
		return cmp.Compare(a.Attempt, b.Attempt)
	})
}
