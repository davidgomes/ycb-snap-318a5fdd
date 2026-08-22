package termenv

import (
	"strings"

	"github.com/muesli/termenv/ansi"
	"github.com/rivo/uniseg"
)

// TruncateOptions configures ANSI-aware truncation.
type TruncateOptions = ansi.TruncateOptions

// TruncateANSI truncates s to the given visible width without splitting escape
// sequences.
func TruncateANSI(s string, width int, opts TruncateOptions) string {
	return ansi.TruncateANSI(s, width, opts)
}

// StripANSI removes all ANSI escape sequences from s.
func StripANSI(s string) string {
	return ansi.StripANSI(s)
}

// ANSIWidth returns the visible width of s ignoring ANSI sequences.
func ANSIWidth(s string) int {
	return ansi.ANSIWidth(s)
}

// HasANSI reports whether s contains ANSI escape sequences.
func HasANSI(s string) bool {
	return ansi.HasANSI(s)
}

func truncatePlain(s string, width int, tail string) string {
	if width < 0 {
		width = 0
	}

	tailWidth := uniseg.StringWidth(tail)
	contentWidth := width - tailWidth
	if contentWidth < 0 {
		if tail == "" {
			return ""
		}
		return tail
	}

	curWidth := 0
	var out strings.Builder

	gr := uniseg.NewGraphemes(s)
	for gr.Next() {
		str := gr.Str()
		w := uniseg.StringWidth(str)
		if w == 0 {
			out.WriteString(str)
			continue
		}

		if curWidth+w > contentWidth {
			out.WriteString(tail)
			return out.String()
		}

		out.WriteString(str)
		curWidth += w
	}

	return out.String()
}

func truncatePlainNoTail(s string, width int) string {
	if width < 0 {
		width = 0
	}

	curWidth := 0
	var out strings.Builder

	gr := uniseg.NewGraphemes(s)
	for gr.Next() {
		str := gr.Str()
		w := uniseg.StringWidth(str)
		if w == 0 {
			out.WriteString(str)
			continue
		}

		if curWidth+w > width {
			return out.String()
		}

		out.WriteString(str)
		curWidth += w
	}

	return out.String()
}
