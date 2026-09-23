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

// PublishAttempt is a single attempt at publishing an artifact, as stored in
// its [ExtraPublishAttempts] extra field.
type PublishAttempt struct {
	Publisher string `json:"publisher"`
	Instance  string `json:"instance"`
	Target    string `json:"target"`
	Attempt   int    `json:"attempt"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}

func comparePublishAttempts(a, b PublishAttempt) int {
	return cmp.Or(
		cmp.Compare(a.Publisher, b.Publisher),
		cmp.Compare(a.Instance, b.Instance),
		cmp.Compare(a.Target, b.Target),
		cmp.Compare(a.Attempt, b.Attempt),
		cmp.Compare(a.Status, b.Status),
		cmp.Compare(a.Error, b.Error),
	)
}

// PublishAttempts collects publish attempts, possibly from many goroutines,
// and writes them to the artifacts once [PublishAttempts.Apply] is called.
//
// Artifact extras are not safe for concurrent use, and other goroutines might
// be reading them while publishing, so they are only written on Apply, which
// should be called once all the publishing goroutines are done.
//
// The zero value is ready to use.
type PublishAttempts struct {
	mu       sync.Mutex
	attempts map[*Artifact][]PublishAttempt
}

// Record records the outcome of an attempt at publishing the given artifact.
// A nil err means the attempt succeeded.
// Attempts for a nil artifact are ignored.
func (p *PublishAttempts) Record(a *Artifact, publisher, instance, target string, attempt int, err error) {
	if a == nil {
		return
	}
	pa := PublishAttempt{
		Publisher: publisher,
		Instance:  instance,
		Target:    target,
		Attempt:   attempt,
		Status:    PublishAttemptSuccess,
	}
	if err != nil {
		pa.Status = PublishAttemptFailure
		pa.Error = cmp.Or(err.Error(), "unknown error")
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.attempts == nil {
		p.attempts = map[*Artifact][]PublishAttempt{}
	}
	p.attempts[a] = append(p.attempts[a], pa)
}

// Apply adds the recorded attempts to their artifacts' extras, merging them
// with previously recorded ones, sorted by publisher, instance, target, and
// attempt.
func (p *PublishAttempts) Apply() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for a, attempts := range p.attempts {
		all := slices.Concat(ExtraOr(*a, ExtraPublishAttempts, []PublishAttempt(nil)), attempts)
		slices.SortStableFunc(all, comparePublishAttempts)
		if a.Extra == nil {
			a.Extra = Extras{}
		}
		a.Extra[ExtraPublishAttempts] = all
	}
	p.attempts = nil
}
