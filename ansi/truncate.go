package ansi

import (
	"strings"

	"github.com/rivo/uniseg"
)

const (
	sgrReset       = "\x1b[0m"
	hyperlinkClose = "\x1b]8;;\x1b\\"
)

// TruncateOptions configures TruncateANSI.
type TruncateOptions struct {
	// Tail is appended when the string gets truncated. Its width counts
	// toward the limit, and it inherits the style active at the cut.
	Tail string

	// PreserveResets re-opens the enclosing style after each run of resets,
	// so text that follows an inner reset keeps the enclosing style. The
	// enclosing style is the first SGR sequence of the string, provided it
	// precedes all text.
	PreserveResets bool
}

// TruncateANSI shortens s to at most width terminal cells, treating escape
// sequences as zero-width and never splitting them or a grapheme cluster.
//
// If s fits and PreserveResets is not set, s is returned unchanged. Otherwise
// the result ends with an SGR reset if any style is still active, and closes
// any OSC 8 hyperlink left open.
func TruncateANSI(s string, width int, opts TruncateOptions) string {
	if width <= 0 {
		return ""
	}

	toks := Tokenize(s)
	var total int
	for _, t := range toks {
		total += uniseg.StringWidth(t.Text)
	}
	truncated := total > width
	if !truncated && !opts.PreserveResets {
		return s
	}

	var tail string
	avail := width
	if truncated {
		tail = opts.Tail
		if ANSIWidth(tail) > width {
			tail = TruncateANSI(tail, width, TruncateOptions{})
		}
		avail -= ANSIWidth(tail)
	}

	cutTok, cutOff, lastText := cut(toks, avail)
	enclosing, enclosingIdx := "", -1
	if opts.PreserveResets {
		enclosing, enclosingIdx = enclosingStyle(toks)
	}

	var b strings.Builder
	b.Grow(len(s) + len(tail) + len(sgrReset) + len(hyperlinkClose))
	var st state
	for i := 0; i < cutTok; i++ {
		t := toks[i]
		b.WriteString(t.Raw)
		st.apply(t)

		if t.Type != TokenReset || enclosing == "" || i < enclosingIdx {
			continue
		}
		var next Token
		if i+1 < len(toks) {
			next = toks[i+1]
		}
		if next.Type == TokenReset {
			continue // the run of resets goes on
		}
		if lastText <= i && tail == "" {
			continue // no text left to style
		}
		trailing := sgrTrailing(t.Raw)
		if trailing == "" && next.Type == TokenSGR && next.Raw == enclosing {
			continue // already re-opened
		}
		b.WriteString(enclosing)
		if trailing != "" {
			// Re-apply what the reset itself set, which the enclosing style
			// would otherwise override.
			b.WriteString("\x1b[" + trailing + "m")
		}
		st.styled = true
	}
	if cutTok < len(toks) {
		b.WriteString(toks[cutTok].Text[:cutOff])
	}

	b.WriteString(tail)
	for _, t := range Tokenize(tail) {
		st.apply(t)
	}
	if st.styled {
		b.WriteString(sgrReset)
	}
	if st.linked {
		b.WriteString(hyperlinkClose)
	}
	return b.String()
}

// state tracks what a terminal would have open after printing tokens.
type state struct {
	styled bool
	linked bool
}

func (s *state) apply(t Token) {
	switch t.Type {
	case TokenSGR:
		s.styled = true
	case TokenReset:
		s.styled = sgrTrailing(t.Raw) != ""
	case TokenHyperlinkOpen:
		s.linked = true
	case TokenHyperlinkClose:
		s.linked = false
	case TokenText:
	}
}

// cut finds the first grapheme cluster that no longer fits into avail cells.
// It returns the index of its token and its byte offset within that token's
// Text, or len(toks) if everything fits. last is the index of the last token
// that keeps any text, or -1.
func cut(toks []Token, avail int) (idx, off, last int) {
	var used int
	last = -1
	for i, t := range toks {
		rest, gs := t.Text, -1
		for len(rest) > 0 {
			_, next, w, ngs := uniseg.FirstGraphemeClusterInString(rest, gs)
			if used+w > avail {
				return i, len(t.Text) - len(rest), last
			}
			used += w
			rest, gs = next, ngs
			last = i
		}
	}
	return len(toks), 0, last
}

// enclosingStyle returns the first SGR sequence that precedes all text, and
// its token index.
func enclosingStyle(toks []Token) (string, int) {
	for i, t := range toks {
		if t.Text != "" {
			break
		}
		if t.Type == TokenSGR {
			return t.Raw, i
		}
	}
	return "", -1
}
