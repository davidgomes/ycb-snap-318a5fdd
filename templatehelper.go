package termenv

import (
	"text/template"
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

//nolint:mnd
func templateFuncs(p Profile, preserve bool) template.FuncMap {
	truncFuncs := template.FuncMap{
		"Truncate": func(width int, tail, s string) string {
			return truncateFor(p, preserve, s, width, TruncateOptions{Tail: tail})
		},
		"truncate": func(width int, s string) string {
			return truncateFor(p, preserve, s, width, TruncateOptions{})
		},
	}
	if p == Ascii {
		fm := template.FuncMap{}
		for k, v := range noopTemplateFuncs {
			fm[k] = v
		}
		for k, v := range truncFuncs {
			fm[k] = v
		}
		return fm
	}

	newStyle := func(s string) Style {
		st := p.String(s)
		st.preserveResets = preserve
		return st
	}

	fm := template.FuncMap{
		"Color": func(values ...interface{}) string {
			s := newStyle(values[len(values)-1].(string))
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
			s := newStyle(values[len(values)-1].(string))
			if len(values) == 2 {
				s = s.Foreground(p.Color(values[0].(string)))
			}

			return s.String()
		},
		"Background": func(values ...interface{}) string {
			s := newStyle(values[len(values)-1].(string))
			if len(values) == 2 {
				s = s.Background(p.Color(values[0].(string)))
			}

			return s.String()
		},
		"Bold":      styleFunc(newStyle, Style.Bold),
		"Faint":     styleFunc(newStyle, Style.Faint),
		"Italic":    styleFunc(newStyle, Style.Italic),
		"Underline": styleFunc(newStyle, Style.Underline),
		"Overline":  styleFunc(newStyle, Style.Overline),
		"Blink":     styleFunc(newStyle, Style.Blink),
		"Reverse":   styleFunc(newStyle, Style.Reverse),
		"CrossOut":  styleFunc(newStyle, Style.CrossOut),
	}
	for k, v := range truncFuncs {
		fm[k] = v
	}
	return fm
}

func styleFunc(newStyle func(string) Style, f func(Style) Style) func(...interface{}) string {
	return func(values ...interface{}) string {
		s := newStyle(values[0].(string))
		return f(s).String()
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
}

func noColorFunc(values ...interface{}) string {
	return values[len(values)-1].(string)
}

func noStyleFunc(values ...interface{}) string {
	return values[0].(string)
}
