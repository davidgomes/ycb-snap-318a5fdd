package termenv

import "github.com/muesli/termenv/ansi"

// TruncateOptions controls ANSI-aware truncation.
type TruncateOptions = ansi.TruncateOptions

// TruncateANSI truncates s to width visible columns without splitting control
// sequences.
func TruncateANSI(s string, width int, opts TruncateOptions) string {
	return ansi.TruncateANSI(s, width, opts)
}

// StripANSI removes ANSI control sequences from s.
func StripANSI(s string) string {
	return ansi.StripANSI(s)
}

// ANSIWidth returns the visible column width of s.
func ANSIWidth(s string) int {
	return ansi.ANSIWidth(s)
}

// HasANSI reports whether s contains ANSI control sequences.
func HasANSI(s string) bool {
	return ansi.HasANSI(s)
}
