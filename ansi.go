package termenv

import "github.com/muesli/termenv/ansi"

// TruncateOptions controls ANSI-aware truncation.
type TruncateOptions = ansi.TruncateOptions

// TruncateANSI shortens s to width terminal columns without splitting escape sequences.
func TruncateANSI(s string, width int, opts TruncateOptions) string {
	return ansi.TruncateANSI(s, width, opts)
}

// StripANSI returns s without ANSI escape sequences.
func StripANSI(s string) string {
	return ansi.StripANSI(s)
}

// ANSIWidth returns the number of terminal columns s occupies.
func ANSIWidth(s string) int {
	return ansi.ANSIWidth(s)
}

// HasANSI reports whether s contains an ANSI escape sequence.
func HasANSI(s string) bool {
	return ansi.HasANSI(s)
}
