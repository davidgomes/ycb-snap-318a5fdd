package ansi

import (
	"strings"

	"github.com/rivo/uniseg"
)

// HasANSI reports whether s contains ANSI escape sequences.
func HasANSI(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == esc {
			return true
		}
	}
	return false
}

// StripANSI removes all ANSI escape sequences from s.
func StripANSI(s string) string {
	if !HasANSI(s) {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))

	for _, tok := range Tokenize(s) {
		if tok.Type == TokenText {
			b.WriteString(tok.Text)
		}
	}

	return b.String()
}

// ANSIWidth returns the visible width of s ignoring ANSI sequences.
func ANSIWidth(s string) int {
	if s == "" {
		return 0
	}

	width := 0
	for _, tok := range Tokenize(s) {
		if tok.Type == TokenText {
			width += uniseg.StringWidth(tok.Text)
		}
	}

	return width
}
