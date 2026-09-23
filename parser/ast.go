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

// IdentList represents a list of identifiers.
type IdentList struct {
	LParen  Pos
	VarArgs bool
	List    []*Ident
	RParen  Pos

	// Patterns holds the destructuring pattern for each parameter in List,
	// or nil for plain identifier parameters. Patterns is nil if no
	// parameter uses destructuring.
	Patterns []Expr
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
	var list []string
	for i, e := range n.List {
		s := e.String()
		if i < len(n.Patterns) && n.Patterns[i] != nil {
			s = n.Patterns[i].String()
		}
		if n.VarArgs && i == len(n.List)-1 {
			list = append(list, "..."+s)
		} else {
			list = append(list, s)
		}
	}
	return "(" + strings.Join(list, ", ") + ")"
}
