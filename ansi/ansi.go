// Package ansi provides helpers for inspecting and truncating ANSI strings.
package ansi

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// TokenType identifies an ANSI token.
type TokenType int

const (
	TokenText TokenType = iota
	TokenSGR
	TokenReset
	TokenHyperlinkOpen
	TokenHyperlinkClose
	tokenControl TokenType = -1
)

// Token is a piece of text or an ANSI control sequence.
type Token struct {
	Type TokenType
	Raw  string
	Text string
}

// TruncateOptions controls ANSI truncation.
type TruncateOptions struct {
	Tail           string
	PreserveResets bool
}

// Tokenize splits text and supported ANSI control sequences without splitting
// escape sequences.
func Tokenize(s string) []Token {
	var tokens []Token
	for i := 0; i < len(s); {
		if s[i] != '\x1b' {
			j := i + 1
			for j < len(s) && s[j] != '\x1b' {
				_, size := utf8.DecodeRuneInString(s[j:])
				j += size
			}
			tokens = append(tokens, Token{Type: TokenText, Raw: s[i:j], Text: s[i:j]})
			i = j
			continue
		}

		end, typ := sequenceEnd(s, i)
		if end == i {
			tokens = append(tokens, Token{Type: TokenText, Raw: s[i : i+1], Text: s[i : i+1]})
			i++
			continue
		}
		raw := s[i:end]
		tokenType := typ
		if typ == TokenSGR && isReset(raw) {
			tokenType = TokenReset
		}
		tokens = append(tokens, Token{Type: tokenType, Raw: raw})
		i = end
	}
	return tokens
}

func sequenceEnd(s string, start int) (int, TokenType) {
	if start+1 >= len(s) {
		return start, TokenText
	}
	switch s[start+1] {
	case '[':
		for i := start + 2; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				if s[i] == 'm' {
					return i + 1, TokenSGR
				}
				return i + 1, tokenControl
			}
		}
	case ']':
		for i := start + 2; i < len(s); i++ {
			if s[i] == '\a' {
				return i + 1, hyperlinkType(s[start+2 : i])
			}
			if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2, hyperlinkType(s[start+2 : i])
			}
		}
	}
	return start, TokenText
}

func hyperlinkType(payload string) TokenType {
	if strings.HasPrefix(payload, "8;") {
		if strings.HasPrefix(payload, "8;;") {
			return TokenHyperlinkClose
		}
		return TokenHyperlinkOpen
	}
	return tokenControl
}

func sgrParams(raw string) []int {
	body := raw[2 : len(raw)-1]
	if body == "" {
		return []int{0}
	}
	parts := strings.Split(body, ";")
	params := make([]int, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err == nil {
			params = append(params, n)
		}
	}
	return params
}

func isReset(raw string) bool {
	for _, param := range sgrParams(raw) {
		if param == 0 {
			return true
		}
	}
	return false
}

// StripANSI removes ANSI control sequences from s.
func StripANSI(s string) string {
	var b strings.Builder
	for _, token := range Tokenize(s) {
		if token.Type == TokenText {
			b.WriteString(token.Text)
		}
	}
	return b.String()
}

// ANSIWidth returns the visible Unicode width of s.
func ANSIWidth(s string) int {
	return uniseg.StringWidth(StripANSI(s))
}

// HasANSI reports whether s contains a recognized ANSI control sequence.
func HasANSI(s string) bool {
	for _, token := range Tokenize(s) {
		if token.Type != TokenText {
			return true
		}
	}
	return false
}

// TruncateANSI truncates s to width, preserving complete ANSI sequences.
func TruncateANSI(s string, width int, opts TruncateOptions) string {
	if width <= 0 {
		return ""
	}
	tailWidth := ANSIWidth(opts.Tail)
	tail := opts.Tail
	if tailWidth > width {
		tail = TruncateANSI(tail, width, TruncateOptions{})
		tailWidth = ANSIWidth(tail)
	}
	budget := width - tailWidth
	if budget < 0 {
		budget = 0
	}

	var b strings.Builder
	visible := 0
	enclosing := make([]string, 0)
	active := false
	hyperlink := false
	truncated := false
	for _, token := range Tokenize(s) {
		switch token.Type {
		case TokenText:
			graphemes := uniseg.NewGraphemes(token.Text)
			for graphemes.Next() {
				cluster := graphemes.Str()
				w := uniseg.StringWidth(cluster)
				if visible+w > budget {
					truncated = true
					break
				}
				b.WriteString(cluster)
				visible += w
			}
		case TokenReset:
			b.WriteString(token.Raw)
			active = false
			if opts.PreserveResets && len(enclosing) > 0 {
				b.WriteString("\x1b[" + strings.Join(enclosing, ";") + "m")
				active = true
			}
		case TokenSGR:
			b.WriteString(token.Raw)
			enclosing = append(enclosing, token.Raw[2:len(token.Raw)-1])
			active = true
		case TokenHyperlinkOpen:
			b.WriteString(token.Raw)
			hyperlink = true
		case TokenHyperlinkClose:
			b.WriteString(token.Raw)
			hyperlink = false
		}
		if truncated {
			break
		}
	}

	if truncated && tail != "" {
		b.WriteString(tail)
	}
	if hyperlink {
		b.WriteString("\x1b]8;;\x1b\\")
	}
	if active {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}
