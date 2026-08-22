package ansi

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	esc = '\x1b'
)

func isTerminator(c byte) bool {
	return (c >= 0x40 && c <= 0x5a) || (c >= 0x61 && c <= 0x7a)
}

// Tokenize splits s into text and escape sequence tokens without breaking
// CSI or OSC sequences.
func Tokenize(s string) []Token {
	if s == "" {
		return nil
	}

	var tokens []Token
	var text strings.Builder

	flushText := func() {
		if text.Len() == 0 {
			return
		}
		tokens = append(tokens, Token{
			Type: TokenText,
			Raw:  text.String(),
			Text: text.String(),
		})
		text.Reset()
	}

	for i := 0; i < len(s); {
		if s[i] != esc {
			_, size := utf8.DecodeRuneInString(s[i:])
			text.WriteString(s[i : i+size])
			i += size
			continue
		}

		flushText()

		if i+1 >= len(s) {
			text.WriteByte(s[i])
			i++
			continue
		}

		switch s[i+1] {
		case '[':
			j := i + 2
			for j < len(s) && !isTerminator(s[j]) {
				j++
			}
			if j >= len(s) {
				text.WriteString(s[i:])
				return tokens
			}
			j++
			raw := s[i:j]
			if s[j-1] == 'm' && isResetSGR(raw) {
				tokens = append(tokens, Token{Type: TokenReset, Raw: raw})
			} else if s[j-1] == 'm' {
				tokens = append(tokens, Token{Type: TokenSGR, Raw: raw})
			} else {
				tokens = append(tokens, Token{Type: TokenSGR, Raw: raw})
			}
			i = j
		case ']':
			j := i + 2
			for j < len(s) {
				if s[j] == esc && j+1 < len(s) && s[j+1] == '\\' {
					j += 2
					break
				}
				if s[j] == '\a' {
					j++
					break
				}
				j++
			}
			if j > len(s) {
				j = len(s)
			}
			raw := s[i:j]
			if isHyperlinkOSC(raw) {
				if isHyperlinkClose(raw) {
					tokens = append(tokens, Token{Type: TokenHyperlinkClose, Raw: raw})
				} else {
					tokens = append(tokens, Token{Type: TokenHyperlinkOpen, Raw: raw})
				}
			} else {
				tokens = append(tokens, Token{Type: TokenSGR, Raw: raw})
			}
			i = j
		default:
			j := i + 2
			for j < len(s) && !isTerminator(s[j]) {
				j++
			}
			if j >= len(s) {
				text.WriteString(s[i:])
				return tokens
			}
			j++
			tokens = append(tokens, Token{Type: TokenSGR, Raw: s[i:j]})
			i = j
		}
	}

	flushText()
	return tokens
}

func isResetSGR(raw string) bool {
	if len(raw) < 3 || raw[0] != esc || raw[1] != '[' || raw[len(raw)-1] != 'm' {
		return false
	}

	params := raw[2 : len(raw)-1]
	if params == "" {
		return true
	}

	for _, part := range strings.Split(params, ";") {
		if part == "" {
			return true
		}
		n, err := strconv.Atoi(part)
		if err != nil || n == 0 {
			return true
		}
	}

	return false
}

func isHyperlinkOSC(raw string) bool {
	if len(raw) < 4 || raw[0] != esc || raw[1] != ']' {
		return false
	}

	body := raw[2:]
	if strings.HasPrefix(body, "\a") {
		return false
	}

	payload := body
	if idx := strings.Index(payload, string([]byte{esc, '\\'})); idx >= 0 {
		payload = payload[:idx]
	} else if idx := strings.Index(payload, "\a"); idx >= 0 {
		payload = payload[:idx]
	}

	return strings.HasPrefix(payload, "8") && (len(payload) == 1 || payload[1] == ';')
}

func isHyperlinkClose(raw string) bool {
	if !isHyperlinkOSC(raw) {
		return false
	}

	body := raw[2:]
	payload := body
	if idx := strings.Index(payload, string([]byte{esc, '\\'})); idx >= 0 {
		payload = payload[:idx]
	} else if idx := strings.Index(payload, "\a"); idx >= 0 {
		payload = payload[:idx]
	}

	if payload == "8" {
		return true
	}
	if !strings.HasPrefix(payload, "8;") {
		return false
	}

	parts := strings.Split(payload, ";")
	if len(parts) < 3 {
		return true
	}

	return parts[2] == ""
}
