package termenv

import (
	"text/template"

	"github.com/muesli/termenv/ansi"
)

// TemplateFuncs returns template helpers for the given output.
func (o Output) TemplateFuncs() template.FuncMap {
	return templateFuncs(o.Profile, o.preserveResets)
}

// TemplateFuncs contains a few useful template helpers.
//
//nolint:mnd
func TemplateFuncs(p Profile) template.FuncMap {
	return templateFuncs(p, false)
}

func templateFuncs(p Profile, preserveResets bool) template.FuncMap {
	if p == Ascii {
		return asciiTemplateFuncs
	}

	return template.FuncMap{
		"Color": func(values ...interface{}) string {
			s := p.String(values[len(values)-1].(string))
			s.preserveResets = preserveResets
			switch len(values) {
			case 2:
				s = s.Foreground(p.Color(values[0].(string)))
			case 3:
				s = s.
					Foreground(p.Color(values[0].(string))).
					Background(p.Color(values[1].(string)))
			}

			return s.String()
		},
		"Foreground": func(values ...interface{}) string {
			s := p.String(values[len(values)-1].(string))
			s.preserveResets = preserveResets
			if len(values) == 2 {
				s = s.Foreground(p.Color(values[0].(string)))
			}

			return s.String()
		},
		"Background": func(values ...interface{}) string {
			s := p.String(values[len(values)-1].(string))
			s.preserveResets = preserveResets
			if len(values) == 2 {
				s = s.Background(p.Color(values[0].(string)))
			}

			return s.String()
		},
		"Bold":      styleFunc(p, preserveResets, Style.Bold),
		"Faint":     styleFunc(p, preserveResets, Style.Faint),
		"Italic":    styleFunc(p, preserveResets, Style.Italic),
		"Underline": styleFunc(p, preserveResets, Style.Underline),
		"Overline":  styleFunc(p, preserveResets, Style.Overline),
		"Blink":     styleFunc(p, preserveResets, Style.Blink),
		"Reverse":   styleFunc(p, preserveResets, Style.Reverse),
		"CrossOut":  styleFunc(p, preserveResets, Style.CrossOut),
		"Truncate": func(width int, tail, s string) string {
			return p.String(s).Truncate(width, ansi.TruncateOptions{Tail: tail, PreserveResets: preserveResets})
		},
		"truncate": func(width int, s string) string {
			return p.String(s).Truncate(width, ansi.TruncateOptions{PreserveResets: preserveResets})
		},
	}
}

func styleFunc(p Profile, preserveResets bool, f func(Style) Style) func(...interface{}) string {
	return func(values ...interface{}) string {
		s := p.String(values[0].(string))
		s.preserveResets = preserveResets
		return f(s).String()
	}
}

var asciiTemplateFuncs = template.FuncMap{
	"Color":      noColorFunc,
	"Foreground": noColorFunc,
	"Background": noColorFunc,
	"Bold":       noStyleFunc,
	"Faint":      noStyleFunc,
	"Italic":     noStyleFunc,
	"Underline":  noStyleFunc,
	"Overline":   noStyleFunc,
	"Blink":      noStyleFunc,
	"Reverse":    noStyleFunc,
	"CrossOut":   noStyleFunc,
	"Truncate": func(width int, tail, s string) string {
		return ansi.TruncateANSI(ansi.StripANSI(s), width, ansi.TruncateOptions{Tail: ansi.StripANSI(tail)})
	},
	"truncate": func(width int, s string) string {
		return ansi.TruncateANSI(ansi.StripANSI(s), width, ansi.TruncateOptions{})
	},
}

func noColorFunc(values ...interface{}) string {
	return values[len(values)-1].(string)
}

func noStyleFunc(values ...interface{}) string {
	return values[0].(string)
}
