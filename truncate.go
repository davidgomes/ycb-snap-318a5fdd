package termenv

import "github.com/muesli/termenv/ansi"

// TruncateOptions configures truncation. See ansi.TruncateOptions.
type TruncateOptions = ansi.TruncateOptions

// TruncateANSI shortens s to at most width terminal cells without splitting
// escape sequences. See ansi.TruncateANSI.
func TruncateANSI(s string, width int, opts TruncateOptions) string {
	return ansi.TruncateANSI(s, width, opts)
}

// StripANSI returns s with all escape sequences removed.
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

// Truncate shortens s to at most width terminal cells without splitting
// escape sequences. Preserve-resets is enabled if either the Output default
// or opts.PreserveResets is set.
//
// Under the Ascii profile no escape sequences are emitted: s and the tail are
// stripped before truncating.
func (o Output) Truncate(s string, width int, opts TruncateOptions) string {
	return truncate(o.Profile, o.preserveResets, s, width, opts)
}

func truncate(p Profile, preserveResets bool, s string, width int, opts TruncateOptions) string {
	if p == Ascii {
		return ansi.TruncateANSI(ansi.StripANSI(s), width, TruncateOptions{Tail: ansi.StripANSI(opts.Tail)})
	}
	opts.PreserveResets = opts.PreserveResets || preserveResets
	return ansi.TruncateANSI(s, width, opts)
}
