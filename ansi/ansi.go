// Package ansi tokenizes terminal control sequences and truncates strings
// without splitting CSI or OSC sequences.
package ansi

import (
	"strconv"
	"strings"

	"github.com/rivo/uniseg"
)

// TokenType classifies a span of terminal text.
type TokenType int

const (
	// TokenText is printable text. Text holds the visible runes.
	TokenText TokenType = iota
	// TokenSGR is a Select Graphic Rendition sequence that is not a reset.
	TokenSGR
	// TokenReset is an SGR reset: ESC[m, or any SGR whose parameters include 0.
	TokenReset
	// TokenHyperlinkOpen is an OSC 8 hyperlink with a non-empty URI.
	TokenHyperlinkOpen
	// TokenHyperlinkClose is an OSC 8 hyperlink with an empty URI.
	TokenHyperlinkClose
)

// Token is one atomic span. Control sequences are never split.
type Token struct {
	Type TokenType
	Raw  string
	Text string
}

const (
	esc = 0x1b
	bel = 0x07
	stC = 0x9c
	csi = 0x9b
	osc = 0x9d
)

// Tokenize splits s into text and control tokens.
func Tokenize(s string) []Token {
	var tokens []Token
	start := 0
	for i := 0; i < len(s); {
		if !isIntro(s[i]) {
			i++
			continue
		}
		if i > start {
			chunk := s[start:i]
			tokens = append(tokens, Token{Type: TokenText, Raw: chunk, Text: chunk})
		}
		tok, n := scanControl(s[i:])
		tokens = append(tokens, tok)
		i += n
		start = i
	}
	if start < len(s) {
		chunk := s[start:]
		tokens = append(tokens, Token{Type: TokenText, Raw: chunk, Text: chunk})
	}
	return tokens
}

func isIntro(b byte) bool {
	return b == esc || b == csi || b == osc
}

// scanControl consumes one control sequence at the start of s.
func scanControl(s string) (Token, int) {
	if s[0] == csi {
		return scanCSI(s, 1)
	}
	if s[0] == osc {
		return scanOSC(s, 1)
	}
	if len(s) == 1 {
		return zeroToken(s), 1
	}
	switch s[1] {
	case '[':
		return scanCSI(s, 2)
	case ']':
		return scanOSC(s, 2)
	default:
		return zeroToken(s[:2]), 2
	}
}

func zeroToken(raw string) Token {
	return Token{Type: TokenText, Raw: raw, Text: ""}
}

func scanCSI(s string, paramStart int) (Token, int) {
	i := paramStart
	for i < len(s) {
		if s[i] >= 0x40 && s[i] <= 0x7e {
			i++
			raw := s[:i]
			if s[i-1] == 'm' {
				if isReset(s[paramStart : i-1]) {
					return Token{Type: TokenReset, Raw: raw}, i
				}
				return Token{Type: TokenSGR, Raw: raw}, i
			}
			return zeroToken(raw), i
		}
		i++
	}
	return zeroToken(s), len(s)
}

// isReset reports whether an SGR parameter string resets.
// ESC[m (empty params) resets. Otherwise any parameter that parses as 0 resets.
func isReset(params string) bool {
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

func scanOSC(s string, bodyStart int) (Token, int) {
	i := bodyStart
	for i < len(s) {
		if s[i] == bel || s[i] == stC {
			return oscToken(s[:i+1], s[bodyStart:i]), i + 1
		}
		if s[i] == esc && i+1 < len(s) && s[i+1] == '\\' {
			return oscToken(s[:i+2], s[bodyStart:i]), i + 2
		}
		i++
	}
	return zeroToken(s), len(s)
}

func oscToken(raw, body string) Token {
	if typ, ok := hyperlinkType(body); ok {
		return Token{Type: typ, Raw: raw}
	}
	return zeroToken(raw)
}

func hyperlinkType(body string) (TokenType, bool) {
	if !strings.HasPrefix(body, "8;") {
		return 0, false
	}
	rest := body[2:]
	_, uri, ok := strings.Cut(rest, ";")
	if !ok {
		return 0, false
	}
	if uri == "" {
		return TokenHyperlinkClose, true
	}
	return TokenHyperlinkOpen, true
}

// TruncateOptions controls ANSI-aware truncation.
type TruncateOptions struct {
	Tail           string
	PreserveResets bool
}

// TruncateANSI truncates s to width visible columns. Tail counts toward width
// and is emitted only when s is longer than width. Control sequences are kept
// intact and have zero width. When styles are still active at the cut, a final
// SGR reset is appended. Open OSC 8 hyperlinks are closed.
func TruncateANSI(s string, width int, opts TruncateOptions) string {
	if width <= 0 {
		return ""
	}
	needsCut := ANSIWidth(s) > width
	if !needsCut && !opts.PreserveResets {
		return s
	}
	tail := ""
	budget := width
	if needsCut {
		tailWidth := uniseg.StringWidth(opts.Tail)
		if tailWidth > width {
			return truncatePlain(opts.Tail, width)
		}
		budget = width - tailWidth
		tail = opts.Tail
	}

	tokens := Tokenize(s)
	var b strings.Builder
	var active []string
	var pending bool
	hyperOpen := false

	flush := func() {
		if !pending {
			return
		}
		for _, seq := range active {
			b.WriteString(seq)
		}
		pending = false
	}

	stop := false
	for _, tok := range tokens {
		if stop {
			break
		}
		switch tok.Type {
		case TokenReset:
			if opts.PreserveResets {
				pending = len(active) > 0
				b.WriteString(tok.Raw)
				break
			}
			pending = false
			active = nil
			b.WriteString(tok.Raw)
		case TokenSGR:
			flush()
			active = append(active, tok.Raw)
			b.WriteString(tok.Raw)
		case TokenHyperlinkOpen:
			flush()
			hyperOpen = true
			b.WriteString(tok.Raw)
		case TokenHyperlinkClose:
			flush()
			hyperOpen = false
			b.WriteString(tok.Raw)
		default:
			if tok.Text == "" {
				b.WriteString(tok.Raw)
				break
			}
			fit, rest := takeWidth(tok.Text, budget)
			if fit != "" {
				flush()
				b.WriteString(fit)
				budget -= uniseg.StringWidth(fit)
			}
			if needsCut && (rest != "" || budget == 0) {
				stop = true
			}
		}
	}

	if tail != "" {
		flush()
		b.WriteString(tail)
	}
	if hyperOpen {
		b.WriteString("\x1b]8;;\x1b\\")
	}
	// A pending restore means the cut landed on a reset and the tail (if any)
	// already replayed the style. Skip a second reset when nothing reopened.
	if len(active) > 0 && !pending {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

func takeWidth(s string, width int) (fit, rest string) {
	if width <= 0 {
		return "", s
	}
	state := -1
	used := 0
	i := 0
	for i < len(s) {
		cluster, _, w, ns := uniseg.FirstGraphemeClusterInString(s[i:], state)
		if used+w > width {
			return s[:i], s[i:]
		}
		used += w
		i += len(cluster)
		state = ns
	}
	return s, ""
}

func truncatePlain(s string, width int) string {
	fit, _ := takeWidth(s, width)
	return fit
}

// StripANSI returns s without control sequences.
func StripANSI(s string) string {
	tokens := Tokenize(s)
	var b strings.Builder
	for _, tok := range tokens {
		b.WriteString(tok.Text)
	}
	return b.String()
}

// ANSIWidth returns the visible column width of s.
func ANSIWidth(s string) int {
	tokens := Tokenize(s)
	w := 0
	for _, tok := range tokens {
		if tok.Text != "" {
			w += uniseg.StringWidth(tok.Text)
		}
	}
	return w
}

// HasANSI reports whether s contains a recognized control sequence.
func HasANSI(s string) bool {
	for _, tok := range Tokenize(s) {
		if tok.Type != TokenText || tok.Raw != tok.Text {
			return true
		}
	}
	return false
}
