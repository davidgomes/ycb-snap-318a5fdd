package termenv

import (
	"fmt"
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

func templateFuncs(p Profile, preserve bool) template.FuncMap {
	if p == Ascii {
		return noopTemplateFuncs
	}

	return template.FuncMap{
		"Color": func(values ...interface{}) string {
			s := p.String(values[len(values)-1].(string))
			if preserve {
				s = s.PreserveResets()
			}
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
			if preserve {
				s = s.PreserveResets()
			}
			if len(values) == 2 {
				s = s.Foreground(p.Color(values[0].(string)))
			}

			return s.String()
		},
		"Background": func(values ...interface{}) string {
			s := p.String(values[len(values)-1].(string))
			if preserve {
				s = s.PreserveResets()
			}
			if len(values) == 2 {
				s = s.Background(p.Color(values[0].(string)))
			}

			return s.String()
		},
		"Bold":      styleFuncWithPreserve(p, Style.Bold, preserve),
		"Faint":     styleFuncWithPreserve(p, Style.Faint, preserve),
		"Italic":    styleFuncWithPreserve(p, Style.Italic, preserve),
		"Underline": styleFuncWithPreserve(p, Style.Underline, preserve),
		"Overline":  styleFuncWithPreserve(p, Style.Overline, preserve),
		"Blink":     styleFuncWithPreserve(p, Style.Blink, preserve),
		"Reverse":   styleFuncWithPreserve(p, Style.Reverse, preserve),
		"CrossOut":  styleFuncWithPreserve(p, Style.CrossOut, preserve),
		"Truncate": func(width interface{}, tail, s string) string {
			return p.String(s).Truncate(toWidth(width), TruncateOptions{Tail: tail, PreserveResets: preserve})
		},
		"truncate": func(width interface{}, s string) string {
			return p.String(s).Truncate(toWidth(width), TruncateOptions{PreserveResets: preserve})
		},
	}
}

func styleFunc(p Profile, f func(Style) Style) func(...interface{}) string {
	return styleFuncWithPreserve(p, f, false)
}
func styleFuncWithPreserve(p Profile, f func(Style) Style, preserve bool) func(...interface{}) string {
	return func(values ...interface{}) string {
		s := p.String(values[0].(string))
		if preserve {
			s = s.PreserveResets()
		}
		return f(s).String()
	}
}

func toWidth(v interface{}) int { var n int; fmt.Sscanf(fmt.Sprint(v), "%d", &n); return n }

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
	"Truncate":   noColorFunc,
	"truncate":   noColorFunc,
}

func noColorFunc(values ...interface{}) string {
	return values[len(values)-1].(string)
}

func noStyleFunc(values ...interface{}) string {
	return values[0].(string)
}
