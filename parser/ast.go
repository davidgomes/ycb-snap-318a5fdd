package parser

import (
	"strings"
)

const (
	nullRep = "<null>"
)

// Node represents a node in the AST.
type Node interface {
	// Pos returns the position of first character belonging to the node.
	Pos() Pos
	// End returns the position of first character immediately after the node.
	End() Pos
	// String returns a string representation of the node.
	String() string
}

// FuncParam is a function parameter: a plain identifier, an optional
// default, or a destructuring pattern.
type FuncParam struct {
	Name    *Ident
	Pattern *BindingPattern
	Default Expr
}

func (p *FuncParam) String() string {
	if p == nil {
		return nullRep
	}
	s := ""
	if p.Pattern != nil {
		s = p.Pattern.String()
	} else if p.Name != nil {
		s = p.Name.String()
	}
	if p.Default != nil {
		s += " = " + p.Default.String()
	}
	return s
}

// IdentList represents a list of identifiers.
type IdentList struct {
	LParen  Pos
	VarArgs bool
	List    []*Ident
	// Params is set when any parameter is a destructuring pattern or has a
	// default. When nil, List is the full parameter list.
	Params []*FuncParam
	RParen Pos
}

// Pos returns the position of first character belonging to the node.
func (n *IdentList) Pos() Pos {
	if n.LParen.IsValid() {
		return n.LParen
	}
	if len(n.List) > 0 {
		return n.List[0].Pos()
	}
	return NoPos
}

// End returns the position of first character immediately after the node.
func (n *IdentList) End() Pos {
	if n.RParen.IsValid() {
		return n.RParen + 1
	}
	if l := len(n.List); l > 0 {
		return n.List[l-1].End()
	}
	return NoPos
}

// NumFields returns the number of fields.
func (n *IdentList) NumFields() int {
	if n == nil {
		return 0
	}
	return len(n.List)
}

func (n *IdentList) String() string {
	if len(n.Params) > 0 {
		var list []string
		for i, e := range n.Params {
			s := e.String()
			if n.VarArgs && i == len(n.Params)-1 {
				s = "..." + s
			}
			list = append(list, s)
		}
		return "(" + strings.Join(list, ", ") + ")"
	}
	var list []string
	for i, e := range n.List {
		if n.VarArgs && i == len(n.List)-1 {
			list = append(list, "..."+e.String())
		} else {
			list = append(list, e.String())
		}
	}
	return "(" + strings.Join(list, ", ") + ")"
}
