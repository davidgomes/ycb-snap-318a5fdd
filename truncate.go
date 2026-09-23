package termenv

import "github.com/muesli/termenv/ansi"

// TruncateOptions configures ANSI-aware truncation.
type TruncateOptions = ansi.TruncateOptions

// TruncateANSI truncates s to at most width terminal cells without splitting
// escape sequences. See ansi.TruncateANSI.
func TruncateANSI(s string, width int, opts TruncateOptions) string {
	return ansi.TruncateANSI(s, width, opts)
}

// StripANSI removes all escape sequences from s.
func StripANSI(s string) string {
	return ansi.StripANSI(s)
}

// ANSIWidth returns the number of terminal cells needed to display s,
// ignoring escape sequences.
func ANSIWidth(s string) int {
	return ansi.ANSIWidth(s)
}

// HasANSI reports whether s contains any escape sequences.
func HasANSI(s string) bool {
	return ansi.HasANSI(s)
}

// Truncate truncates s to at most width terminal cells. Preserve-resets is
// enabled if either the Output default or opts.PreserveResets is set. Under
// the Ascii profile, escape sequences are stripped and the plain text is
// truncated with the tail.
func (o Output) Truncate(s string, width int, opts TruncateOptions) string {
	if o.Profile == Ascii {
		return ansi.TruncateANSI(ansi.StripANSI(s), width, TruncateOptions{Tail: ansi.StripANSI(opts.Tail)})
	}
	opts.PreserveResets = o.preserveResets || opts.PreserveResets
	return ansi.TruncateANSI(s, width, opts)
}

// Truncate renders the Style truncated to at most width terminal cells. Under
// the Ascii profile, the plain text is truncated without a tail.
func (t Style) Truncate(width int, opts TruncateOptions) string {
	if t.profile == Ascii {
		return ansi.TruncateANSI(ansi.StripANSI(t.string), width, TruncateOptions{})
	}
	opts.PreserveResets = t.preserveResets || opts.PreserveResets
	return ansi.TruncateANSI(t.Styled(t.string), width, opts)
}
