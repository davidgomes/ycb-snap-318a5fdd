// Package ansi splits strings into printable text and ANSI escape sequences,
// and measures, strips, and truncates them without breaking the sequences.
package ansi

import (
	"strings"

	"github.com/rivo/uniseg"
)

const (
	esc = '\x1b'
	bel = '\a'
)

// TokenType identifies the kind of a Token.
type TokenType int

const (
	// TokenText is printable text. Escape sequences other than SGR and OSC 8
	// hyperlinks are reported as TokenText with an empty Text, so they are
	// kept intact but never counted as visible.
	TokenText TokenType = iota
	// TokenSGR is a Select Graphic Rendition sequence (ESC[...m) that sets
	// attributes without resetting them.
	TokenSGR
	// TokenReset is an SGR sequence that resets all attributes: ESC[m, or any
	// SGR sequence where a parameter is 0. Empty parameters default to 0;
	// the arguments of extended colors (38;5;n, 38;2;r;g;b, and the 48/58
	// equivalents) are not parameters.
	TokenReset
	// TokenHyperlinkOpen is an OSC 8 sequence with a URI, starting a hyperlink.
	TokenHyperlinkOpen
	// TokenHyperlinkClose is an OSC 8 sequence with an empty URI, ending a
	// hyperlink.
	TokenHyperlinkClose
)

// String returns the name of the token type.
func (t TokenType) String() string {
	switch t {
	case TokenText:
		return "Text"
	case TokenSGR:
		return "SGR"
	case TokenReset:
		return "Reset"
	case TokenHyperlinkOpen:
		return "HyperlinkOpen"
	case TokenHyperlinkClose:
		return "HyperlinkClose"
	}
	return "Unknown"
}

// Token is a segment of a string. Concatenating the Raw fields of the tokens
// returned by Tokenize yields the original string.
type Token struct {
	Type TokenType
	// Raw is the segment exactly as it appears in the input.
	Raw string
	// Text is the printable part of the segment. It is empty for escape
	// sequences.
	Text string
}

// Tokenize splits s into text and escape sequence tokens. Adjacent text is
// merged into a single token. Unterminated or malformed sequences are kept as
// zero-width TokenText tokens.
func Tokenize(s string) []Token {
	var toks []Token
	for len(s) > 0 {
		i := strings.IndexByte(s, esc)
		if i < 0 {
			toks = append(toks, Token{Type: TokenText, Raw: s, Text: s})
			break
		}
		if i > 0 {
			toks = append(toks, Token{Type: TokenText, Raw: s[:i], Text: s[:i]})
			s = s[i:]
		}
		n, ok := scanSequence(s)
		toks = append(toks, classify(s[:n], ok))
		s = s[n:]
	}
	return toks
}

// StripANSI returns s with all escape sequences removed.
func StripANSI(s string) string {
	if !HasANSI(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, t := range Tokenize(s) {
		b.WriteString(t.Text)
	}
	return b.String()
}

// ANSIWidth returns the number of terminal cells needed to display s,
// ignoring escape sequences.
func ANSIWidth(s string) int {
	if !HasANSI(s) {
		return uniseg.StringWidth(s)
	}
	var w int
	for _, t := range Tokenize(s) {
		w += uniseg.StringWidth(t.Text)
	}
	return w
}

// HasANSI reports whether s contains any escape sequences.
func HasANSI(s string) bool {
	return strings.IndexByte(s, esc) >= 0
}

// scanSequence returns the length of the escape sequence at the start of s,
// which must begin with ESC, and whether it is complete. An incomplete
// sequence ends where the input ends or where an invalid byte begins.
func scanSequence(s string) (int, bool) {
	if len(s) < 2 {
		return len(s), false
	}
	switch b := s[1]; {
	case b == '[':
		for i := 2; i < len(s); i++ {
			switch c := s[i]; {
			case c >= 0x20 && c <= 0x3f: // parameter and intermediate bytes
			case c >= 0x40 && c <= 0x7e: // final byte
				return i + 1, true
			default:
				return i, false
			}
		}
		return len(s), false
	case b == ']' || b == 'P' || b == 'X' || b == '^' || b == '_':
		// OSC, DCS, SOS, PM, and APC run until BEL or ST (ESC \).
		for i := 2; i < len(s); i++ {
			switch s[i] {
			case bel:
				return i + 1, true
			case esc:
				if i+1 < len(s) && s[i+1] == '\\' {
					return i + 2, true
				}
				return i, false
			}
		}
		return len(s), false
	case b >= 0x20 && b <= 0x2f:
		for i := 2; i < len(s); i++ {
			switch c := s[i]; {
			case c >= 0x20 && c <= 0x2f:
			case c >= 0x30 && c <= 0x7e:
				return i + 1, true
			default:
				return i, false
			}
		}
		return len(s), false
	case b >= 0x30 && b <= 0x7e:
		return 2, true
	}
	return 1, false
}

func classify(raw string, complete bool) Token {
	opaque := Token{Type: TokenText, Raw: raw}
	if !complete {
		return opaque
	}
	switch raw[1] {
	case '[':
		params := raw[2 : len(raw)-1]
		if raw[len(raw)-1] != 'm' || !isSGRParams(params) {
			return opaque
		}
		if lastReset(strings.Split(params, ";")) >= 0 {
			return Token{Type: TokenReset, Raw: raw}
		}
		return Token{Type: TokenSGR, Raw: raw}
	case ']':
		payload := strings.TrimSuffix(strings.TrimSuffix(raw[2:], "\a"), "\x1b\\")
		if payload != "8" && !strings.HasPrefix(payload, "8;") {
			return opaque
		}
		// OSC 8 ; params ; URI
		if parts := strings.SplitN(payload, ";", 3); len(parts) == 3 && parts[2] != "" {
			return Token{Type: TokenHyperlinkOpen, Raw: raw}
		}
		return Token{Type: TokenHyperlinkClose, Raw: raw}
	}
	return opaque
}

func isSGRParams(params string) bool {
	for i := 0; i < len(params); i++ {
		if c := params[i]; (c < '0' || c > '9') && c != ';' && c != ':' {
			return false
		}
	}
	return true
}

// lastReset returns the index of the last field of an SGR parameter list that
// resets all attributes, or -1 if there is none.
func lastReset(fields []string) int {
	last := -1
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		v := f
		if j := strings.IndexByte(f, ':'); j >= 0 {
			v = f[:j]
		}
		switch strings.TrimLeft(v, "0") {
		case "":
			last = i
		case "38", "48", "58":
			// Colon-separated colors carry their arguments in the same field.
			if v != f || i+1 >= len(fields) {
				continue
			}
			switch strings.TrimLeft(fields[i+1], "0") {
			case "5":
				i += 2
			case "2":
				i += 4
			}
		}
	}
	return last
}

// sgrTrailing returns the parameters of a reset sequence that follow its last
// reset, i.e. the attributes it sets after clearing everything.
func sgrTrailing(raw string) string {
	fields := strings.Split(raw[2:len(raw)-1], ";")
	i := lastReset(fields)
	if i < 0 {
		return ""
	}
	return strings.Join(fields[i+1:], ";")
}
