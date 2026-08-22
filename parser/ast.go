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

// IdentList represents a list of function parameters (patterns).
type IdentList struct {
	LParen  Pos
	VarArgs bool
	Params  []Pattern
	RParen  Pos
}

// Pos returns the position of first character belonging to the node.
func (n *IdentList) Pos() Pos {
	if n.LParen.IsValid() {
		return n.LParen
	}
	if len(n.Params) > 0 {
		return n.Params[0].Pos()
	}
	return NoPos
}

// End returns the position of first character immediately after the node.
func (n *IdentList) End() Pos {
	if n.RParen.IsValid() {
		return n.RParen + 1
	}
	if l := len(n.Params); l > 0 {
		return n.Params[l-1].End()
	}
	return NoPos
}

// NumFields returns the number of fields.
func (n *IdentList) NumFields() int {
	if n == nil {
		return 0
	}
	return len(n.Params)
}

func (n *IdentList) String() string {
	var list []string
	for i, e := range n.Params {
		if n.VarArgs && i == len(n.Params)-1 {
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
