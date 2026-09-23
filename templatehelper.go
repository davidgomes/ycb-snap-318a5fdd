package termenv

import (
	"text/template"
)

// TemplateFuncs returns template helpers for the given output.
// Helpers inherit the output profile and the preserve-resets default.
func (o Output) TemplateFuncs() template.FuncMap {
	return makeTemplateFuncs(o.Profile, o.String, func(s string, width int, tail string) string {
		return o.Truncate(s, width, TruncateOptions{Tail: tail})
	})
}

// TemplateFuncs contains a few useful template helpers.
func TemplateFuncs(p Profile) template.FuncMap {
	return makeTemplateFuncs(p, p.String, func(s string, width int, tail string) string {
		if p == Ascii {
			return TruncateANSI(StripANSI(s), width, TruncateOptions{Tail: StripANSI(tail)})
		}
		return TruncateANSI(s, width, TruncateOptions{Tail: tail})
	})
}

func makeTemplateFuncs(p Profile, newStyle func(...string) Style, truncate func(string, int, string) string) template.FuncMap {
	if p == Ascii {
		funcs := template.FuncMap{
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
		addTruncateFuncs(funcs, truncate)
		return funcs
	}

	funcs := template.FuncMap{
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
	addTruncateFuncs(funcs, truncate)
	return funcs
}

func addTruncateFuncs(funcs template.FuncMap, truncate func(string, int, string) string) {
	// Truncate(width, tail, string) and truncate(width, string).
	funcs["Truncate"] = func(width int, tail, s string) string {
		return truncate(s, width, tail)
	}
	funcs["truncate"] = func(width int, s string) string {
		return truncate(s, width, "")
	}
}

func styleFunc(newStyle func(...string) Style, f func(Style) Style) func(...interface{}) string {
	return func(values ...interface{}) string {
		s := newStyle(values[0].(string))
		return f(s).String()
	}
}

func noColorFunc(values ...interface{}) string {
	return values[len(values)-1].(string)
}

func noStyleFunc(values ...interface{}) string {
	return values[0].(string)
}
