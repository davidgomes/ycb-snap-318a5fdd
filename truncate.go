package termenv

import (
	"strings"

	"github.com/muesli/termenv/ansi"
)

// TruncateOptions controls ANSI-aware truncation.
type TruncateOptions = ansi.TruncateOptions

// TruncateANSI truncates s to width visible cells without splitting escape sequences.
func TruncateANSI(s string, width int, opts TruncateOptions) string {
	return ansi.TruncateANSI(s, width, opts)
}

// StripANSI returns s with ANSI escape sequences removed.
func StripANSI(s string) string {
	return ansi.StripANSI(s)
}

// ANSIWidth returns the visible monospace width of s.
func ANSIWidth(s string) int {
	return ansi.ANSIWidth(s)
}

// HasANSI reports whether s contains ANSI escape sequences.
func HasANSI(s string) bool {
	return ansi.HasANSI(s)
}

// Truncate truncates the styled text to width visible cells.
// In the Ascii profile the result is plain text and the tail is omitted.
func (t Style) Truncate(width int, opts TruncateOptions) string {
	if t.profile == Ascii {
		return TruncateANSI(StripANSI(t.string), width, TruncateOptions{})
	}

	seq := strings.Join(t.styles, ";")
	opened := t.string
	if seq != "" {
		opened = openStyle(seq, t.string)
	}
	if t.preserveResets {
		opts.PreserveResets = true
	}
	return TruncateANSI(opened, width, opts)
}

// Truncate truncates s to width visible cells.
// Preserve-resets is enabled when the output default is set or opts requests it.
// In the Ascii profile the result is plain text and includes the tail.
func (o Output) Truncate(s string, width int, opts TruncateOptions) string {
	if o.Profile == Ascii {
		return TruncateANSI(StripANSI(s), width, TruncateOptions{Tail: StripANSI(opts.Tail)})
	}
	if o.preserveResets {
		opts.PreserveResets = true
	}
	return TruncateANSI(s, width, opts)
}

func openStyle(seq, text string) string {
	return CSI + seq + "m" + text + CSI + ResetSeq + "m"
}
