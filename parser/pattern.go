package parser

import (
	"strings"

	"github.com/d5/tengo/v2/token"
)

// Pattern represents a destructuring pattern.
type Pattern interface {
	Node
	patternNode()
}

// IdentPattern represents an identifier pattern with an optional default.
type IdentPattern struct {
	Name     string
	NamePos  Pos
	Default  Expr
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

// ArrayPattern represents an array destructuring pattern.
type ArrayPattern struct {
	LBrack   Pos
	Elements []Pattern
	Rest     *IdentPattern
	RBrack   Pos
}

func (p *ArrayPattern) patternNode() {}

func (p *ArrayPattern) Pos() Pos { return p.LBrack }

func (p *ArrayPattern) End() Pos { return p.RBrack + 1 }

func (p *ArrayPattern) String() string {
	var elements []string
	for _, e := range p.Elements {
		elements = append(elements, e.String())
	}
	s := "[" + strings.Join(elements, ", ")
	if p.Rest != nil {
		if len(elements) > 0 {
			s += ", "
		}
		s += "..." + p.Rest.String()
	}
	return s + "]"
}

// MapPatternElement represents one entry in a map destructuring pattern.
type MapPatternElement struct {
	Key     string
	KeyPos  Pos
	Name    string
	NamePos Pos
	Default Expr
	Nested  Pattern
}

func (e *MapPatternElement) Pos() Pos { return e.KeyPos }

func (e *MapPatternElement) End() Pos {
	if e.Nested != nil {
		return e.Nested.End()
	}
	if e.Default != nil {
		return e.Default.End()
	}
	if e.NamePos.IsValid() {
		return Pos(int(e.NamePos) + len(e.Name))
	}
	return Pos(int(e.KeyPos) + len(e.Key))
}

func (e *MapPatternElement) String() string {
	if e.Nested != nil {
		return e.Key + ": " + e.Nested.String()
	}
	if e.Name != "" && e.Name != e.Key {
		s := e.Key + ": " + e.Name
		if e.Default != nil {
			s += " = " + e.Default.String()
		}
		return s
	}
	if e.Default != nil {
		return e.Key + " = " + e.Default.String()
	}
	return e.Key
}

// MapPattern represents a map destructuring pattern.
type MapPattern struct {
	LBrace   Pos
	Elements []*MapPatternElement
	RBrace   Pos
}

func (p *MapPattern) patternNode() {}

func (p *MapPattern) Pos() Pos { return p.LBrace }

func (p *MapPattern) End() Pos { return p.RBrace + 1 }

func (p *MapPattern) String() string {
	var elements []string
	for _, e := range p.Elements {
		elements = append(elements, e.String())
	}
	return "{" + strings.Join(elements, ", ") + "}"
}

// ParamList represents function parameter patterns.
type ParamList struct {
	LParen  Pos
	VarArgs bool
	List    []Pattern
	RParen  Pos
}

func (n *ParamList) Pos() Pos {
	if n.LParen.IsValid() {
		return n.LParen
	}
	if len(n.List) > 0 {
		return n.List[0].Pos()
	}
	return NoPos
}

func (n *ParamList) End() Pos {
	if n.RParen.IsValid() {
		return n.RParen + 1
	}
	if l := len(n.List); l > 0 {
		return n.List[l-1].End()
	}
	return NoPos
}

func (n *ParamList) NumFields() int {
	if n == nil {
		return 0
	}
	return len(n.List)
}

func (n *ParamList) String() string {
	var list []string
	for i, e := range n.List {
		if n.VarArgs && i == len(n.List)-1 {
			if ip, ok := e.(*IdentPattern); ok {
				list = append(list, "..."+ip.String())
			} else {
				list = append(list, "..."+e.String())
			}
		} else {
			list = append(list, e.String())
		}
	}
	return "(" + strings.Join(list, ", ") + ")"
}

// IsDestructPatternExpr reports whether expr looks like a destructuring pattern
// when used as the left-hand side of an assignment.
func IsDestructPatternExpr(expr Expr) bool {
	switch expr.(type) {
	case *ArrayLit, *MapLit:
		return true
	default:
		return false
	}
}

// IsSimpleIdentPattern reports whether pat is a plain identifier pattern.
func IsSimpleIdentPattern(pat Pattern) (*IdentPattern, bool) {
	ip, ok := pat.(*IdentPattern)
	if !ok || ip.Default != nil {
		return nil, false
	}
	return ip, true
}

// PatternFromExpr converts an expression used as a destructuring LHS into a
// pattern. Returns nil if expr is not a valid destructuring pattern form.
func PatternFromExpr(expr Expr) Pattern {
	switch e := expr.(type) {
	case *Ident:
		return &IdentPattern{Name: e.Name, NamePos: e.NamePos}
	case *ArrayLit:
		elems := make([]Pattern, 0, len(e.Elements))
		var rest *IdentPattern
		for i, el := range e.Elements {
			if ue, ok := el.(*UnaryExpr); ok && ue.Token == token.Ellipsis {
				if id, ok := ue.Expr.(*Ident); ok {
					rest = &IdentPattern{Name: id.Name, NamePos: id.NamePos}
				}
				if i != len(e.Elements)-1 {
					return nil
				}
				continue
			}
			if p := PatternFromExpr(el); p != nil {
				elems = append(elems, p)
			} else {
				return nil
			}
		}
		return &ArrayPattern{
			LBrack:   e.LBrack,
			Elements: elems,
			Rest:     rest,
			RBrack:   e.RBrack,
		}
	case *MapLit:
		elements := make([]*MapPatternElement, 0, len(e.Elements))
		for _, el := range e.Elements {
			mpe := &MapPatternElement{
				Key:    el.Key,
				KeyPos: el.KeyPos,
			}
			if ip, ok := el.Value.(*Ident); ok {
				mpe.Name = ip.Name
				mpe.NamePos = ip.NamePos
			} else if p := PatternFromExpr(el.Value); p != nil {
				mpe.Nested = p
			} else {
				return nil
			}
			elements = append(elements, mpe)
		}
		return &MapPattern{
			LBrace:   e.LBrace,
			Elements: elements,
			RBrace:   e.RBrace,
		}
	default:
		return nil
	}
}
