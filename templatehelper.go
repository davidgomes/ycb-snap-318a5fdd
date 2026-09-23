package termenv

import (
	"text/template"
)

// TemplateFuncs returns template helpers for the given output.
// Helpers inherit the output's preserve-resets default.
func (o Output) TemplateFuncs() template.FuncMap {
	return templateFuncs(o.Profile, o.preserveResets)
}

// TemplateFuncs contains a few useful template helpers.
//
//nolint:mnd
func TemplateFuncs(p Profile) template.FuncMap {
	return templateFuncs(p, false)
}

func templateFuncs(p Profile, preserve bool) template.FuncMap {
	if p == Ascii {
		return noopTemplateFuncs
	}

	return template.FuncMap{
		"Color": func(values ...interface{}) string {
			s := newTemplateStyle(p, preserve, values[len(values)-1].(string))
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
			s := newTemplateStyle(p, preserve, values[len(values)-1].(string))
			if len(values) == 2 {
				s = s.Foreground(p.Color(values[0].(string)))
			}

			return s.String()
		},
		"Background": func(values ...interface{}) string {
			s := newTemplateStyle(p, preserve, values[len(values)-1].(string))
			if len(values) == 2 {
				s = s.Background(p.Color(values[0].(string)))
			}

			return s.String()
		},
		"Bold":      styleFunc(p, preserve, Style.Bold),
		"Faint":     styleFunc(p, preserve, Style.Faint),
		"Italic":    styleFunc(p, preserve, Style.Italic),
		"Underline": styleFunc(p, preserve, Style.Underline),
		"Overline":  styleFunc(p, preserve, Style.Overline),
		"Blink":     styleFunc(p, preserve, Style.Blink),
		"Reverse":   styleFunc(p, preserve, Style.Reverse),
		"CrossOut":  styleFunc(p, preserve, Style.CrossOut),
		"Truncate":  truncateTemplate(preserve),
		"truncate":  truncateTemplateNoTail(preserve),
	}
}

func newTemplateStyle(p Profile, preserve bool, text string) Style {
	s := p.String(text)
	if preserve {
		s = s.PreserveResets()
	}
	return s
}

func styleFunc(p Profile, preserve bool, f func(Style) Style) func(...interface{}) string {
	return func(values ...interface{}) string {
		s := newTemplateStyle(p, preserve, values[0].(string))
		return f(s).String()
	}
}

func truncateTemplate(preserve bool) func(int, string, string) string {
	return func(width int, tail, s string) string {
		return TruncateANSI(s, width, TruncateOptions{Tail: tail, PreserveResets: preserve})
	}
}

func truncateTemplateNoTail(preserve bool) func(int, string) string {
	return func(width int, s string) string {
		return TruncateANSI(s, width, TruncateOptions{PreserveResets: preserve})
	}
}

var noopTemplateFuncs = template.FuncMap{
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
		return truncatePlain(s, width, tail)
	},
	"truncate": func(width int, s string) string {
		return truncatePlain(s, width, "")
	},
}

func noColorFunc(values ...interface{}) string {
	return values[len(values)-1].(string)
}

func noStyleFunc(values ...interface{}) string {
	return values[0].(string)
}
