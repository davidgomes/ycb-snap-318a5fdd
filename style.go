package termenv

import (
	"strings"

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
		if t.preserveResets {
			return preserveANSI(s)
		}
		return s
	}

	seq := strings.Join(t.styles, ";")
	if seq == "" {
		if t.preserveResets {
			return preserveANSI(s)
		}
		return s
	}

	open := CSI + seq + "m"
	close := CSI + ResetSeq + "m"
	if !t.preserveResets {
		return open + s + close
	}
	return preserveANSI(open + s + close)
}

// PreserveResets keeps this style in effect across reset sequences inside the
// text. After each reset run the style is opened again.
func (t Style) PreserveResets() Style {
	t.preserveResets = true
	return t
}

// Truncate shortens the styled text to width visible cells.
// Under the Ascii profile the result is plain text with no tail and no ANSI.
// Otherwise Tail counts toward width, inherits the active style, and reset
// sequences inside the text are preserved when this style or opts ask for it.
func (t Style) Truncate(width int, opts TruncateOptions) string {
	if t.profile == Ascii {
		return truncatePlain(t.string, width, "")
	}
	if t.preserveResets {
		opts.PreserveResets = true
	}
	body := t.string
	if seq := strings.Join(t.styles, ";"); seq != "" {
		body = CSI + seq + "m" + body + CSI + ResetSeq + "m"
	}
	return TruncateANSI(body, width, opts)
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
