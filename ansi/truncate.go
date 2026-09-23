package ansi

import (
	"strings"

	"github.com/rivo/uniseg"
)

// TruncateOptions configures TruncateANSI.
type TruncateOptions struct {
	// Tail is appended when s is truncated. Its width counts toward the
	// target width and it inherits the style active at the cut point.
	Tail string
	// PreserveResets re-opens the enclosing style (the SGR sequences that
	// precede the first visible text) after each run of resets that is
	// followed by more content.
	PreserveResets bool
}

// TruncateANSI truncates s to at most width terminal cells. Escape sequences
// have zero width and are never split. When s is truncated, open styles are
// closed with a final SGR reset and open OSC 8 hyperlinks are closed. If the
// tail alone is wider than width, an empty string is returned.
func TruncateANSI(s string, width int, opts TruncateOptions) string {
	truncate := ANSIWidth(s) > width
	if !truncate && !opts.PreserveResets {
		return s
	}

	budget := width
	if truncate {
		budget -= ANSIWidth(opts.Tail)
		if width <= 0 || budget < 0 {
			return ""
		}
	}

	tokens := Tokenize(s)
	var enclosing string
	if opts.PreserveResets {
		enclosing = enclosingStyle(tokens)
	}

	var (
		b         strings.Builder
		used      int
		cut       bool
		reopened  bool
		active    bool
		activeAt  = -1
		link      string
		linkAt    = -1
		setActive = func(i int) {
			if !active {
				activeAt = i
			}
			active = true
		}
	)
	b.Grow(len(s) + len(opts.Tail) + len(ResetSeq))

	for i := 0; i < len(tokens) && !cut; i++ {
		t := tokens[i]
		switch t.Type {
		case TokenText:
			if !truncate || t.Text == "" {
				b.WriteString(t.Raw)
				continue
			}
			rest, state := t.Text, -1
			for len(rest) > 0 {
				cluster, r, w, ns := uniseg.FirstGraphemeClusterInString(rest, state)
				if used+w > budget {
					cut = true
					break
				}
				b.WriteString(cluster)
				used += w
				rest, state = r, ns
				// More visible content is known to follow, so stop as soon as
				// the budget is exhausted rather than emitting trailing
				// escapes that would style nothing.
				if used == budget {
					cut = true
					break
				}
			}
		case TokenSGR:
			b.WriteString(t.Raw)
			setActive(i)
		case TokenReset:
			b.WriteString(t.Raw)
			active = false
			if resetLeavesStyle(t.Raw) {
				setActive(i)
			}
			if enclosing != "" && endsResetRun(tokens, i) && !startsWith(tokens[i+1:], enclosing) {
				b.WriteString(enclosing)
				setActive(i)
				reopened = true
			}
		case TokenHyperlinkOpen:
			b.WriteString(t.Raw)
			link, linkAt = t.Raw, i
		case TokenHyperlinkClose:
			b.WriteString(t.Raw)
			link, linkAt = "", -1
		}
	}

	if !truncate {
		if reopened && active {
			b.WriteString(ResetSeq)
		}
		return b.String()
	}

	b.WriteString(opts.Tail)
	closeLink := func() {
		if link != "" {
			b.WriteString(hyperlinkClose(link))
		}
	}
	closeStyle := func() {
		if active {
			b.WriteString(ResetSeq)
		}
	}
	if linkAt > activeAt {
		closeLink()
		closeStyle()
	} else {
		closeStyle()
		closeLink()
	}

	return b.String()
}

// enclosingStyle returns the SGR sequences preceding the first visible text
// or reset in tokens.
func enclosingStyle(tokens []Token) string {
	var b strings.Builder
	for _, t := range tokens {
		switch {
		case t.Type == TokenSGR:
			b.WriteString(t.Raw)
		case t.Type == TokenReset, t.Type == TokenText && t.Text != "":
			return b.String()
		}
	}
	return b.String()
}

// endsResetRun reports whether tokens[i] is the last reset of a run that is
// followed by more tokens.
func endsResetRun(tokens []Token, i int) bool {
	return i+1 < len(tokens) && tokens[i+1].Type != TokenReset
}

// startsWith reports whether the SGR tokens at the start of tokens begin with
// seq.
func startsWith(tokens []Token, seq string) bool {
	var b strings.Builder
	for _, t := range tokens {
		if t.Type != TokenSGR {
			break
		}
		b.WriteString(t.Raw)
		if b.Len() >= len(seq) {
			break
		}
	}
	return strings.HasPrefix(b.String(), seq)
}
