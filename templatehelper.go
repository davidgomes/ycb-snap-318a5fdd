package termenv

import (
	"text/template"
)

// TemplateFuncs returns template helpers for the given output.
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

	o := Output{Profile: p, preserveResets: preserveResets}

	return template.FuncMap{
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
		"Truncate": func(width int, tail, s string) string {
			return o.Truncate(s, width, TruncateOptions{Tail: tail})
		},
		"truncate": func(width int, s string) string {
			return o.Truncate(s, width, TruncateOptions{})
		},
	}
}

func styleFunc(o Output, f func(Style) Style) func(...interface{}) string {
	return func(values ...interface{}) string {
		s := o.String(values[0].(string))
		return f(s).String()
	}
}

var asciiOutput = Output{Profile: Ascii}

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
		return asciiOutput.Truncate(s, width, TruncateOptions{Tail: tail})
	},
	"truncate": func(width int, s string) string {
		return asciiOutput.Truncate(s, width, TruncateOptions{})
	},
}

func noColorFunc(values ...interface{}) string {
	return values[len(values)-1].(string)
}

func noStyleFunc(values ...interface{}) string {
	return values[0].(string)
}
