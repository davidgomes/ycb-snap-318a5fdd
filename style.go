package termenv

import (
	"strings"

	"github.com/muesli/termenv/ansi"
	"github.com/rivo/uniseg"
)

// Sequence definitions.
const (
	ResetSeq     = "0"
	BoldSeq      = "1"
	FaintSeq     = "2"
	ItalicSeq    = "3"
	UnderlineSeq = "4"
	BlinkSeq     = "5"
	ReverseSeq   = "7"
	CrossOutSeq  = "9"
	OverlineSeq  = "53"
)

// Style is a string that various rendering styles can be applied to.
type Style struct {
	profile Profile
	string
	styles         []string
	preserveResets bool
}

// String returns a new Style.
func String(s ...string) Style {
	return Style{
		profile: ANSI,
		string:  strings.Join(s, " "),
	}
}

func (t Style) String() string {
	return t.Styled(t.string)
}

// Styled renders s with all applied styles.
func (t Style) Styled(s string) string {
	if t.profile == Ascii {
		return s
	}
	if len(t.styles) == 0 {
		return s
	}

	seq := strings.Join(t.styles, ";")
	if seq == "" {
		return s
	}

	open := CSI + seq + "m"
	if t.preserveResets {
		s = reopenAfterResets(s, open)
	}
	return open + s + CSI + ResetSeq + "m"
}

// PreserveResets re-opens this style after every reset run in the text.
func (t Style) PreserveResets() Style {
	t.preserveResets = true
	return t
}

// Truncate shortens the styled string to width visible columns.
// Under the Ascii profile the result is plain text and opts.Tail is ignored.
func (t Style) Truncate(width int, opts TruncateOptions) string {
	if t.profile == Ascii {
		return TruncateANSI(StripANSI(t.string), width, TruncateOptions{})
	}
	if opts.PreserveResets {
		t.preserveResets = true
	}
	if len(t.styles) == 0 {
		return TruncateANSI(t.string, width, opts)
	}
	opts.PreserveResets = false
	return TruncateANSI(t.String(), width, opts)
}

// reopenAfterResets inserts open after each reset run that is followed by
// more input, so the enclosing style continues.
func reopenAfterResets(s, open string) string {
	if open == "" || !ansi.HasANSI(s) {
		return s
	}
	tokens := ansi.Tokenize(s)
	var b strings.Builder
	b.Grow(len(s) + len(open))
	for i := 0; i < len(tokens); {
		if tokens[i].Type != ansi.TokenReset {
			b.WriteString(tokens[i].Raw)
			i++
			continue
		}
		for i < len(tokens) && tokens[i].Type == ansi.TokenReset {
			b.WriteString(tokens[i].Raw)
			i++
		}
		if i < len(tokens) {
			b.WriteString(open)
		}
	}
	return b.String()
}

// Foreground sets a foreground color.
func (t Style) Foreground(c Color) Style {
	if c != nil {
		t.styles = append(t.styles, c.Sequence(false))
	}
	return t
}

// Background sets a background color.
func (t Style) Background(c Color) Style {
	if c != nil {
		t.styles = append(t.styles, c.Sequence(true))
	}
	return t
}

// Bold enables bold rendering.
func (t Style) Bold() Style {
	t.styles = append(t.styles, BoldSeq)
	return t
}

// Faint enables faint rendering.
func (t Style) Faint() Style {
	t.styles = append(t.styles, FaintSeq)
	return t
}

// Italic enables italic rendering.
func (t Style) Italic() Style {
	t.styles = append(t.styles, ItalicSeq)
	return t
}

// Underline enables underline rendering.
func (t Style) Underline() Style {
	t.styles = append(t.styles, UnderlineSeq)
	return t
}

// Overline enables overline rendering.
func (t Style) Overline() Style {
	t.styles = append(t.styles, OverlineSeq)
	return t
}

// Blink enables blink mode.
func (t Style) Blink() Style {
	t.styles = append(t.styles, BlinkSeq)
	return t
}

// Reverse enables reverse color mode.
func (t Style) Reverse() Style {
	t.styles = append(t.styles, ReverseSeq)
	return t
}

// CrossOut enables crossed-out rendering.
func (t Style) CrossOut() Style {
	t.styles = append(t.styles, CrossOutSeq)
	return t
}

// Width returns the width required to print all runes in Style.
func (t Style) Width() int {
	return uniseg.StringWidth(t.string)
}
