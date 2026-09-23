package artifact

import (
	"cmp"
	"slices"
	"sync"
)

// Publish attempt statuses.
const (
	PublishAttemptSuccess = "success"
	PublishAttemptFailure = "failure"
)

// PublishAttempt records a single attempt of publishing an artifact.
type PublishAttempt struct {
	Publisher string `json:"publisher"`
	Instance  string `json:"instance"`
	Target    string `json:"target"`
	Attempt   uint   `json:"attempt"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}

// Artifacts are published concurrently by multiple goroutines, so appends to
// the publish attempts extra must be serialized.
//
//nolint:gochecknoglobals
var publishAttemptsLock sync.Mutex

// RecordPublishAttempt appends an attempt to the artifact's
// [ExtraPublishAttempts] extra, keeping the list sorted by publisher,
// instance, target and attempt.
// A nil err records a success, otherwise a failure with the error message.
func (a *Artifact) RecordPublishAttempt(publisher, instance, target string, attemptNumber uint, err error) {
	if a == nil {
		return
	}
	attempt := PublishAttempt{
		Publisher: publisher,
		Instance:  instance,
		Target:    target,
		Attempt:   attemptNumber,
		Status:    PublishAttemptSuccess,
	}
	if err != nil {
		attempt.Status = PublishAttemptFailure
		attempt.Error = err.Error()
	}

	publishAttemptsLock.Lock()
	defer publishAttemptsLock.Unlock()

	if a.Extra == nil {
		a.Extra = Extras{}
	}
	var attempts []PublishAttempt
	if existing, ok := a.Extra[ExtraPublishAttempts]; ok {
		if cast, err := tryCastExtra[[]PublishAttempt](existing); err == nil {
			attempts = slices.Clone(cast)
		}
	}
	attempts = append(attempts, attempt)
	slices.SortStableFunc(attempts, comparePublishAttempts)
	a.Extra[ExtraPublishAttempts] = attempts
}

func comparePublishAttempts(x, y PublishAttempt) int {
	return cmp.Or(
		cmp.Compare(x.Publisher, y.Publisher),
		cmp.Compare(x.Instance, y.Instance),
		cmp.Compare(x.Target, y.Target),
		cmp.Compare(x.Attempt, y.Attempt),
	)
}
