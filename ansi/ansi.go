// Package ansi provides helpers for tokenizing, measuring, stripping and
// truncating strings that contain ANSI escape sequences.
package ansi

import (
	"strconv"
	"strings"

	"github.com/rivo/uniseg"
)

const (
	esc = '\x1b'
	bel = '\a'

	resetSeq     = "\x1b[0m"
	linkCloseSeq = "\x1b]8;;\x1b\\"
)

// TokenType identifies the kind of a Token.
type TokenType int

const (
	// TokenText is visible text. Escape sequences other than SGR and OSC 8
	// hyperlinks are also reported as TokenText with an empty Text, so that
	// they are kept intact but contribute no visible width.
	TokenText TokenType = iota
	// TokenSGR is a Select Graphic Rendition sequence (ESC[...m) that does
	// not reset attributes.
	TokenSGR
	// TokenReset is an SGR sequence that resets attributes: ESC[m, or any
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

// Token is a single lexical element of a string containing ANSI sequences.
// Raw holds the exact bytes of the token; Text holds its visible text, which
// is empty for escape sequences.
type Token struct {
	Type TokenType
	Raw  string
	Text string
}

// TruncateOptions configures TruncateANSI.
type TruncateOptions struct {
	// Tail is appended when the string is truncated. Its width counts toward
	// the requested width and it inherits the style active at the cut.
	Tail string
	// PreserveResets re-opens the enclosing style (the SGR sequences that
	// precede the first visible text) after each run of resets.
	PreserveResets bool
}

// Tokenize splits s into text and escape sequence tokens. Adjacent text is
// merged into a single token. Escape sequences are never split; unterminated
// sequences extend to the end of the string.
func Tokenize(s string) []Token {
	var tokens []Token
	textStart := 0

	flushText := func(end int) {
		if end > textStart {
			t := s[textStart:end]
			tokens = append(tokens, Token{Type: TokenText, Raw: t, Text: t})
		}
	}

	for i := 0; i < len(s); {
		if s[i] != esc {
			i++
			continue
		}
		flushText(i)
		n, typ := scanEscape(s[i:])
		tokens = append(tokens, Token{Type: typ, Raw: s[i : i+n]})
		i += n
		textStart = i
	}
	flushText(len(s))

	return tokens
}

// scanEscape returns the length and type of the escape sequence at the start
// of s, which must begin with ESC.
func scanEscape(s string) (int, TokenType) {
	if len(s) < 2 { //nolint:mnd
		return len(s), TokenText
	}

	switch s[1] {
	case '[':
		return scanCSI(s)
	case ']':
		return scanOSC(s)
	}
	return 2, TokenText //nolint:mnd
}

func scanCSI(s string) (int, TokenType) {
	i := 2
	for i < len(s) && s[i] >= 0x30 && s[i] <= 0x3f {
		i++
	}
	paramsEnd := i
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	if i >= len(s) || s[i] < 0x40 || s[i] > 0x7e {
		if i < len(s) {
			// Malformed sequence: swallow the offending byte so it
			// doesn't leak out as visible text.
			i++
		}
		return i, TokenText
	}

	if s[i] != 'm' || paramsEnd != i {
		return i + 1, TokenText
	}

	params := s[2:paramsEnd]
	if isReset(params) {
		return i + 1, TokenReset
	}
	return i + 1, TokenSGR
}

func scanOSC(s string) (int, TokenType) {
	end, bodyEnd := len(s), len(s)
	for i := 2; i < len(s); i++ {
		if s[i] == bel {
			end, bodyEnd = i+1, i
			break
		}
		if s[i] == esc && i+1 < len(s) && s[i+1] == '\\' {
			end, bodyEnd = i+2, i
			break
		}
	}

	body := s[2:bodyEnd]
	if !strings.HasPrefix(body, "8;") {
		return end, TokenText
	}
	// OSC 8 ; params ; URI
	rest := body[2:]
	sep := strings.IndexByte(rest, ';')
	if sep < 0 {
		return end, TokenText
	}
	if rest[sep+1:] == "" {
		return end, TokenHyperlinkClose
	}
	return end, TokenHyperlinkOpen
}

// isReset reports whether the SGR parameter string resets attributes.
func isReset(params string) bool {
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

// trailingParams returns the SGR parameters of a reset sequence that follow
// its last zero parameter, i.e. the attributes it sets after resetting.
func trailingParams(raw string) string {
	params := strings.TrimSuffix(strings.TrimPrefix(raw, "\x1b["), "m")
	parts := strings.Split(params, ";")
	last := -1
	for i, p := range parts {
		if n, err := strconv.Atoi(p); err == nil && n == 0 {
			last = i
		}
	}
	return strings.Join(parts[last+1:], ";")
}

// StripANSI removes all escape sequences from s.
func StripANSI(s string) string {
	if !HasANSI(s) {
		return s
	}
	var b strings.Builder
	for _, t := range Tokenize(s) {
		b.WriteString(t.Text)
	}
	return b.String()
}

// ANSIWidth returns the visible width of s, ignoring escape sequences.
func ANSIWidth(s string) int {
	return uniseg.StringWidth(StripANSI(s))
}

// HasANSI reports whether s contains an escape sequence.
func HasANSI(s string) bool {
	return strings.IndexByte(s, esc) >= 0
}

// TruncateANSI truncates s to at most width visible cells. Escape sequences
// are never split and have zero width. If s is truncated, opts.Tail is
// appended (its width counts toward width), followed by closing any open
// hyperlink and a final SGR reset if styles are active.
func TruncateANSI(s string, width int, opts TruncateOptions) string {
	if width <= 0 {
		return ""
	}

	fits := ANSIWidth(s) <= width
	if fits && !opts.PreserveResets {
		return s
	}

	tokens := Tokenize(s)
	var enclosing string
	if opts.PreserveResets {
		enclosing = enclosingStyle(tokens)
	}

	if fits {
		return render(tokens, -1, enclosing, "")
	}

	tail := opts.Tail
	if tw := uniseg.StringWidth(tail); tw > width {
		tail = truncateText(tail, width)
	}
	return render(tokens, width-uniseg.StringWidth(tail), enclosing, tail)
}

// enclosingStyle returns the SGR sequences that precede the first visible
// text or reset in tokens.
func enclosingStyle(tokens []Token) string {
	var b strings.Builder
	for _, t := range tokens {
		switch t.Type {
		case TokenSGR:
			b.WriteString(t.Raw)
		case TokenReset:
			return b.String()
		case TokenText:
			if t.Text != "" {
				return b.String()
			}
		}
	}
	return b.String()
}

// render re-emits tokens, limited to avail visible cells (unlimited when
// avail is negative). When the limit is hit, tail is appended and any open
// hyperlink and active style are closed.
func render(tokens []Token, avail int, enclosing, tail string) string {
	var (
		b        strings.Builder
		cur      int
		active   bool
		linkOpen bool
		reopen   string
		pending  bool
	)

	flushReopen := func() {
		if !pending {
			return
		}
		pending = false
		b.WriteString(enclosing)
		if reopen != "" {
			b.WriteString("\x1b[" + reopen + "m")
			active = true
		}
		if enclosing != "" {
			active = true
		}
	}

	cut := false
loop:
	for _, t := range tokens {
		switch t.Type {
		case TokenReset:
			b.WriteString(t.Raw)
			reopen = trailingParams(t.Raw)
			active = reopen != ""
			pending = enclosing != ""
			continue
		case TokenText:
			if t.Text == "" {
				break
			}
			if avail < 0 {
				flushReopen()
				b.WriteString(t.Text)
				continue
			}
			rest := t.Text
			state := -1
			for rest != "" {
				var cluster string
				var w int
				cluster, rest, w, state = uniseg.FirstGraphemeClusterInString(rest, state)
				if cur+w > avail {
					cut = true
					break loop
				}
				flushReopen()
				b.WriteString(cluster)
				cur += w
			}
			continue
		}

		flushReopen()
		b.WriteString(t.Raw)
		switch t.Type {
		case TokenSGR:
			active = true
		case TokenHyperlinkOpen:
			linkOpen = true
		case TokenHyperlinkClose:
			linkOpen = false
		}
	}

	if !cut {
		return b.String()
	}

	if tail != "" {
		flushReopen()
		b.WriteString(tail)
	}
	if linkOpen {
		b.WriteString(linkCloseSeq)
	}
	if active {
		b.WriteString(resetSeq)
	}
	return b.String()
}

// truncateText truncates plain text to at most width cells.
func truncateText(s string, width int) string {
	var b strings.Builder
	cur := 0
	state := -1
	for s != "" {
		var cluster string
		var w int
		cluster, s, w, state = uniseg.FirstGraphemeClusterInString(s, state)
		if cur+w > width {
			break
		}
		b.WriteString(cluster)
		cur += w
	}
	return b.String()
}
