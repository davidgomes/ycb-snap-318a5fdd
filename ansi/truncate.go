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
		full      bool
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

loop:
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		// Once the budget is exhausted, only sequences that close what was
		// already emitted are kept; anything that would open new styles or
		// print is cut.
		if full && t.Type != TokenReset && t.Type != TokenHyperlinkClose {
			break
		}
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
					break loop
				}
				b.WriteString(cluster)
				used += w
				rest, state = r, ns
				if used == budget {
					full = true
					if len(rest) > 0 {
						break loop
					}
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
			if enclosing != "" && endsResetRun(tokens, i) {
				// Reuse a re-open already present in the input instead of
				// duplicating it.
				n := matchingSGRTokens(tokens[i+1:], enclosing)
				b.WriteString(enclosing)
				setActive(i)
				reopened = reopened || n == 0
				i += n
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

// matchingSGRTokens returns the number of leading SGR tokens whose
// concatenation is exactly seq, or 0 if there is no such prefix.
func matchingSGRTokens(tokens []Token, seq string) int {
	var b strings.Builder
	for i, t := range tokens {
		if t.Type != TokenSGR {
			return 0
		}
		b.WriteString(t.Raw)
		switch {
		case b.String() == seq:
			return i + 1
		case !strings.HasPrefix(seq, b.String()):
			return 0
		}
	}
	return 0
}
