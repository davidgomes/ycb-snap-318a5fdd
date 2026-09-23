// Package ansi tokenizes ANSI escape sequences and truncates styled text
// without splitting those sequences.
package ansi

import (
	"strconv"
	"strings"

	"github.com/rivo/uniseg"
)

const (
	esc byte = 0x1b
	bel byte = 0x07

	// CSI parameter and intermediate bytes, then a final byte.
	csiByteMin  = 0x20
	csiByteMax  = 0x3f
	csiFinalMin = 0x40
	csiFinalMax = 0x7e

	// ESC Fe single-byte finals.
	feMin = 0x40
	feMax = 0x5f
)

// TokenType classifies a span of terminal text.
type TokenType int

const (
	// TokenText is printable text. Raw and Text are the same span.
	TokenText TokenType = iota
	// TokenSGR is a Select Graphic Rendition sequence that does not reset.
	TokenSGR
	// TokenReset is ESC[m or any SGR sequence with a parameter that parses as 0.
	TokenReset
	// TokenHyperlinkOpen is an OSC 8 sequence that starts a hyperlink.
	TokenHyperlinkOpen
	// TokenHyperlinkClose is an OSC 8 sequence that ends a hyperlink.
	TokenHyperlinkClose
)

// Token is one ANSI span.
//
// Raw is the original bytes. Text is the visible text, the SGR parameter
// string, or the hyperlink URI.
type Token struct {
	Type TokenType
	Raw  string
	Text string
}

// Tokenize splits s into text, SGR, reset, and OSC 8 hyperlink tokens.
// Other CSI and OSC sequences are returned as TokenSGR so callers can copy
// them without splitting them.
func Tokenize(s string) []Token {
	tokens := make([]Token, 0)
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		text := b.String()
		tokens = append(tokens, Token{Type: TokenText, Raw: text, Text: text})
		b.Reset()
	}

	for i := 0; i < len(s); {
		if s[i] != esc {
			b.WriteByte(s[i])
			i++
			continue
		}
		n, typ, text := parseEscape(s[i:])
		if n == 0 {
			b.WriteByte(s[i])
			i++
			continue
		}
		flush()
		tokens = append(tokens, Token{Type: typ, Raw: s[i : i+n], Text: text})
		i += n
	}
	flush()
	return tokens
}

// StripANSI returns s without CSI or OSC sequences.
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

// ANSIWidth returns the number of terminal columns s occupies.
// Escape sequences have zero width. Wide runes occupy two columns and
// U+200B occupies none.
func ANSIWidth(s string) int {
	width := 0
	for _, tok := range Tokenize(s) {
		if tok.Type != TokenText {
			continue
		}
		width += stringWidth(tok.Text)
	}
	return width
}

// HasANSI reports whether s contains an ESC byte.
func HasANSI(s string) bool {
	return strings.IndexByte(s, esc) >= 0
}

func parseEscape(s string) (n int, typ TokenType, text string) {
	if len(s) < 2 || s[0] != esc {
		return 0, TokenText, ""
	}
	switch s[1] {
	case '[':
		n = scanCSI(s)
		if n == 0 {
			return 0, TokenText, ""
		}
		typ, text = classifyCSI(s[:n])
		return n, typ, text
	case ']':
		n = scanOSC(s)
		if n < 2 {
			return 0, TokenText, ""
		}
		typ, text = classifyOSC(s[:n])
		return n, typ, text
	default:
		if s[1] >= feMin && s[1] <= feMax {
			return 2, TokenSGR, ""
		}
		return 0, TokenText, ""
	}
}

func scanCSI(s string) int {
	i := 2
	for i < len(s) {
		c := s[i]
		if c >= csiFinalMin && c <= csiFinalMax {
			return i + 1
		}
		if c >= csiByteMin && c <= csiByteMax {
			i++
			continue
		}
		if i == 2 {
			return 0
		}
		return i
	}
	if len(s) >= 2 {
		return len(s)
	}
	return 0
}

func scanOSC(s string) int {
	i := 2
	for i < len(s) {
		if s[i] == bel {
			return i + 1
		}
		if s[i] == esc && i+1 < len(s) && s[i+1] == '\\' {
			return i + 2
		}
		i++
	}
	return len(s)
}

func classifyCSI(raw string) (TokenType, string) {
	if len(raw) < 3 || raw[len(raw)-1] < csiFinalMin || raw[len(raw)-1] > csiFinalMax {
		body := ""
		if len(raw) > 2 {
			body = raw[2:]
		}
		return TokenSGR, body
	}
	params := raw[2 : len(raw)-1]
	if raw[len(raw)-1] == 'm' && isResetSGR(params) {
		return TokenReset, params
	}
	return TokenSGR, params
}

func classifyOSC(raw string) (TokenType, string) {
	payload := oscPayload(raw)
	parts := strings.SplitN(payload, ";", 3)
	if len(parts) == 0 || parts[0] != "8" {
		return TokenSGR, payload
	}
	uri := ""
	if len(parts) == 3 {
		uri = parts[2]
	}
	if uri == "" {
		return TokenHyperlinkClose, ""
	}
	return TokenHyperlinkOpen, uri
}

func oscPayload(raw string) string {
	if len(raw) < 2 {
		return ""
	}
	body := raw[2:]
	body = strings.TrimSuffix(body, "\x1b\\")
	body = strings.TrimSuffix(body, "\a")
	return body
}

// isResetSGR reports whether an SGR parameter list resets attributes.
// ESC[m (an empty parameter list) is a reset. So is any semicolon-separated
// parameter that strconv parses as the integer 0.
func isResetSGR(params string) bool {
	if params == "" {
		return true
	}
	for _, p := range strings.Split(params, ";") {
		n, err := strconv.Atoi(p)
		if err == nil && n == 0 {
			return true
		}
	}
	return false
}

// establishesStyle reports whether a reset sequence also sets attributes
// after the reset parameter, such as ESC[0;31m.
func establishesStyle(params string) bool {
	if params == "" {
		return false
	}
	for _, p := range strings.Split(params, ";") {
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err == nil && n != 0 {
			return true
		}
	}
	return false
}

func stringWidth(s string) int {
	width := 0
	state := -1
	for len(s) > 0 {
		var cluster string
		var w int
		cluster, s, w, state = uniseg.FirstGraphemeClusterInString(s, state)
		width += clusterWidth(cluster, w)
	}
	return width
}

func clusterWidth(cluster string, width int) int {
	if cluster == "\u200b" {
		return 0
	}
	return width
}
