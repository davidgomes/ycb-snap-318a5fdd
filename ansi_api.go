package termenv

import "github.com/muesli/termenv/ansi"

// TruncateOptions controls ANSI-aware truncation.
type TruncateOptions = ansi.TruncateOptions

// TruncateANSI truncates s to width visible cells without splitting escape
// sequences. See ansi.TruncateANSI.
func TruncateANSI(s string, width int, opts TruncateOptions) string {
	return ansi.TruncateANSI(s, width, opts)
}

// StripANSI returns s without ANSI escape sequences.
func StripANSI(s string) string {
	return ansi.StripANSI(s)
}

// ANSIWidth reports the visible cell width of s.
func ANSIWidth(s string) int {
	return ansi.ANSIWidth(s)
}

// HasANSI reports whether s contains an ANSI escape sequence.
func HasANSI(s string) bool {
	return ansi.HasANSI(s)
}

// preserveANSI rewrites reset runs without changing visible width.
func preserveANSI(s string) string {
	w := ANSIWidth(s)
	if w < 1 {
		w = 1
	}
	return TruncateANSI(s, w, TruncateOptions{PreserveResets: true})
}

// truncatePlain cuts visible text. tail is included only when the string is
// actually shortened. Escape sequences are removed and none are emitted.
func truncatePlain(s string, width int, tail string) string {
	return TruncateANSI(StripANSI(s), width, TruncateOptions{Tail: StripANSI(tail)})
}
