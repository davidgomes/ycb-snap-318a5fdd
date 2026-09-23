// Package ansi tokenizes terminal escape sequences and truncates strings
// without splitting CSI or OSC commands.
package ansi

import (
	"strconv"
	"strings"

	"github.com/rivo/uniseg"
)

// TokenType classifies one span returned by Tokenize.
type TokenType int

const (
	// TokenText is visible text. Non-SGR escape sequences are also reported
	// as TokenText with an empty Text so they stay atomic and zero-width.
	TokenText TokenType = iota
	// TokenSGR is a Select Graphic Rendition sequence that does not reset.
	TokenSGR
	// TokenReset is an SGR reset: ESC[m, or any ESC[...m with a parameter
	// that parses as 0.
	TokenReset
	// TokenHyperlinkOpen is an OSC 8 hyperlink with a non-empty URI.
	TokenHyperlinkOpen
	// TokenHyperlinkClose is an OSC 8 hyperlink with an empty URI.
	TokenHyperlinkClose
)

// Token is a single text run or one complete escape sequence.
type Token struct {
	Type TokenType
	// Raw is the original text or the full escape sequence.
	Raw string
	// Text is the visible text, the SGR parameter list, or the hyperlink URI.
	Text string
}

const (
	esc = 0x1b
	bel = 0x07

	sgrReset  = "\x1b[0m"
	osc8Close = "\x1b]8;;\x1b\\"
)

// Tokenize splits s into text and ANSI sequence tokens.
// Concatenating every token's Raw reproduces s.
func Tokenize(s string) []Token {
	var tokens []Token
	for i := 0; i < len(s); {
		if s[i] != esc {
			j := i + 1
			for j < len(s) && s[j] != esc {
				j++
			}
			tokens = append(tokens, Token{Type: TokenText, Raw: s[i:j], Text: s[i:j]})
			i = j
			continue
		}
		if i+1 < len(s) && s[i+1] == '[' {
			raw, params, final, next := scanCSI(s, i)
			if final == 'm' && sgrParams(params) {
				typ := TokenSGR
				if isResetParams(params) {
					typ = TokenReset
				}
				tokens = append(tokens, Token{Type: typ, Raw: raw, Text: params})
			} else {
				tokens = append(tokens, Token{Type: TokenText, Raw: raw, Text: ""})
			}
			i = next
			continue
		}
		if i+1 < len(s) && s[i+1] == ']' {
			raw, next := scanOSC(s, i)
			if tok, ok := parseHyperlink(raw); ok {
				tokens = append(tokens, tok)
			} else {
				tokens = append(tokens, Token{Type: TokenText, Raw: raw, Text: ""})
			}
			i = next
			continue
		}
		next := scanESC(s, i)
		tokens = append(tokens, Token{Type: TokenText, Raw: s[i:next], Text: ""})
		i = next
	}
	return tokens
}

// StripANSI returns s with CSI and OSC sequences removed.
func StripANSI(s string) string {
	var b strings.Builder
	for _, tok := range Tokenize(s) {
		if tok.Type == TokenText {
			b.WriteString(tok.Text)
		}
	}
	return b.String()
}

// ANSIWidth reports the visible monospace width of s.
// Escape sequences, wide runes, and zero-width characters such as U+200B
// follow Unicode width rules.
func ANSIWidth(s string) int {
	w := 0
	for _, tok := range Tokenize(s) {
		if tok.Type == TokenText && tok.Text != "" {
			w += uniseg.StringWidth(tok.Text)
		}
	}
	return w
}

// HasANSI reports whether s contains an ESC byte.
func HasANSI(s string) bool {
	return strings.IndexByte(s, esc) >= 0
}

// TruncateOptions controls ANSI-aware truncation.
type TruncateOptions struct {
	// Tail is appended when s is wider than the requested width.
	// It counts toward that width and is written inside the active style.
	Tail string
	// PreserveResets re-opens the active SGR style after each reset run.
	PreserveResets bool
}

// TruncateANSI truncates s to width visible cells.
// CSI and OSC sequences are never split and contribute no visible width.
// A final SGR reset is appended when styles are still active, and an open
// OSC 8 hyperlink is closed.
func TruncateANSI(s string, width int, opts TruncateOptions) string {
	if width < 0 {
		width = 0
	}
	tokens := Tokenize(s)
	visible := 0
	for _, tok := range tokens {
		if tok.Type == TokenText {
			visible += uniseg.StringWidth(tok.Text)
		}
	}

	limit := width
	if visible > width {
		tailW := ANSIWidth(opts.Tail)
		if tailW > width {
			return ""
		}
		limit = width - tailW
	}

	var b strings.Builder
	var style []string
	hyperlink := false
	used := 0
	cut := false

	for i := 0; i < len(tokens); {
		tok := tokens[i]
		if tok.Type == TokenReset {
			j := i + 1
			for j < len(tokens) && tokens[j].Type == TokenReset {
				j++
			}
			for k := i; k < j; k++ {
				b.WriteString(tokens[k].Raw)
			}
			if opts.PreserveResets && len(style) > 0 && j < len(tokens) {
				for _, seq := range style {
					b.WriteString(seq)
				}
			} else {
				style = nil
			}
			i = j
			continue
		}

		switch tok.Type {
		case TokenSGR:
			b.WriteString(tok.Raw)
			style = append(style, tok.Raw)
		case TokenHyperlinkOpen:
			b.WriteString(tok.Raw)
			hyperlink = true
		case TokenHyperlinkClose:
			b.WriteString(tok.Raw)
			hyperlink = false
		default:
			if tok.Text == "" {
				b.WriteString(tok.Raw)
				break
			}
			rest := tok.Text
			state := -1
			for len(rest) > 0 {
				var cluster string
				var w, newState int
				cluster, rest, w, newState = uniseg.FirstGraphemeClusterInString(rest, state)
				if used+w > limit {
					cut = true
					break
				}
				b.WriteString(cluster)
				used += w
				state = newState
			}
		}
		if cut {
			break
		}
		i++
	}

	if cut {
		b.WriteString(opts.Tail)
	}
	if hyperlink {
		b.WriteString(osc8Close)
	}
	if len(style) > 0 {
		b.WriteString(sgrReset)
	}
	return b.String()
}

func scanCSI(s string, i int) (raw, params string, final byte, next int) {
	j := i + 2
	paramStart := j
	for j < len(s) {
		c := s[j]
		if c >= 0x30 && c <= 0x3F {
			j++
			continue
		}
		break
	}
	params = s[paramStart:j]
	for j < len(s) {
		c := s[j]
		if c >= 0x20 && c <= 0x2F {
			j++
			continue
		}
		break
	}
	if j >= len(s) {
		return s[i:], params, 0, len(s)
	}
	final = s[j]
	if final < 0x40 || final > 0x7E {
		return s[i:j], params, 0, j
	}
	j++
	return s[i:j], params, final, j
}

func scanOSC(s string, i int) (raw string, next int) {
	j := i + 2
	for j < len(s) {
		if s[j] == bel {
			return s[i : j+1], j + 1
		}
		if s[j] == esc && j+1 < len(s) && s[j+1] == '\\' {
			return s[i : j+2], j + 2
		}
		j++
	}
	return s[i:], len(s)
}

func scanESC(s string, i int) int {
	j := i + 1
	if j >= len(s) {
		return len(s)
	}
	for j < len(s) && s[j] >= 0x20 && s[j] <= 0x2F {
		j++
	}
	if j < len(s) && s[j] >= 0x30 && s[j] <= 0x7E {
		return j + 1
	}
	if j == i+1 {
		return i + 1
	}
	return j
}

func parseHyperlink(raw string) (Token, bool) {
	body := raw
	if len(body) < 2 || body[0] != esc || body[1] != ']' {
		return Token{}, false
	}
	body = body[2:]
	switch {
	case strings.HasSuffix(body, "\x1b\\"):
		body = strings.TrimSuffix(body, "\x1b\\")
	case strings.HasSuffix(body, "\a"):
		body = strings.TrimSuffix(body, "\a")
	default:
		return Token{}, false
	}
	if !strings.HasPrefix(body, "8;") {
		return Token{}, false
	}
	rest := body[2:]
	semi := strings.IndexByte(rest, ';')
	if semi < 0 {
		return Token{}, false
	}
	uri := rest[semi+1:]
	if uri == "" {
		return Token{Type: TokenHyperlinkClose, Raw: raw}, true
	}
	return Token{Type: TokenHyperlinkOpen, Raw: raw, Text: uri}, true
}

func sgrParams(params string) bool {
	for i := 0; i < len(params); i++ {
		c := params[i]
		if c != ';' && c != ':' && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// isResetParams reports whether an SGR parameter list resets attributes.
// ESC[m (empty params) is a reset. Any semicolon-separated parameter that
// parses as the integer 0 is a reset, including zeros inside a color payload.
func isResetParams(params string) bool {
	if params == "" {
		return true
	}
	for _, p := range strings.Split(params, ";") {
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err == nil && n == 0 {
			return true
		}
	}
	return false
}
