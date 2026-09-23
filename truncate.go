package termenv

import (
	"math"

	"github.com/muesli/termenv/ansi"
)

// TruncateOptions configures TruncateANSI.
type TruncateOptions = ansi.TruncateOptions

// TruncateANSI truncates s to width visible cells without splitting escape
// sequences.
func TruncateANSI(s string, width int, opts TruncateOptions) string {
	return ansi.TruncateANSI(s, width, opts)
}

// StripANSI removes all escape sequences from s.
func StripANSI(s string) string {
	return ansi.StripANSI(s)
}

// ANSIWidth returns the visible cell width of s.
func ANSIWidth(s string) int {
	return ansi.ANSIWidth(s)
}

// HasANSI reports whether s contains an escape sequence.
func HasANSI(s string) bool {
	return ansi.HasANSI(s)
}

// WithPreserveResets sets whether styles created by the Output re-open
// themselves after nested resets.
func WithPreserveResets(v bool) OutputOption {
	return func(o *Output) {
		o.preserveResets = v
	}
}

// String returns a new Style inheriting the Output's defaults.
func (o Output) String(s ...string) Style {
	st := o.Profile.String(s...)
	st.preserveResets = o.preserveResets
	return st
}

// Truncate truncates s to width visible cells.
func (o Output) Truncate(s string, width int, opts TruncateOptions) string {
	return truncateFor(o.Profile, o.preserveResets, s, width, opts)
}

func truncateFor(p Profile, preserve bool, s string, width int, opts TruncateOptions) string {
	if p == Ascii {
		return ansi.TruncateANSI(ansi.StripANSI(s), width, TruncateOptions{Tail: ansi.StripANSI(opts.Tail)})
	}
	opts.PreserveResets = preserve || opts.PreserveResets
	return ansi.TruncateANSI(s, width, opts)
}

// PreserveResets makes the style re-open itself after resets inside its text.
func (t Style) PreserveResets() Style {
	t.preserveResets = true
	return t
}

// Truncate renders the style and truncates it to width visible cells.
func (t Style) Truncate(width int, opts TruncateOptions) string {
	if t.profile == Ascii {
		return ansi.TruncateANSI(ansi.StripANSI(t.string), width, TruncateOptions{})
	}
	opts.PreserveResets = t.preserveResets || opts.PreserveResets
	t.preserveResets = false
	return ansi.TruncateANSI(t.String(), width, opts)
}

func preserveResets(s string) string {
	return ansi.TruncateANSI(s, math.MaxInt, TruncateOptions{PreserveResets: true})
}
