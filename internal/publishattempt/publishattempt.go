// Package publishattempt records per-artifact publish tries.
package publishattempt

import (
	"cmp"
	"slices"
	"strings"

	"github.com/goreleaser/goreleaser/v2/internal/artifact"
	"github.com/goreleaser/goreleaser/v2/pkg/context"
)

// Record stores one attempt on the context audit log and, when art is one of
// the release artifacts, on that artifact's extra.publish_attempts list.
func Record(ctx *context.Context, art *artifact.Artifact, attempt context.PublishAttempt) {
	if ctx == nil {
		return
	}
	ctx.AddPublishAttempt(attempt)
	if art == nil {
		return
	}
	if art.Extra == nil {
		art.Extra = artifact.Extras{}
	}
	existing, _ := art.Extra[context.ExtraPublishAttempts].([]context.PublishAttempt)
	existing = append(existing, attempt)
	slices.SortStableFunc(existing, func(a, b context.PublishAttempt) int {
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
	art.Extra[context.ExtraPublishAttempts] = existing
}
