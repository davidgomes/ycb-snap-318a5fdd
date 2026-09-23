package termenv

import (
	"text/template"
)

// TemplateFuncs returns template helpers for the given output.
// The Output's preserve-resets default is applied to every helper.
func (o Output) TemplateFuncs() template.FuncMap {
	return templateFuncs(o.Profile, o.preserveResets)
}

// TemplateFuncs contains a few useful template helpers.
func TemplateFuncs(p Profile) template.FuncMap {
	return templateFuncs(p, false)
}

//nolint:mnd
func templateFuncs(p Profile, preserve bool) template.FuncMap {
	if p == Ascii {
		return noopTemplateFuncs
	}

	return template.FuncMap{
		"Color": func(values ...interface{}) string {
			s := p.String(values[len(values)-1].(string))
			switch len(values) {
			case 2:
				s = s.Foreground(p.Color(values[0].(string)))
			case 3:
				s = s.
					Foreground(p.Color(values[0].(string))).
					Background(p.Color(values[1].(string)))
			}

			return finishStyle(s, preserve)
		},
		"Foreground": func(values ...interface{}) string {
			s := p.String(values[len(values)-1].(string))
			if len(values) == 2 {
				s = s.Foreground(p.Color(values[0].(string)))
			}

			return finishStyle(s, preserve)
		},
		"Background": func(values ...interface{}) string {
			s := p.String(values[len(values)-1].(string))
			if len(values) == 2 {
				s = s.Background(p.Color(values[0].(string)))
			}

			return finishStyle(s, preserve)
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
		"truncate":  truncateNoTailTemplate(preserve),
	}
}

func finishStyle(s Style, preserve bool) string {
	if preserve {
		s = s.PreserveResets()
	}
	return s.String()
}

func styleFunc(p Profile, preserve bool, f func(Style) Style) func(...interface{}) string {
	return func(values ...interface{}) string {
		s := p.String(values[0].(string))
		return finishStyle(f(s), preserve)
	}
}

func truncateTemplate(preserve bool) func(int, string, string) string {
	return func(width int, tail, s string) string {
		return TruncateANSI(s, width, TruncateOptions{Tail: tail, PreserveResets: preserve})
	}
}

func truncateNoTailTemplate(preserve bool) func(int, string) string {
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
		return TruncateANSI(StripANSI(s), width, TruncateOptions{Tail: StripANSI(tail)})
	},
	"truncate": func(width int, s string) string {
		return TruncateANSI(StripANSI(s), width, TruncateOptions{})
	},
}

func noColorFunc(values ...interface{}) string {
	return values[len(values)-1].(string)
}

func noStyleFunc(values ...interface{}) string {
	return values[0].(string)
}
