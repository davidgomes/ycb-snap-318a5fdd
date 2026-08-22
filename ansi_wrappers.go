package termenv

import "github.com/muesli/termenv/ansi"

type TruncateOptions = ansi.TruncateOptions

func TruncateANSI(s string, width int, opts TruncateOptions) string {
	return ansi.TruncateANSI(s, width, opts)
}
func StripANSI(s string) string { return ansi.StripANSI(s) }
func ANSIWidth(s string) int    { return ansi.ANSIWidth(s) }
func HasANSI(s string) bool     { return ansi.HasANSI(s) }
