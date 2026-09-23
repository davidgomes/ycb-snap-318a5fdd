// Package context provides gorelease context which is passed through the
// pipeline.
//
// The context extends the standard library context and add a few more
// fields and other things, so pipes can gather data provided by previous
// pipes without really knowing each other.
package context

import (
	"cmp"
	stdctx "context"
	"maps"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/goreleaser/goreleaser/v2/internal/artifact"
	"github.com/goreleaser/goreleaser/v2/pkg/config"
)

// GitInfo includes tags and diffs used in some point.
type GitInfo struct {
	Branch      string
	CurrentTag  string
	PreviousTag string
	Commit      string
	ShortCommit string
	FullCommit  string
	FirstCommit string
	CommitDate  time.Time
	URL         string
	Summary     string
	TagSubject  string
	TagContents string
	TagBody     string
	Dirty       bool
}

// Env is the environment variables.
type Env map[string]string

// Copy returns a copy of the environment.
func (e Env) Copy() Env {
	out := Env{}
	maps.Copy(out, e)
	return out
}

// Strings returns the current environment as a list of strings, suitable for
// os executions.
func (e Env) Strings() []string {
	result := make([]string, 0, len(e))
	for k, v := range e {
		result = append(result, k+"="+v)
	}
	return result
}

// TokenType is either github or gitlab.
type TokenType string

const (
	// TokenTypeGitHub defines github as type of the token.
	TokenTypeGitHub TokenType = "github"
	// TokenTypeGitLab defines gitlab as type of the token.
	TokenTypeGitLab TokenType = "gitlab"
	// TokenTypeGitea defines gitea as type of the token.
	TokenTypeGitea TokenType = "gitea"
)

type Action uint8

const (
	ActionNone Action = iota
	ActionBuild
	ActionRelease
)

// Context carries along some data through the pipes.
type Context struct {
	stdctx.Context
	Action            Action
	Config            config.Project
	Env               Env
	Token             string
	TokenType         TokenType
	Git               GitInfo
	Date              time.Time
	Artifacts         *artifact.Artifacts
	ReleaseURL        string
	ReleaseNotes      string
	ReleaseNotesFile  string
	ReleaseNotesTmpl  string
	ReleaseHeaderFile string
	ReleaseHeaderTmpl string
	ReleaseFooterFile string
	ReleaseFooterTmpl string
	Version           string
	ModulePath        string
	PartialTarget     string
	Snapshot          bool
	FailFast          bool
	Partial           bool
	SingleTarget      bool
	SkipTokenCheck    bool
	Clean             bool
	PreRelease        bool
	Deprecated        bool
	Parallelism       int
	Semver            Semver
	Runtime           Runtime
	Skips             map[string]bool

	NotifiedDeprecations map[string]struct{}

	// Extra holds publisher audit data. Publish attempts are stored at
	// Extra["publish_attempts"] and kept sorted for deterministic output.
	Extra map[string]any `json:"extra,omitempty"`

	publishAttempts *publishAttemptLog
}

// ExtraPublishAttempts is the Extra key for the publish attempt audit log.
const ExtraPublishAttempts = "publish_attempts"

// PublishAttempt is one try to publish a single artifact.
type PublishAttempt struct {
	Publisher string `json:"publisher"`
	Instance  string `json:"instance"`
	Target    string `json:"target"`
	Attempt   int    `json:"attempt"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}

const (
	// PublishStatusSuccess is a successful publish attempt.
	PublishStatusSuccess = "success"
	// PublishStatusFailure is a failed publish attempt.
	PublishStatusFailure = "failure"
)

type publishAttemptLog struct {
	mu    sync.Mutex
	items []PublishAttempt
}

type Runtime struct {
	Goos   string
	Goarch string
}

// Semver represents a semantic version.
type Semver struct {
	Major      uint64
	Minor      uint64
	Patch      uint64
	Prerelease string
}

// WrapWithTimeout new context with the given timeout.
func WrapWithTimeout(parent stdctx.Context, config config.Project, timeout time.Duration) (*Context, stdctx.CancelFunc) {
	ctx, cancel := stdctx.WithTimeout(parent, timeout) // nosem
	return Wrap(ctx, config), cancel
}

// Wrap wraps an existing context.
func Wrap(ctx stdctx.Context, config config.Project) *Context {
	return &Context{
		Context:              ctx,
		Config:               config,
		Env:                  ToEnv(append(os.Environ(), config.Env...)),
		Parallelism:          4,
		Artifacts:            artifact.New(),
		Date:                 time.Now(),
		Skips:                map[string]bool{},
		NotifiedDeprecations: map[string]struct{}{},
		Extra:                map[string]any{},
		publishAttempts:      &publishAttemptLog{},
		Runtime: Runtime{
			Goos:   runtime.GOOS,
			Goarch: runtime.GOARCH,
		},
	}
}

// AddPublishAttempt appends one attempt and re-sorts extra.publish_attempts
// by publisher, instance, target, then attempt.
func (ctx *Context) AddPublishAttempt(attempt PublishAttempt) {
	if ctx == nil {
		return
	}
	log := ctx.ensurePublishAttempts()
	log.mu.Lock()
	defer log.mu.Unlock()
	log.items = append(log.items, attempt)
	sortPublishAttempts(log.items)
	if ctx.Extra == nil {
		ctx.Extra = map[string]any{}
	}
	ctx.Extra[ExtraPublishAttempts] = slices.Clone(log.items)
}

// PublishAttempts returns a copy of the sorted publish attempt audit log.
func (ctx *Context) PublishAttempts() []PublishAttempt {
	if ctx == nil {
		return nil
	}
	log := ctx.ensurePublishAttempts()
	log.mu.Lock()
	defer log.mu.Unlock()
	return slices.Clone(log.items)
}

var publishAttemptInit sync.Mutex

func (ctx *Context) ensurePublishAttempts() *publishAttemptLog {
	publishAttemptInit.Lock()
	defer publishAttemptInit.Unlock()
	if ctx.publishAttempts == nil {
		ctx.publishAttempts = &publishAttemptLog{}
	}
	return ctx.publishAttempts
}

func sortPublishAttempts(items []PublishAttempt) {
	slices.SortStableFunc(items, func(a, b PublishAttempt) int {
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
}

// ToEnv converts a list of strings to an Env (aka a map[string]string).
func ToEnv(env []string) Env {
	r := Env{}
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if !ok || k == "" {
			continue
		}
		if k == "GORELEASER_EXPERIMENTAL" {
			os.Setenv(k, v)
		}
		r[k] = v
	}
	return r
}
