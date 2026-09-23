// Package ansi provides helpers for inspecting, measuring, and truncating
// strings that contain ANSI escape sequences.
package ansi

import (
	"strconv"
	"strings"

	"github.com/rivo/uniseg"
)

const (
	esc = '\x1b'
	bel = '\a'

	csi = "\x1b["
	osc = "\x1b]"
	st  = "\x1b\\"

	// ResetSeq is the canonical SGR reset sequence.
	ResetSeq = csi + "0m"
)

// TokenType identifies the kind of a Token.
type TokenType int

const (
	// TokenText is printable text. Escape sequences other than SGR and OSC 8
	// hyperlinks (e.g. cursor movement or other OSC commands) are also
	// reported as TokenText, with an empty Text since they are not visible.
	TokenText TokenType = iota
	// TokenSGR is an SGR (Select Graphic Rendition) sequence that is not a
	// reset.
	TokenSGR
	// TokenReset is an SGR sequence that resets styles: ESC[m, or any
	// ESC[...m where a parameter parses to 0.
	TokenReset
	// TokenHyperlinkOpen is an OSC 8 sequence with a non-empty URI.
	TokenHyperlinkOpen
	// TokenHyperlinkClose is an OSC 8 sequence with an empty URI.
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

// Token is a segment of a string produced by Tokenize.
type Token struct {
	Type TokenType
	// Raw holds the exact bytes of the segment in the input.
	Raw string
	// Text holds the visible text of the segment; it is empty for escape
	// sequences.
	Text string
}

// Tokenize splits s into text runs and escape sequences. Concatenating the
// Raw fields of the result yields s; concatenating the Text fields yields
// StripANSI(s).
func Tokenize(s string) []Token {
	var tokens []Token
	textStart := 0
	flush := func(end int) {
		if end > textStart {
			tokens = append(tokens, Token{Type: TokenText, Raw: s[textStart:end], Text: s[textStart:end]})
		}
	}

	for i := 0; i < len(s); {
		if s[i] != esc {
			i++
			continue
		}
		flush(i)
		n := seqLen(s[i:])
		raw := s[i : i+n]
		tokens = append(tokens, Token{Type: classify(raw), Raw: raw})
		i += n
		textStart = i
	}
	flush(len(s))

	return tokens
}

// seqLen returns the byte length of the escape sequence at the start of s,
// which must begin with ESC. Unterminated sequences extend to the end of s so
// they are never split.
func seqLen(s string) int {
	if len(s) < 2 { //nolint:mnd
		return len(s)
	}
	switch s[1] {
	case '[':
		for i := 2; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				return i + 1
			}
		}
		return len(s)
	case ']', 'P', 'X', '^', '_':
		for i := 2; i < len(s); i++ {
			if s[i] == bel {
				return i + 1
			}
			if s[i] == esc && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2 //nolint:mnd
			}
		}
		return len(s)
	default:
		i := 1
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
			i++
		}
		if i < len(s) {
			i++
		}
		return i
	}
}

func classify(raw string) TokenType {
	switch {
	case strings.HasPrefix(raw, csi) && strings.HasSuffix(raw, "m"):
		if isResetParams(raw[len(csi) : len(raw)-1]) {
			return TokenReset
		}
		return TokenSGR
	case strings.HasPrefix(raw, osc+"8;"):
		if hyperlinkURI(raw) == "" {
			return TokenHyperlinkClose
		}
		return TokenHyperlinkOpen
	}
	return TokenText
}

func isResetParams(params string) bool {
	if params == "" {
		return true
	}
	for _, p := range strings.Split(params, ";") {
		if n, err := strconv.Atoi(p); err == nil && n == 0 {
			return true
		}
	}
	return false
}

// resetLeavesStyle reports whether a reset sequence such as ESC[0;31m sets
// new attributes after its last reset parameter.
func resetLeavesStyle(raw string) bool {
	params := strings.Split(raw[len(csi):len(raw)-1], ";")
	for i := len(params) - 1; i >= 0; i-- {
		if n, err := strconv.Atoi(params[i]); err == nil && n == 0 {
			return i < len(params)-1
		}
	}
	return false
}

// hyperlinkURI returns the URI of an OSC 8 sequence: OSC 8 ; params ; URI ST.
func hyperlinkURI(raw string) string {
	body := strings.TrimPrefix(raw, osc+"8;")
	body = strings.TrimSuffix(strings.TrimSuffix(body, st), string(bel))
	if i := strings.IndexByte(body, ';'); i >= 0 {
		return body[i+1:]
	}
	return ""
}

// hyperlinkClose returns an OSC 8 close sequence using the same terminator as
// the given open sequence.
func hyperlinkClose(open string) string {
	if strings.HasSuffix(open, string(bel)) {
		return osc + "8;;" + string(bel)
	}
	return osc + "8;;" + st
}

// StripANSI removes all escape sequences from s.
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
	return uniseg.StringWidth(StripANSI(s))
}

// HasANSI reports whether s contains any escape sequences.
func HasANSI(s string) bool {
	return strings.IndexByte(s, esc) >= 0
}
