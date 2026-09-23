// Package ansi tokenizes terminal escape sequences and measures visible width.
package ansi

import (
	"strconv"
	"strings"

	"github.com/rivo/uniseg"
)

const (
	esc byte = 0x1b
	bel byte = 0x07
)

// TokenType classifies one span of an ANSI string.
type TokenType int

const (
	// TokenText is a run of visible text. Control sequences that are not SGR
	// or OSC 8 hyperlinks are also reported as TokenText with an empty Text
	// field so callers can keep them atomic and zero-width.
	TokenText TokenType = iota
	// TokenSGR is a Select Graphic Rendition sequence that does not reset.
	TokenSGR
	// TokenReset is an SGR reset: ESC[m, or any ESC[...m whose parameters
	// include a value that parses as 0.
	TokenReset
	// TokenHyperlinkOpen starts an OSC 8 hyperlink.
	TokenHyperlinkOpen
	// TokenHyperlinkClose ends an OSC 8 hyperlink.
	TokenHyperlinkClose
)

// Token is a single text run or ANSI sequence.
// Raw is the original substring. Text is the visible text and is empty for
// sequences, which have no display width.
type Token struct {
	Type TokenType
	Raw  string
	Text string
}

// Tokenize splits s into text and ANSI sequence tokens.
// Concatenating every token's Raw reproduces s.
func Tokenize(s string) []Token {
	if s == "" {
		return nil
	}

	tokens := make([]Token, 0, 8)
	textStart := 0
	flush := func(end int) {
		if end <= textStart {
			return
		}
		raw := s[textStart:end]
		tokens = append(tokens, Token{Type: TokenText, Raw: raw, Text: raw})
	}

	for i := 0; i < len(s); {
		if s[i] != esc {
			i++
			continue
		}
		raw, typ, ok := parseSequence(s, i)
		if !ok {
			i++
			continue
		}
		flush(i)
		tokens = append(tokens, Token{Type: typ, Raw: raw, Text: ""})
		i += len(raw)
		textStart = i
	}
	flush(len(s))
	return tokens
}

// StripANSI returns s without ANSI escape sequences.
func StripANSI(s string) string {
	if !HasANSI(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, tok := range Tokenize(s) {
		if tok.Text != "" {
			b.WriteString(tok.Text)
		}
	}
	return b.String()
}

// ANSIWidth reports the visible cell width of s.
// CSI and OSC sequences have zero width. Wide runes occupy two cells and
// U+200B occupies zero.
func ANSIWidth(s string) int {
	if s == "" {
		return 0
	}
	if !HasANSI(s) {
		return textWidth(s)
	}
	w := 0
	for _, tok := range Tokenize(s) {
		if tok.Text != "" {
			w += textWidth(tok.Text)
		}
	}
	return w
}

// HasANSI reports whether s contains an ESC byte.
func HasANSI(s string) bool {
	return strings.IndexByte(s, esc) >= 0
}

func textWidth(s string) int {
	return uniseg.StringWidth(s)
}

func parseSequence(s string, i int) (raw string, typ TokenType, ok bool) {
	if i >= len(s) || s[i] != esc || i+1 >= len(s) {
		return "", 0, false
	}
	switch s[i+1] {
	case '[':
		return parseCSI(s, i)
	case ']':
		return parseOSC(s, i)
	default:
		return parseEscFinal(s, i)
	}
}

func parseCSI(s string, start int) (string, TokenType, bool) {
	j := start + 2
	for j < len(s) {
		c := s[j]
		switch {
		case c >= 0x30 && c <= 0x3F:
			j++
		case c >= 0x20 && c <= 0x2F:
			j++
		case c >= 0x40 && c <= 0x7E:
			j++
			raw := s[start:j]
			if c != 'm' {
				return raw, TokenText, true
			}
			params := raw[2 : len(raw)-1]
			if isLiteralReset(params) {
				return raw, TokenReset, true
			}
			return raw, TokenSGR, true
		default:
			if j == start+2 {
				return "", 0, false
			}
			return s[start:j], TokenText, true
		}
	}
	if j == start+2 {
		return "", 0, false
	}
	return s[start:j], TokenText, true
}

func parseOSC(s string, start int) (string, TokenType, bool) {
	j := start + 2
	for j < len(s) {
		if s[j] == bel {
			raw := s[start : j+1]
			return raw, classifyOSC(oscBody(raw)), true
		}
		if s[j] == esc {
			if j+1 < len(s) && s[j+1] == '\\' {
				raw := s[start : j+2]
				return raw, classifyOSC(oscBody(raw)), true
			}
			if j == start+2 {
				return "", 0, false
			}
			return s[start:j], TokenText, true
		}
		j++
	}
	if j == start+2 {
		return "", 0, false
	}
	return s[start:j], TokenText, true
}

func parseEscFinal(s string, start int) (string, TokenType, bool) {
	j := start + 1
	if s[j] >= 0x20 && s[j] <= 0x2F {
		for j < len(s) && s[j] >= 0x20 && s[j] <= 0x2F {
			j++
		}
		if j < len(s) && s[j] >= 0x30 && s[j] <= 0x7E {
			return s[start : j+1], TokenText, true
		}
		return "", 0, false
	}
	if s[j] >= 0x30 && s[j] <= 0x7E {
		return s[start : j+1], TokenText, true
	}
	return "", 0, false
}

func oscBody(raw string) string {
	if len(raw) < 2 || raw[0] != esc || raw[1] != ']' {
		return ""
	}
	body := raw[2:]
	if strings.HasSuffix(body, "\x1b\\") {
		return strings.TrimSuffix(body, "\x1b\\")
	}
	if strings.HasSuffix(body, "\a") {
		return strings.TrimSuffix(body, "\a")
	}
	return body
}

func classifyOSC(body string) TokenType {
	if !strings.HasPrefix(body, "8;") {
		return TokenText
	}
	rest := body[2:]
	idx := strings.IndexByte(rest, ';')
	if idx < 0 {
		return TokenText
	}
	if rest[idx+1:] == "" {
		return TokenHyperlinkClose
	}
	return TokenHyperlinkOpen
}

// isLiteralReset reports whether an SGR parameter string should be tokenized
// as a reset. An empty parameter list (ESC[m) is a reset. So is any
// semicolon-separated parameter that parses as integer 0, including color
// subparameters such as the blue component of a truecolor sequence.
func isLiteralReset(params string) bool {
	if params == "" {
		return true
	}
	for _, p := range strings.Split(params, ";") {
		if p == "" {
			return true
		}
		if n, err := strconv.Atoi(p); err == nil && n == 0 {
			return true
		}
	}
	return false
}

func sgrParams(raw string) (string, bool) {
	if len(raw) < 3 || raw[0] != esc || raw[1] != '[' || raw[len(raw)-1] != 'm' {
		return "", false
	}
	return raw[2 : len(raw)-1], true
}

func isPureReset(params string) bool {
	if params == "" {
		return true
	}
	for _, p := range strings.Split(params, ";") {
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil || n != 0 {
			return false
		}
	}
	return true
}
