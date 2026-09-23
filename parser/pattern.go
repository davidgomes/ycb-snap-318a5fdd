package parser

import (
	"strconv"
	"strings"
	"unicode"
)

// BindingPattern is the target of a destructuring binding.
// Patterns appear on the left of := and as function parameters.
type BindingPattern interface {
	Node
	bindingPattern()
}

// BindingElem is one element of an array destructuring pattern.
type BindingElem struct {
	Pattern BindingPattern
	Default Expr
	Rest    bool
}

// ArrayBinding is an array destructuring pattern: [a, b = 1, ...rest].
type ArrayBinding struct {
	Elements []*BindingElem
	LBrack   Pos
	RBrack   Pos
}

func (e *ArrayBinding) bindingPattern() {}

// Pos returns the position of first character belonging to the node.
func (e *ArrayBinding) Pos() Pos {
	return e.LBrack
}

// End returns the position of first character immediately after the node.
func (e *ArrayBinding) End() Pos {
	return e.RBrack + 1
}

// String returns a string representation of the pattern.
func (e *ArrayBinding) String() string {
	parts := make([]string, 0, len(e.Elements))
	for _, el := range e.Elements {
		parts = append(parts, el.String())
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// String returns a string representation of the element.
func (e *BindingElem) String() string {
	var b strings.Builder
	if e.Rest {
		b.WriteString("...")
	}
	if e.Pattern != nil {
		b.WriteString(e.Pattern.String())
	}
	if e.Default != nil {
		b.WriteString(" = ")
		b.WriteString(e.Default.String())
	}
	return b.String()
}

// MapBindingElem is one element of a map destructuring pattern.
type MapBindingElem struct {
	Key     string
	KeyPos  Pos
	Colon   Pos
	Pattern BindingPattern
	Default Expr
	Rest    bool
}

// MapBinding is a map destructuring pattern: {x}, {x: a}, {x: a = 50}.
type MapBinding struct {
	Elements []*MapBindingElem
	LBrace   Pos
	RBrace   Pos
}

func (e *MapBinding) bindingPattern() {}

// Pos returns the position of first character belonging to the node.
func (e *MapBinding) Pos() Pos {
	return e.LBrace
}

// End returns the position of first character immediately after the node.
func (e *MapBinding) End() Pos {
	return e.RBrace + 1
}

// String returns a string representation of the pattern.
func (e *MapBinding) String() string {
	parts := make([]string, 0, len(e.Elements))
	for _, el := range e.Elements {
		parts = append(parts, el.String())
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// String returns a string representation of the element.
func (e *MapBindingElem) String() string {
	if e.Rest {
		if e.Pattern != nil {
			return "..." + e.Pattern.String()
		}
		return "..."
	}
	s := formatMapKey(e.Key)
	if e.Colon.IsValid() {
		s += ": "
		if e.Pattern != nil {
			s += e.Pattern.String()
		}
	}
	if e.Default != nil {
		s += " = " + e.Default.String()
	}
	return s
}

func formatMapKey(key string) string {
	if key == "" || !isIdentKey(key) {
		return strconv.Quote(key)
	}
	return key
}

func isIdentKey(key string) bool {
	for i, r := range key {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' {
				return false
			}
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}

func (e *Ident) bindingPattern() {}
