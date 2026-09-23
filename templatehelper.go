package termenv

import (
	"text/template"
)

// TemplateFuncs returns template helpers for the given output. The helpers
// inherit the Output's preserve-resets default.
func (o Output) TemplateFuncs() template.FuncMap {
	return templateFuncs(o.Profile, o.preserveResets)
}

// TemplateFuncs contains a few useful template helpers.
func TemplateFuncs(p Profile) template.FuncMap {
	return templateFuncs(p, false)
}

//nolint:mnd
func templateFuncs(p Profile, preserveResets bool) template.FuncMap {
	if p == Ascii {
		return noopTemplateFuncs
	}

	str := func(s string) Style {
		st := p.String(s)
		st.preserveResets = preserveResets
		return st
	}

	return template.FuncMap{
		"Color": func(values ...interface{}) string {
			s := str(values[len(values)-1].(string))
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
			s := str(values[len(values)-1].(string))
			if len(values) == 2 {
				s = s.Foreground(p.Color(values[0].(string)))
			}

			return s.String()
		},
		"Background": func(values ...interface{}) string {
			s := str(values[len(values)-1].(string))
			if len(values) == 2 {
				s = s.Background(p.Color(values[0].(string)))
			}

			return s.String()
		},
		"Bold":      styleFunc(str, Style.Bold),
		"Faint":     styleFunc(str, Style.Faint),
		"Italic":    styleFunc(str, Style.Italic),
		"Underline": styleFunc(str, Style.Underline),
		"Overline":  styleFunc(str, Style.Overline),
		"Blink":     styleFunc(str, Style.Blink),
		"Reverse":   styleFunc(str, Style.Reverse),
		"CrossOut":  styleFunc(str, Style.CrossOut),
		"Truncate":  truncateFunc(p, preserveResets),
		"truncate":  truncateNoTailFunc(p, preserveResets),
	}
}

func styleFunc(str func(string) Style, f func(Style) Style) func(...interface{}) string {
	return func(values ...interface{}) string {
		s := str(values[0].(string))
		return f(s).String()
	}
}

func truncateFunc(p Profile, preserveResets bool) func(int, string, string) string {
	return func(width int, tail, s string) string {
		return truncate(p, preserveResets, s, width, TruncateOptions{Tail: tail})
	}
}

func truncateNoTailFunc(p Profile, preserveResets bool) func(int, string) string {
	return func(width int, s string) string {
		return truncate(p, preserveResets, s, width, TruncateOptions{})
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
	"Truncate":   truncateFunc(Ascii, false),
	"truncate":   truncateNoTailFunc(Ascii, false),
}

func noColorFunc(values ...interface{}) string {
	return values[len(values)-1].(string)
}

func noStyleFunc(values ...interface{}) string {
	return values[0].(string)
}
