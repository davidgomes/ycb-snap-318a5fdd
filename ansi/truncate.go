package ansi

import (
	"strings"

	"github.com/rivo/uniseg"
)

const (
	// sgrReset is appended when a truncated result still has an active style.
	sgrReset = "\x1b[0m"
)

// TruncateOptions controls ANSI-aware truncation.
type TruncateOptions struct {
	// Tail is appended when the string is cut. Its visible width counts
	// toward the limit, and it is written while the active style is still on.
	Tail string
	// PreserveResets re-opens the enclosing style after each reset run.
	PreserveResets bool
}

// TruncateANSI truncates s to width visible cells.
//
// CSI and OSC sequences are kept intact and have zero width. Wide runes
// occupy two cells and U+200B occupies zero; a cluster that does not fit is
// dropped rather than split. When the string is cut, Tail inherits the active
// style, an open OSC 8 hyperlink is closed, and a final SGR reset is appended
// if graphic rendition is still active. PreserveResets writes the style that
// was active before each reset run again after that run.
func TruncateANSI(s string, width int, opts TruncateOptions) string {
	if width <= 0 {
		return ""
	}

	tokens := Tokenize(s)
	total, realReset := measureTokens(tokens)
	if total <= width && !opts.PreserveResets {
		return s
	}
	if total <= width && !realReset {
		return s
	}

	truncating := total > width
	if truncating && ANSIWidth(opts.Tail) > width {
		return opts.Tail
	}

	tr := &truncator{
		opts:       opts,
		budget:     width,
		truncating: truncating,
		linkEnd:    "\x1b\\",
	}
	if truncating {
		tr.budget = width - ANSIWidth(opts.Tail)
	}
	tr.b.Grow(len(s) + len(opts.Tail) + 8)
	tr.walk(tokens)

	if tr.truncated {
		// The tail follows the cut, so a reset run that ended on the cut
		// still encloses the tail when preserve-resets is on.
		tr.endResetRun()
		tr.b.WriteString(opts.Tail)
		if tr.linkOpen {
			tr.b.WriteString("\x1b]8;;")
			tr.b.WriteString(tr.linkEnd)
		}
		if tr.needReset {
			tr.b.WriteString(sgrReset)
		}
		return tr.b.String()
	}

	if tr.reopened && tr.needReset {
		tr.b.WriteString(sgrReset)
	}
	return tr.b.String()
}

func measureTokens(tokens []Token) (width int, realReset bool) {
	for _, tok := range tokens {
		if tok.Text != "" {
			width += textWidth(tok.Text)
		}
		if tok.Type == TokenReset {
			realReset = true
		}
	}
	return width, realReset
}

type truncator struct {
	b          strings.Builder
	opts       TruncateOptions
	budget     int
	used       int
	truncating bool
	truncated  bool
	reopened   bool
	active     []string
	saved      []string
	inRun      bool
	needReset  bool
	linkOpen   bool
	linkEnd    string
}

func (tr *truncator) walk(tokens []Token) {
	for _, tok := range tokens {
		if tr.truncated {
			return
		}
		// Once the visible budget is filled, later sequences belong to the
		// truncated tail. A zero budget still keeps leading sequences so the
		// tail can inherit an opening style.
		if tr.truncating && tr.budget > 0 && tr.used >= tr.budget {
			tr.truncated = true
			return
		}
		switch tok.Type {
		case TokenReset:
			tr.beginReset(tok.Raw)
		case TokenSGR:
			tr.noteStyle(tok.Raw)
		case TokenHyperlinkOpen:
			tr.endResetRun()
			tr.linkOpen = true
			tr.linkEnd = oscTerminator(tok.Raw)
			tr.b.WriteString(tok.Raw)
		case TokenHyperlinkClose:
			tr.endResetRun()
			tr.linkOpen = false
			tr.b.WriteString(tok.Raw)
		default:
			tr.writeText(tok)
		}
	}
}

func (tr *truncator) noteStyle(raw string) {
	tr.endResetRun()
	tr.active = append(tr.active, raw)
	tr.needReset = true
	tr.b.WriteString(raw)
}

func (tr *truncator) beginReset(raw string) {
	if !tr.inRun {
		tr.saved = cloneStrings(tr.active)
		tr.inRun = true
	}
	params, _ := sgrParams(raw)
	// A pure reset clears graphic rendition. Any other sequence that merely
	// contains a 0 (for example 0;31 or a truecolor channel) still leaves a
	// style behind, and that sequence is what a later reset run re-opens.
	if isPureReset(params) {
		tr.active = nil
		tr.needReset = false
	} else {
		tr.active = []string{raw}
		tr.needReset = true
	}
	tr.b.WriteString(raw)
}

func (tr *truncator) endResetRun() {
	if !tr.inRun {
		return
	}
	tr.inRun = false
	if !tr.opts.PreserveResets || len(tr.saved) == 0 {
		return
	}
	for _, seq := range tr.saved {
		tr.b.WriteString(seq)
	}
	tr.active = cloneStrings(tr.saved)
	tr.needReset = true
	tr.reopened = true
}

func (tr *truncator) writeText(tok Token) {
	tr.endResetRun()
	if tok.Text == "" {
		if tok.Raw != "" {
			tr.b.WriteString(tok.Raw)
		}
		return
	}
	if !tr.truncating {
		tr.b.WriteString(tok.Text)
		return
	}

	rest := tok.Text
	state := -1
	for len(rest) > 0 {
		cluster, next, width, nextState := uniseg.FirstGraphemeClusterInString(rest, state)
		if cluster == "" {
			break
		}
		if width > 0 && tr.used+width > tr.budget {
			tr.truncated = true
			return
		}
		tr.b.WriteString(cluster)
		tr.used += width
		rest = next
		state = nextState
	}
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func oscTerminator(raw string) string {
	if strings.HasSuffix(raw, "\a") {
		return "\a"
	}
	return "\x1b\\"
}
