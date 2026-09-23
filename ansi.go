package termenv

import (
	"strconv"
	"strings"

	"github.com/muesli/termenv/ansi"
)

// TruncateOptions configures TruncateANSI.
type TruncateOptions = ansi.TruncateOptions

// TruncateANSI truncates s to at most width visible cells without splitting
// escape sequences. See ansi.TruncateANSI.
func TruncateANSI(s string, width int, opts TruncateOptions) string {
	return ansi.TruncateANSI(s, width, opts)
}

// StripANSI removes all escape sequences from s.
func StripANSI(s string) string {
	return ansi.StripANSI(s)
}

// ANSIWidth returns the visible width of s, ignoring escape sequences.
func ANSIWidth(s string) int {
	return ansi.ANSIWidth(s)
}

// HasANSI reports whether s contains an escape sequence.
func HasANSI(s string) bool {
	return ansi.HasANSI(s)
}

// reopenAfterResets re-emits open after each run of SGR resets in s, so that
// nested styled strings don't cancel the enclosing style. Attributes set by a
// reset sequence after its zero parameter (e.g. ESC[0;31m) are re-applied
// after open so they still take precedence.
func reopenAfterResets(s, open string) string {
	if !ansi.HasANSI(s) {
		return s
	}

	tokens := ansi.Tokenize(s)
	var b strings.Builder
	for i, t := range tokens {
		b.WriteString(t.Raw)
		if t.Type != ansi.TokenReset {
			continue
		}
		if i+1 >= len(tokens) || tokens[i+1].Type == ansi.TokenReset {
			continue
		}
		b.WriteString(open)
		if p := resetTrailingParams(t.Raw); p != "" {
			b.WriteString(CSI + p + "m")
		}
	}
	return b.String()
}

func resetTrailingParams(raw string) string {
	params := strings.TrimSuffix(strings.TrimPrefix(raw, CSI), "m")
	parts := strings.Split(params, ";")
	last := -1
	for i, p := range parts {
		if n, err := strconv.Atoi(p); err == nil && n == 0 {
			last = i
		}
	}
	return strings.Join(parts[last+1:], ";")
}
