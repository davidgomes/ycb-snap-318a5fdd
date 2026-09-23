// Package ansi provides ANSI escape sequence aware string utilities.
package ansi

import (
	"strconv"
	"strings"

	"github.com/rivo/uniseg"
)

// TokenType identifies the kind of a Token.
type TokenType int

// Token types.
const (
	TokenText TokenType = iota
	TokenSGR
	TokenReset
	TokenHyperlinkOpen
	TokenHyperlinkClose
)

const (
	resetSeq          = "\x1b[0m"
	hyperlinkCloseSeq = "\x1b]8;;\x1b\\"
)

// Token is a piece of a string: either visible text or an escape sequence.
// Raw holds the original bytes. Text holds the visible text and is empty for
// escape sequences. Escape sequences other than SGR and OSC 8 are reported as
// TokenText with an empty Text.
type Token struct {
	Type TokenType
	Raw  string
	Text string
}

// Tokenize splits s into text and escape sequence tokens.
func Tokenize(s string) []Token {
	var tokens []Token
	textStart := 0
	flush := func(end int) {
		if end > textStart {
			t := s[textStart:end]
			tokens = append(tokens, Token{Type: TokenText, Raw: t, Text: t})
		}
	}
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			i++
			continue
		}
		n, tok := parseEscape(s[i:])
		flush(i)
		tokens = append(tokens, tok)
		i += n
		textStart = i
	}
	flush(len(s))
	return tokens
}

// parseEscape parses the escape sequence at the start of s (s[0] == ESC).
func parseEscape(s string) (int, Token) {
	if len(s) < 2 {
		return len(s), Token{Type: TokenText, Raw: s}
	}
	switch s[1] {
	case '[':
		j := 2
		for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
			j++
		}
		if j < len(s) {
			j++
		}
		raw := s[:j]
		if raw[len(raw)-1] == 'm' {
			if isReset(raw[2 : len(raw)-1]) {
				return j, Token{Type: TokenReset, Raw: raw}
			}
			return j, Token{Type: TokenSGR, Raw: raw}
		}
		return j, Token{Type: TokenText, Raw: raw}
	case ']':
		j := 2
		bodyEnd := len(s)
		for j < len(s) {
			if s[j] == 0x07 {
				bodyEnd = j
				j++
				break
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				bodyEnd = j
				j += 2
				break
			}
			j++
		}
		if j >= len(s) && bodyEnd == len(s) {
			j = len(s)
		}
		raw := s[:j]
		body := s[2:bodyEnd]
		if strings.HasPrefix(body, "8;") {
			parts := strings.SplitN(body, ";", 3)
			if len(parts) == 3 && parts[2] == "" {
				return j, Token{Type: TokenHyperlinkClose, Raw: raw}
			}
			return j, Token{Type: TokenHyperlinkOpen, Raw: raw}
		}
		return j, Token{Type: TokenText, Raw: raw}
	default:
		return 2, Token{Type: TokenText, Raw: s[:2]}
	}
}

func isReset(params string) bool {
	if params == "" {
		return true
	}
	for _, p := range strings.FieldsFunc(params, func(r rune) bool { return r == ';' || r == ':' }) {
		if v, err := strconv.Atoi(p); err == nil && v == 0 {
			return true
		}
	}
	return false
}

// TruncateOptions configures TruncateANSI.
type TruncateOptions struct {
	// Tail is appended when s is truncated. Its width counts toward the limit.
	Tail string
	// PreserveResets re-opens the enclosing style after each reset run.
	PreserveResets bool
}

// TruncateANSI truncates s to at most width visible cells without splitting
// escape sequences. Active styles are reset and open hyperlinks are closed.
func TruncateANSI(s string, width int, opts TruncateOptions) string {
	if width < 0 {
		width = 0
	}
	fits := ANSIWidth(s) <= width
	if fits && !opts.PreserveResets {
		return s
	}

	tail := opts.Tail
	limit := width
	if !fits {
		tailWidth := ANSIWidth(tail)
		if tailWidth > width {
			tail = TruncateANSI(tail, width, TruncateOptions{})
			tailWidth = ANSIWidth(tail)
		}
		limit = width - tailWidth
	}

	var (
		b           strings.Builder
		enclosing   string
		seenReset   bool
		styleActive bool
		pending     bool
		linkOpen    bool
		used        int
	)
	flushPending := func() {
		if pending {
			b.WriteString(enclosing)
			styleActive = enclosing != ""
			pending = false
		}
	}

	truncated := false
loop:
	for _, tok := range Tokenize(s) {
		switch tok.Type {
		case TokenReset:
			b.WriteString(tok.Raw)
			styleActive = false
			seenReset = true
			pending = opts.PreserveResets && enclosing != ""
		case TokenSGR:
			flushPending()
			if !seenReset {
				enclosing += tok.Raw
			}
			b.WriteString(tok.Raw)
			styleActive = true
		case TokenHyperlinkOpen:
			flushPending()
			b.WriteString(tok.Raw)
			linkOpen = true
		case TokenHyperlinkClose:
			b.WriteString(tok.Raw)
			linkOpen = false
		default:
			if tok.Text == "" {
				b.WriteString(tok.Raw)
				continue
			}
			g := uniseg.NewGraphemes(tok.Text)
			for g.Next() {
				w := g.Width()
				if used+w > limit {
					truncated = true
					break loop
				}
				flushPending()
				b.WriteString(g.Str())
				used += w
			}
		}
	}

	if truncated && tail != "" {
		if linkOpen {
			b.WriteString(hyperlinkCloseSeq)
			linkOpen = false
		}
		flushPending()
		b.WriteString(tail)
		if HasANSI(tail) {
			styleActive = true
		}
	}
	if linkOpen {
		b.WriteString(hyperlinkCloseSeq)
	}
	if styleActive {
		b.WriteString(resetSeq)
	}
	return b.String()
}

// StripANSI removes all escape sequences from s.
func StripANSI(s string) string {
	if !HasANSI(s) {
		return s
	}
	var b strings.Builder
	for _, tok := range Tokenize(s) {
		b.WriteString(tok.Text)
	}
	return b.String()
}

// ANSIWidth returns the visible cell width of s, ignoring escape sequences.
func ANSIWidth(s string) int {
	return uniseg.StringWidth(StripANSI(s))
}

// HasANSI reports whether s contains an escape sequence.
func HasANSI(s string) bool {
	return strings.IndexByte(s, 0x1b) >= 0
}
