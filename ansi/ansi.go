package ansi

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

type TokenType int

const (
	TokenText TokenType = iota
	TokenSGR
	TokenReset
	TokenHyperlinkOpen
	TokenHyperlinkClose
)

type Token struct {
	Type      TokenType
	Raw, Text string
}
type TruncateOptions struct {
	Tail           string
	PreserveResets bool
}

func Tokenize(s string) []Token {
	var out []Token
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			j := i
			for j < len(s) && s[j] != 0x1b {
				j++
			}
			out = append(out, Token{Type: TokenText, Raw: s[i:j], Text: s[i:j]})
			i = j
			continue
		}
		if i+1 >= len(s) {
			out = append(out, Token{Type: TokenText, Raw: s[i:]})
			break
		}
		if s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < '@' || s[j] > '~') {
				j++
			}
			if j < len(s) {
				j++
				raw := s[i:j]
				typ := TokenSGR
				if s[j-1] == 'm' && sgrReset(raw) {
					typ = TokenReset
				}
				out = append(out, Token{Type: typ, Raw: raw})
				i = j
				continue
			}
		} else if s[i+1] == ']' {
			j := i + 2
			for j < len(s) && s[j] != 0x07 && !(s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\') {
				j++
			}
			if j < len(s) {
				if s[j] == 0x07 {
					j++
				} else {
					j += 2
				}
				raw := s[i:j]
				typ := TokenHyperlinkOpen
				if strings.HasPrefix(raw, "\x1b]8;;") && strings.HasSuffix(raw, ";;\x07") || strings.HasPrefix(raw, "\x1b]8;;") && strings.HasSuffix(raw, ";;\x1b\\") {
					typ = TokenHyperlinkClose
				}
				out = append(out, Token{Type: typ, Raw: raw})
				i = j
				continue
			}
		}
		_, n := utf8.DecodeRuneInString(s[i:])
		out = append(out, Token{Type: TokenText, Raw: s[i : i+n], Text: s[i : i+n]})
		i += n
	}
	return out
}

func sgrReset(s string) bool {
	body := s[2 : len(s)-1]
	if body == "" {
		return true
	}
	for _, p := range strings.Split(body, ";") {
		if n, e := strconv.Atoi(p); e == nil && n == 0 {
			return true
		}
	}
	return false
}
func StripANSI(s string) string {
	var b strings.Builder
	for _, t := range Tokenize(s) {
		if t.Type == TokenText {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}
func HasANSI(s string) bool {
	for _, t := range Tokenize(s) {
		if t.Type != TokenText {
			return true
		}
	}
	return false
}
func ANSIWidth(s string) int { return uniseg.StringWidth(StripANSI(s)) }

func TruncateANSI(s string, width int, o TruncateOptions) string {
	if width <= 0 {
		return ""
	}
	tail := o.Tail
	tw := uniseg.StringWidth(tail)
	limit := width - tw
	if limit < 0 {
		limit = 0
	}
	var b strings.Builder
	used := 0
	active := []string{}
	hyperlink := ""
	for _, t := range Tokenize(s) {
		switch t.Type {
		case TokenText:
			w := uniseg.StringWidth(t.Text)
			if used+w > limit {
				gr := uniseg.NewGraphemes(t.Text)
				for gr.Next() {
					g := gr.Str()
					gw := uniseg.StringWidth(g)
					if used+gw > limit {
						break
					}
					b.WriteString(g)
					used += gw
				}
				if used >= limit {
					goto done
				}
				continue
			}
			b.WriteString(t.Raw)
			used += w
		case TokenSGR:
			b.WriteString(t.Raw)
			if o.PreserveResets {
				active = append(active, t.Raw)
			}
		case TokenReset:
			b.WriteString(t.Raw)
			if o.PreserveResets {
				for _, x := range active {
					b.WriteString(x)
				}
			}
		case TokenHyperlinkOpen:
			b.WriteString(t.Raw)
			hyperlink = t.Raw
		case TokenHyperlinkClose:
			b.WriteString(t.Raw)
			hyperlink = ""
		}
	}
done:
	if tw > 0 {
		b.WriteString(tail)
	}
	if hyperlink != "" {
		b.WriteString("\x1b]8;;\x07")
	}
	if o.PreserveResets && len(active) > 0 {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}
