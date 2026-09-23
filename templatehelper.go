package termenv

import (
	"text/template"
)

// TemplateFuncs returns template helpers for the given output.
// Helpers inherit the output's PreserveResets default.
func (o Output) TemplateFuncs() template.FuncMap {
	fns := copyFuncMap(TemplateFuncs(o.Profile))
	if o.Profile == Ascii {
		fns["Truncate"] = func(width int, tail, s string) string {
			return o.Truncate(s, width, TruncateOptions{Tail: tail})
		}
		fns["truncate"] = func(width int, s string) string {
			return o.Truncate(s, width, TruncateOptions{})
		}
		return fns
	}

	color := func(values ...interface{}) string {
		s := o.String(values[len(values)-1].(string))
		switch len(values) {
		case 2:
			s = s.Foreground(o.Color(values[0].(string)))
		case 3:
			s = s.Foreground(o.Color(values[0].(string))).Background(o.Color(values[1].(string)))
		}
		return s.String()
	}
	fns["Color"] = color
	fns["Foreground"] = func(values ...interface{}) string {
		s := o.String(values[len(values)-1].(string))
		if len(values) == 2 {
			s = s.Foreground(o.Color(values[0].(string)))
		}
		return s.String()
	}
	fns["Background"] = func(values ...interface{}) string {
		s := o.String(values[len(values)-1].(string))
		if len(values) == 2 {
			s = s.Background(o.Color(values[0].(string)))
		}
		return s.String()
	}
	fns["Bold"] = outputStyleFunc(o, Style.Bold)
	fns["Faint"] = outputStyleFunc(o, Style.Faint)
	fns["Italic"] = outputStyleFunc(o, Style.Italic)
	fns["Underline"] = outputStyleFunc(o, Style.Underline)
	fns["Overline"] = outputStyleFunc(o, Style.Overline)
	fns["Blink"] = outputStyleFunc(o, Style.Blink)
	fns["Reverse"] = outputStyleFunc(o, Style.Reverse)
	fns["CrossOut"] = outputStyleFunc(o, Style.CrossOut)
	fns["Truncate"] = func(width int, tail, s string) string {
		return o.Truncate(s, width, TruncateOptions{Tail: tail})
	}
	fns["truncate"] = func(width int, s string) string {
		return o.Truncate(s, width, TruncateOptions{})
	}
	return fns
}

// TemplateFuncs contains a few useful template helpers.
//
//nolint:mnd
func TemplateFuncs(p Profile) template.FuncMap {
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

			return s.String()
		},
		"Foreground": func(values ...interface{}) string {
			s := p.String(values[len(values)-1].(string))
			if len(values) == 2 {
				s = s.Foreground(p.Color(values[0].(string)))
			}

			return s.String()
		},
		"Background": func(values ...interface{}) string {
			s := p.String(values[len(values)-1].(string))
			if len(values) == 2 {
				s = s.Background(p.Color(values[0].(string)))
			}

			return s.String()
		},
		"Bold":      styleFunc(p, Style.Bold),
		"Faint":     styleFunc(p, Style.Faint),
		"Italic":    styleFunc(p, Style.Italic),
		"Underline": styleFunc(p, Style.Underline),
		"Overline":  styleFunc(p, Style.Overline),
		"Blink":     styleFunc(p, Style.Blink),
		"Reverse":   styleFunc(p, Style.Reverse),
		"CrossOut":  styleFunc(p, Style.CrossOut),
		"Truncate": func(width int, tail, s string) string {
			if p == Ascii {
				return TruncateANSI(StripANSI(s), width, TruncateOptions{Tail: tail})
			}
			return TruncateANSI(s, width, TruncateOptions{Tail: tail})
		},
		"truncate": func(width int, s string) string {
			if p == Ascii {
				return TruncateANSI(StripANSI(s), width, TruncateOptions{})
			}
			return TruncateANSI(s, width, TruncateOptions{})
		},
	}
}

func styleFunc(p Profile, f func(Style) Style) func(...interface{}) string {
	return func(values ...interface{}) string {
		s := p.String(values[0].(string))
		return f(s).String()
	}
}

func outputStyleFunc(o Output, f func(Style) Style) func(...interface{}) string {
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
	"Truncate": func(width int, tail, s string) string {
		return TruncateANSI(StripANSI(s), width, TruncateOptions{Tail: tail})
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

func copyFuncMap(in template.FuncMap) template.FuncMap {
	out := make(template.FuncMap, len(in)+2)
	for k, v := range in {
		out[k] = v
	}
	return out
}
