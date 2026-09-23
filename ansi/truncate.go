package ansi

import (
	"strings"

	"github.com/rivo/uniseg"
)

const (
	// sgrReset clears graphic rendition.
	sgrReset = "\x1b[0m"
	// osc8Close ends an OSC 8 hyperlink.
	osc8Close = "\x1b]8;;\x1b\\"
)

// TruncateOptions controls ANSI-aware truncation.
type TruncateOptions struct {
	// Tail is appended when the string is shortened. It counts toward width
	// and is written while the active style is still in effect.
	Tail string
	// PreserveResets re-opens the enclosing SGR style after each reset run.
	PreserveResets bool
}

// TruncateANSI shortens s to width terminal columns.
//
// CSI and OSC sequences are copied whole and have zero width. When the text
// is shortened, an open OSC 8 hyperlink is closed and a final SGR reset is
// appended if a style is still active. With PreserveResets, each reset run
// that is followed by more input re-opens the SGR sequences active before it.
func TruncateANSI(s string, width int, opts TruncateOptions) string {
	if width < 0 {
		width = 0
	}
	visible := ANSIWidth(s)
	if visible <= width && !opts.PreserveResets {
		return s
	}
	if visible > width && width == 0 {
		return ""
	}

	tail := ""
	limit := width
	truncating := visible > width
	if truncating {
		tailWidth := ANSIWidth(opts.Tail)
		if tailWidth > width {
			return TruncateANSI(opts.Tail, width, TruncateOptions{})
		}
		tail = opts.Tail
		limit = width - tailWidth
	}

	st := truncState{limit: limit}
	st.b.Grow(len(s))
	tokens := Tokenize(s)
	for i := 0; i < len(tokens); i++ {
		if st.cut {
			break
		}
		tok := tokens[i]
		switch tok.Type {
		case TokenText:
			st.consumeText(tok.Text)
		case TokenReset:
			j := i
			for j < len(tokens) && tokens[j].Type == TokenReset {
				j++
			}
			st.emitResetRun(tokens[i:j], j < len(tokens), opts.PreserveResets)
			i = j - 1
		case TokenSGR:
			st.b.WriteString(tok.Raw)
			if strings.HasSuffix(tok.Raw, "m") {
				st.active = append(st.active, tok.Raw)
			}
		case TokenHyperlinkOpen:
			st.b.WriteString(tok.Raw)
			st.linkOpen = true
			st.linkClose = hyperlinkCloseFor(tok.Raw)
		case TokenHyperlinkClose:
			st.b.WriteString(tok.Raw)
			st.linkOpen = false
		}
	}

	// Closers are synthesized when truncation drops the original terminators,
	// and after a preserve-resets re-open that would otherwise leak style.
	if st.cut {
		st.b.WriteString(tail)
	}
	if st.cut && st.linkOpen {
		closer := st.linkClose
		if closer == "" {
			closer = osc8Close
		}
		st.b.WriteString(closer)
	}
	if len(st.active) > 0 && (st.cut || st.reopened) {
		st.b.WriteString(sgrReset)
	}
	return st.b.String()
}

type truncState struct {
	b         strings.Builder
	active    []string
	linkClose string
	linkOpen  bool
	used      int
	limit     int
	cut       bool
	reopened  bool
}

func (st *truncState) consumeText(text string) {
	state := -1
	for len(text) > 0 {
		var cluster string
		var w int
		cluster, text, w, state = uniseg.FirstGraphemeClusterInString(text, state)
		w = clusterWidth(cluster, w)
		if st.used+w > st.limit {
			st.cut = true
			return
		}
		st.b.WriteString(cluster)
		st.used += w
	}
}

func hyperlinkCloseFor(raw string) string {
	if strings.HasSuffix(raw, "\a") {
		return "\x1b]8;;\a"
	}
	return osc8Close
}

func (st *truncState) emitResetRun(run []Token, more bool, preserve bool) {
	for _, tok := range run {
		st.b.WriteString(tok.Raw)
	}
	if preserve && len(st.active) > 0 && more {
		for _, seq := range st.active {
			st.b.WriteString(seq)
		}
		st.reopened = true
		return
	}
	last := run[len(run)-1]
	st.active = nil
	if establishesStyle(last.Text) {
		st.active = append(st.active, last.Raw)
	}
}
