package parser

import "strings"

// ArrayPattern represents an array destructuring pattern.
type ArrayPattern struct {
	LBrack   Pos
	Elements []*PatternElement
	RBrack   Pos
}

func (e *ArrayPattern) exprNode() {}

// Pos returns the position of first character belonging to the node.
func (e *ArrayPattern) Pos() Pos {
	return e.LBrack
}

// End returns the position of first character immediately after the node.
func (e *ArrayPattern) End() Pos {
	return e.RBrack + 1
}

func (e *ArrayPattern) String() string {
	var elements []string
	for _, m := range e.Elements {
		elements = append(elements, m.String())
	}
	return "[" + strings.Join(elements, ", ") + "]"
}

// MapPattern represents a map destructuring pattern.
type MapPattern struct {
	LBrace   Pos
	Elements []*PatternElement
	RBrace   Pos
}

func (e *MapPattern) exprNode() {}

// Pos returns the position of first character belonging to the node.
func (e *MapPattern) Pos() Pos {
	return e.LBrace
}

// End returns the position of first character immediately after the node.
func (e *MapPattern) End() Pos {
	return e.RBrace + 1
}

func (e *MapPattern) String() string {
	var elements []string
	for _, m := range e.Elements {
		elements = append(elements, m.String())
	}
	return "{" + strings.Join(elements, ", ") + "}"
}

// PatternElement represents a single element of an array or map
// destructuring pattern. Key and KeyPos are only used in map patterns.
// Ellipsis is valid for rest elements. Default is nil if the element has no
// default value.
type PatternElement struct {
	Key       string
	KeyPos    Pos
	Ellipsis  Pos
	Target    Expr
	AssignPos Pos
	Default   Expr
}

// Pos returns the position of first character belonging to the node.
func (e *PatternElement) Pos() Pos {
	if e.KeyPos.IsValid() {
		return e.KeyPos
	}
	if e.Ellipsis.IsValid() {
		return e.Ellipsis
	}
	return e.Target.Pos()
}

// End returns the position of first character immediately after the node.
func (e *PatternElement) End() Pos {
	if e.Default != nil {
		return e.Default.End()
	}
	return e.Target.End()
}

func (e *PatternElement) String() string {
	s := e.Target.String()
	if e.Ellipsis.IsValid() {
		s = "..." + s
	}
	if e.KeyPos.IsValid() {
		s = e.Key + ": " + s
	}
	if e.Default != nil {
		s += " = " + e.Default.String()
	}
	return s
}

// coverRest is a "...x" element parsed inside an array or map literal. It is
// only valid once the literal is converted into a destructuring pattern.
type coverRest struct {
	Ellipsis Pos
	Target   Expr
}

func (e *coverRest) exprNode() {}

func (e *coverRest) Pos() Pos {
	return e.Ellipsis
}

func (e *coverRest) End() Pos {
	return e.Target.End()
}

func (e *coverRest) String() string {
	return "..." + e.Target.String()
}

// coverDefault is a "x = expr" element parsed inside an array or map literal.
// It is only valid once the literal is converted into a destructuring
// pattern.
type coverDefault struct {
	Target    Expr
	AssignPos Pos
	Default   Expr
}

func (e *coverDefault) exprNode() {}

func (e *coverDefault) Pos() Pos {
	return e.Target.Pos()
}

func (e *coverDefault) End() Pos {
	return e.Default.End()
}

func (e *coverDefault) String() string {
	return e.Target.String() + " = " + e.Default.String()
}
