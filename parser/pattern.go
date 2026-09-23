package parser

import (
	"strconv"
	"strings"

	"github.com/d5/tengo/v2/token"
)

// PatternElement represents a single element of an array pattern.
type PatternElement struct {
	Target  Expr // *Ident, *ArrayPattern or *MapPattern
	Default Expr // optional
}

func (e *PatternElement) String() string {
	if e.Default != nil {
		return e.Target.String() + " = " + e.Default.String()
	}
	return e.Target.String()
}

// ArrayPattern represents an array destructuring pattern.
type ArrayPattern struct {
	LBrack   Pos
	Elements []*PatternElement
	Rest     *Ident // optional
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
	if e.Rest != nil {
		elements = append(elements, "..."+e.Rest.String())
	}
	return "[" + strings.Join(elements, ", ") + "]"
}

// MapPatternEntry represents a single entry of a map pattern.
type MapPatternEntry struct {
	Key     string
	KeyPos  Pos
	Target  Expr // *Ident, *ArrayPattern or *MapPattern
	Default Expr // optional
}

func (e *MapPatternEntry) String() string {
	s := e.Key + ": " + e.Target.String()
	if e.Default != nil {
		s += " = " + e.Default.String()
	}
	return s
}

// MapPattern represents a map destructuring pattern.
type MapPattern struct {
	LBrace  Pos
	Entries []*MapPatternEntry
	RBrace  Pos
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
	var entries []string
	for _, m := range e.Entries {
		entries = append(entries, m.String())
	}
	return "{" + strings.Join(entries, ", ") + "}"
}

// parsePattern parses an array or map pattern. Violations of the rest
// placement rule are appended to restErrs instead of being reported, so that
// speculative parsing can decide whether to commit.
func (p *Parser) parsePattern(restErrs *[]Pos) Expr {
	if p.token == token.LBrack {
		return p.parseArrayPattern(restErrs)
	}
	return p.parseMapPattern(restErrs)
}

func (p *Parser) parsePatternTarget(restErrs *[]Pos) Expr {
	switch p.token {
	case token.LBrack, token.LBrace:
		return p.parsePattern(restErrs)
	}
	return p.parseIdent()
}

func (p *Parser) parseArrayPattern(restErrs *[]Pos) Expr {
	x := &ArrayPattern{LBrack: p.expect(token.LBrack)}
	p.exprLevel++
	for p.token != token.RBrack && p.token != token.EOF {
		if x.Rest != nil {
			*restErrs = append(*restErrs, p.pos)
		}
		if p.token == token.Ellipsis {
			p.next()
			x.Rest = p.parseIdent()
		} else {
			el := &PatternElement{Target: p.parsePatternTarget(restErrs)}
			if p.token == token.Assign {
				p.next()
				el.Default = p.parseExpr()
			}
			x.Elements = append(x.Elements, el)
		}
		if p.token != token.Comma {
			break
		}
		p.next()
	}
	p.exprLevel--
	x.RBrack = p.expect(token.RBrack)
	return x
}

func (p *Parser) parseMapPattern(restErrs *[]Pos) Expr {
	x := &MapPattern{LBrace: p.expect(token.LBrace)}
	p.exprLevel++
	for p.token != token.RBrace && p.token != token.EOF {
		entry := &MapPatternEntry{KeyPos: p.pos}
		switch p.token {
		case token.Ident:
			entry.Key = p.tokenLit
			p.next()
		case token.String:
			entry.Key, _ = strconv.Unquote(p.tokenLit)
			p.next()
		case token.Ellipsis:
			p.error(p.pos, "rest element is not supported in map patterns")
			p.next()
		default:
			p.errorExpected(p.pos, "map key")
			p.next()
		}
		if p.token == token.Colon {
			p.next()
			entry.Target = p.parsePatternTarget(restErrs)
		} else {
			entry.Target = &Ident{Name: entry.Key, NamePos: entry.KeyPos}
		}
		if p.token == token.Assign {
			p.next()
			entry.Default = p.parseExpr()
		}
		x.Entries = append(x.Entries, entry)
		if p.token != token.Comma {
			break
		}
		p.next()
	}
	p.exprLevel--
	x.RBrace = p.expect(token.RBrace)
	return x
}

// tryParseDestructuring speculatively parses a destructuring pattern at the
// start of a simple statement. It restores the parser state and returns nil
// when the input is not a destructuring assignment.
func (p *Parser) tryParseDestructuring() Stmt {
	saved, savedScanner := *p, *p.scanner
	var restErrs []Pos
	pattern := p.parsePattern(&restErrs)
	if len(p.errors) > len(saved.errors) ||
		(p.token != token.Define && p.token != token.Assign) {
		*p = saved
		*p.scanner = savedScanner
		return nil
	}
	for _, pos := range restErrs {
		p.error(pos, "rest element must be last")
	}
	pos, tok := p.pos, p.token
	if tok == token.Assign {
		p.error(pos, "cannot use destructuring with =")
	}
	p.next()
	return &AssignStmt{
		LHS:      []Expr{pattern},
		RHS:      []Expr{p.parseExpr()},
		Token:    tok,
		TokenPos: pos,
	}
}
