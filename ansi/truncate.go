package ansi

import (
	"strings"

	"github.com/rivo/uniseg"
)

const (
	hyperlinkClose = "\x1b]8;;\x1b\\"
)

// TruncateANSI truncates s to the given visible width without splitting escape
// sequences. Tail counts toward width and inherits the active style.
func TruncateANSI(s string, width int, opts TruncateOptions) string {
	return TruncateANSIWithEnclosing(s, width, opts, "")
}

// TruncateANSIWithEnclosing truncates s and re-applies enclosingSGR after resets
// when PreserveResets is enabled.
func TruncateANSIWithEnclosing(s string, width int, opts TruncateOptions, enclosingSGR string) string {
	return truncateANSI(s, width, opts, enclosingSGR)
}

func truncateANSI(s string, width int, opts TruncateOptions, enclosingSGR string) string {
	if width < 0 {
		width = 0
	}

	tailWidth := ANSIWidth(opts.Tail)
	if tailWidth > width {
		return styledTail(opts.Tail, nil, enclosingSGR)
	}

	contentWidth := width - tailWidth
	tokens := Tokenize(s)

	var (
		out           strings.Builder
		curWidth      int
		truncated     bool
		tailAdded     bool
		activeSGR     []string
		stylesActive  bool
		hyperlinkOpen int
	)

	writeActiveStyle := func() {
		if enclosingSGR != "" {
			out.WriteString("\x1b[")
			out.WriteString(enclosingSGR)
			out.WriteString("m")
			return
		}
		for _, seq := range activeSGR {
			out.WriteString(seq)
		}
	}

	applySGR := func(raw string) {
		activeSGR = append(activeSGR, raw)
		stylesActive = true
		out.WriteString(raw)
	}

	applyReset := func(raw string) {
		snapshot := append([]string(nil), activeSGR...)
		activeSGR = nil
		stylesActive = false
		out.WriteString(raw)

		if !opts.PreserveResets {
			return
		}

		if enclosingSGR != "" {
			out.WriteString("\x1b[")
			out.WriteString(enclosingSGR)
			out.WriteString("m")
			stylesActive = true
			return
		}

		for _, seq := range snapshot {
			out.WriteString(seq)
		}
		activeSGR = append([]string(nil), snapshot...)
		if len(activeSGR) > 0 {
			stylesActive = true
		}
	}

	for _, tok := range tokens {
		switch tok.Type {
		case TokenText:
			if truncated {
				continue
			}

			gr := uniseg.NewGraphemes(tok.Text)
			for gr.Next() {
				str := gr.Str()
				w := uniseg.StringWidth(str)
				if w == 0 {
					if !tailAdded {
						out.WriteString(str)
					}
					continue
				}

				if curWidth+w > contentWidth {
					if !tailAdded {
						writeActiveStyle()
						out.WriteString(opts.Tail)
						tailAdded = true
					}
					truncated = true
					continue
				}

				out.WriteString(str)
				curWidth += w
			}

		case TokenSGR:
			applySGR(tok.Raw)

		case TokenReset:
			applyReset(tok.Raw)

		case TokenHyperlinkOpen:
			hyperlinkOpen++
			out.WriteString(tok.Raw)

		case TokenHyperlinkClose:
			if hyperlinkOpen > 0 {
				hyperlinkOpen--
			}
			out.WriteString(tok.Raw)
		}
	}

	if stylesActive {
		out.WriteString("\x1b[0m")
	}

	for hyperlinkOpen > 0 {
		out.WriteString(hyperlinkClose)
		hyperlinkOpen--
	}

	return out.String()
}

func styledTail(tail string, activeSGR []string, enclosingSGR string) string {
	if tail == "" {
		return ""
	}

	var b strings.Builder
	if enclosingSGR != "" {
		b.WriteString("\x1b[")
		b.WriteString(enclosingSGR)
		b.WriteString("m")
	} else {
		for _, seq := range activeSGR {
			b.WriteString(seq)
		}
	}
	b.WriteString(tail)
	return b.String()
}
