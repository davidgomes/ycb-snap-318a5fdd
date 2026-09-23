package parser

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// PatternKind is the shape of a destructuring pattern.
type PatternKind int

const (
	// PatternIdent binds one name.
	PatternIdent PatternKind = iota
	// PatternArray binds by position.
	PatternArray
	// PatternMap binds by key.
	PatternMap
)

// Pattern is a destructuring pattern. Map elements carry Key.
// Default is evaluated only when the matched position or key is absent.
type Pattern struct {
	Kind     PatternKind
	Name     *Ident
	Key      string
	KeyPos   Pos
	Rest     bool
	Default  Expr
	Elements []*Pattern
	LPos     Pos
	RPos     Pos
}

// Pos returns the position of first character belonging to the node.
func (e *Pattern) Pos() Pos {
	if e == nil {
		return NoPos
	}
	return e.LPos
}

// End returns the position of first character immediately after the node.
func (e *Pattern) End() Pos {
	if e == nil {
		return NoPos
	}
	if e.Default != nil {
		return e.Default.End()
	}
	if e.Kind == PatternIdent && e.Name != nil && e.RPos == 0 {
		return e.Name.End()
	}
	if e.RPos == 0 {
		return e.LPos
	}
	if e.Kind == PatternIdent {
		return e.RPos
	}
	return e.RPos + 1
}

func (e *Pattern) String() string {
	if e == nil {
		return nullRep
	}
	s := e.bodyString()
	if e.Default != nil {
		s += " = " + e.Default.String()
	}
	return s
}

func (e *Pattern) bodyString() string {
	switch e.Kind {
	case PatternIdent:
		s := ""
		if e.Rest {
			s = "..."
		}
		if e.Name != nil {
			s += e.Name.String()
		}
		return s
	case PatternArray:
		var els []string
		for _, el := range e.Elements {
			els = append(els, el.String())
		}
		return "[" + strings.Join(els, ", ") + "]"
	case PatternMap:
		var els []string
		for _, el := range e.Elements {
			els = append(els, el.mapElemString())
		}
		return "{" + strings.Join(els, ", ") + "}"
	default:
		return nullRep
	}
}

func (e *Pattern) mapElemString() string {
	body := e.String()
	if e.Key == "" {
		return body
	}
	if e.Kind == PatternIdent && !e.Rest && e.Name != nil && e.Name.Name == e.Key {
		return body
	}
	key := e.Key
	if !identKey(key) {
		key = strconv.Quote(key)
	}
	return key + ": " + body
}

func identKey(s string) bool {
	if s == "" {
		return false
	}
	r, size := utf8.DecodeRuneInString(s)
	if !unicode.IsLetter(r) && r != '_' {
		return false
	}
	for _, r := range s[size:] {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}
