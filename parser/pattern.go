package parser

import (
	"strings"
)

// Pattern represents a destructuring pattern.
type Pattern interface {
	Node
	patternNode()
}

// IdentPattern represents an identifier binding with an optional default.
type IdentPattern struct {
	Name     string
	NamePos  Pos
	Default  Expr // nil if no default
}

func (p *IdentPattern) patternNode() {}

func (p *IdentPattern) Pos() Pos { return p.NamePos }

func (p *IdentPattern) End() Pos {
	if p.Default != nil {
		return p.Default.End()
	}
	return Pos(int(p.NamePos) + len(p.Name))
}

func (p *IdentPattern) String() string {
	if p.Default != nil {
		return p.Name + " = " + p.Default.String()
	}
	return p.Name
}

// RestPattern represents a rest element in an array pattern.
type RestPattern struct {
	EllipsisPos Pos
	Name        string
	NamePos     Pos
}

func (p *RestPattern) patternNode() {}

func (p *RestPattern) Pos() Pos { return p.EllipsisPos }

func (p *RestPattern) End() Pos {
	return Pos(int(p.NamePos) + len(p.Name))
}

func (p *RestPattern) String() string {
	return "..." + p.Name
}

// ArrayPattern represents an array destructuring pattern.
type ArrayPattern struct {
	Elements []Pattern
	LBrack   Pos
	RBrack   Pos
}

func (p *ArrayPattern) patternNode() {}

func (p *ArrayPattern) Pos() Pos { return p.LBrack }

func (p *ArrayPattern) End() Pos { return p.RBrack + 1 }

func (p *ArrayPattern) String() string {
	var elems []string
	for _, e := range p.Elements {
		elems = append(elems, e.String())
	}
	return "[" + strings.Join(elems, ", ") + "]"
}

// MapPatternElement represents one entry in a map destructuring pattern.
type MapPatternElement struct {
	Key     string
	KeyPos  Pos
	Pattern Pattern
}

func (e *MapPatternElement) String() string {
	if ip, ok := e.Pattern.(*IdentPattern); ok && ip.Name == e.Key && ip.Default == nil {
		return e.Key
	}
	return e.Key + ": " + e.Pattern.String()
}

// MapPattern represents a map destructuring pattern.
type MapPattern struct {
	Elements []*MapPatternElement
	LBrace   Pos
	RBrace   Pos
}

func (p *MapPattern) patternNode() {}

func (p *MapPattern) Pos() Pos { return p.LBrace }

func (p *MapPattern) End() Pos { return p.RBrace + 1 }

func (p *MapPattern) String() string {
	var elems []string
	for _, e := range p.Elements {
		elems = append(elems, e.String())
	}
	return "{" + strings.Join(elems, ", ") + "}"
}
