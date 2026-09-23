package termenv

import (
	"text/template"
)

// TemplateFuncs returns template helpers for the given output.
func (o Output) TemplateFuncs() template.FuncMap {
	return templateFuncs(&o)
}

// TemplateFuncs contains a few useful template helpers.
func TemplateFuncs(p Profile) template.FuncMap {
	return templateFuncs(&Output{Profile: p})
}

//nolint:mnd
func templateFuncs(o *Output) template.FuncMap {
	p := o.Profile
	truncateFuncs := template.FuncMap{
		"Truncate": func(width int, tail string, s string) string {
			return o.Truncate(s, width, TruncateOptions{Tail: tail})
		},
		"truncate": func(width int, s string) string {
			return o.Truncate(s, width, TruncateOptions{})
		},
	}

	if p == Ascii {
		funcs := template.FuncMap{}
		for k, v := range noopTemplateFuncs {
			funcs[k] = v
		}
		for k, v := range truncateFuncs {
			funcs[k] = v
		}
		return funcs
	}

	funcs := template.FuncMap{
		"Color": func(values ...interface{}) string {
			s := o.String(values[len(values)-1].(string))
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
			s := o.String(values[len(values)-1].(string))
			if len(values) == 2 {
				s = s.Foreground(p.Color(values[0].(string)))
			}

			return s.String()
		},
		"Background": func(values ...interface{}) string {
			s := o.String(values[len(values)-1].(string))
			if len(values) == 2 {
				s = s.Background(p.Color(values[0].(string)))
			}

			return s.String()
		},
		"Bold":      styleFunc(o, Style.Bold),
		"Faint":     styleFunc(o, Style.Faint),
		"Italic":    styleFunc(o, Style.Italic),
		"Underline": styleFunc(o, Style.Underline),
		"Overline":  styleFunc(o, Style.Overline),
		"Blink":     styleFunc(o, Style.Blink),
		"Reverse":   styleFunc(o, Style.Reverse),
		"CrossOut":  styleFunc(o, Style.CrossOut),
	}
	for k, v := range truncateFuncs {
		funcs[k] = v
	}
	return funcs
}

func styleFunc(o *Output, f func(Style) Style) func(...interface{}) string {
	return func(values ...interface{}) string {
		s := o.String(values[0].(string))
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
