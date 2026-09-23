package parser

import (
	"strconv"

	"github.com/d5/tengo/v2/token"
)

// parseSave is a speculative-parse checkpoint.
type parseSave struct {
	scanner   Scanner
	pos       Pos
	token     token.Token
	tokenLit  string
	exprLevel int
	syncPos   Pos
	syncCount int
	errLen    int
}

func (p *Parser) save() parseSave {
	return parseSave{
		scanner:   *p.scanner,
		pos:       p.pos,
		token:     p.token,
		tokenLit:  p.tokenLit,
		exprLevel: p.exprLevel,
		syncPos:   p.syncPos,
		syncCount: p.syncCount,
		errLen:    len(p.errors),
	}
}

func (p *Parser) restore(s parseSave) {
	*p.scanner = s.scanner
	p.pos = s.pos
	p.token = s.token
	p.tokenLit = s.tokenLit
	p.exprLevel = s.exprLevel
	p.syncPos = s.syncPos
	p.syncCount = s.syncCount
	p.errors = p.errors[:s.errLen]
}

type patternParse struct {
	pat         *BindingPattern
	unambiguous bool
	soft        bool
}

// tryDestructureAssign parses `[...] :=` / `{...} :=` and the same forms
// written with `=`. Any other following token rewinds the parser so ordinary
// array and map literals are unchanged.
func (p *Parser) tryDestructureAssign() (Stmt, bool) {
	if p.token != token.LBrack && p.token != token.LBrace {
		return nil, false
	}

	cp := p.save()
	pr := p.parseBindingPattern()
	if pr.soft || pr.pat == nil {
		p.restore(cp)
		return nil, false
	}
	if p.token == token.Define || p.token == token.Assign {
		pos, tok := p.pos, p.token
		p.next()
		rhs := p.parseExprList()
		return &AssignStmt{
			LHS:      []Expr{pr.pat},
			RHS:      rhs,
			Token:    tok,
			TokenPos: pos,
		}, true
	}
	p.restore(cp)
	return nil, false
}

func (p *Parser) parseBindingPattern() patternParse {
	switch p.token {
	case token.LBrack:
		return p.parseArrayPattern()
	case token.LBrace:
		return p.parseMapPattern()
	default:
		return patternParse{soft: true}
	}
}

func (p *Parser) parseArrayPattern() patternParse {
	lpos := p.expect(token.LBrack)
	var elements []*BindingElem
	unambiguous := false
	for p.token != token.RBrack && p.token != token.EOF {
		if p.token == token.Ellipsis {
			p.next()
			if p.token != token.Ident {
				return patternParse{soft: true}
			}
			elements = append(elements, &BindingElem{
				Name: p.parseIdent(),
				Rest: true,
			})
			unambiguous = true
		} else {
			el, unamb, soft := p.parseBindingTarget()
			if soft {
				return patternParse{soft: true}
			}
			if unamb {
				unambiguous = true
			}
			elements = append(elements, el)
		}
		if !p.expectComma(token.RBrack, "binding element") {
			break
		}
	}
	rpos := p.expect(token.RBrack)
	return patternParse{
		pat: &BindingPattern{
			LPos:     lpos,
			RPos:     rpos,
			Kind:     token.LBrack,
			Elements: elements,
		},
		unambiguous: unambiguous,
	}
}

func (p *Parser) parseMapPattern() patternParse {
	lpos := p.expect(token.LBrace)
	var elements []*BindingElem
	unambiguous := false
	for p.token != token.RBrace && p.token != token.EOF {
		if p.token == token.Ellipsis {
			p.next()
			if p.token != token.Ident {
				return patternParse{soft: true}
			}
			elements = append(elements, &BindingElem{
				Name: p.parseIdent(),
				Rest: true,
			})
			unambiguous = true
			if !p.expectComma(token.RBrace, "binding element") {
				break
			}
			continue
		}

		var key string
		keyPos := p.pos
		fromString := false
		switch p.token {
		case token.Ident:
			key = p.tokenLit
			p.next()
		case token.String:
			key, _ = strconv.Unquote(p.tokenLit)
			fromString = true
			p.next()
		default:
			return patternParse{soft: true}
		}

		el := &BindingElem{Key: key, KeyPos: keyPos}
		switch p.token {
		case token.Colon:
			p.next()
			target, unamb, soft := p.parseBindingTarget()
			if soft {
				return patternParse{soft: true}
			}
			el.Name = target.Name
			el.Nested = target.Nested
			el.Default = target.Default
			if unamb {
				unambiguous = true
			}
		case token.Assign:
			// {x = expr}. String keys are not shorthand bindings.
			if fromString {
				return patternParse{soft: true}
			}
			p.next()
			el.Name = &Ident{Name: key, NamePos: keyPos}
			el.Default = p.parseExpr()
			unambiguous = true
		case token.Comma, token.RBrace, token.Semicolon:
			if fromString {
				return patternParse{soft: true}
			}
			el.Name = &Ident{Name: key, NamePos: keyPos}
			unambiguous = true
		default:
			return patternParse{soft: true}
		}
		elements = append(elements, el)
		if !p.expectComma(token.RBrace, "binding element") {
			break
		}
	}
	rpos := p.expect(token.RBrace)
	return patternParse{
		pat: &BindingPattern{
			LPos:     lpos,
			RPos:     rpos,
			Kind:     token.LBrace,
			Elements: elements,
		},
		unambiguous: unambiguous,
	}
}

// parseBindingTarget parses an identifier, a nested pattern, and an optional
// default. soft is set when the tokens are an ordinary expression instead.
func (p *Parser) parseBindingTarget() (el *BindingElem, unambiguous, soft bool) {
	switch p.token {
	case token.LBrack, token.LBrace:
		pr := p.parseBindingPattern()
		if pr.soft || pr.pat == nil {
			return nil, false, true
		}
		el = &BindingElem{Nested: pr.pat}
		unambiguous = pr.unambiguous
		if p.token == token.Assign {
			p.next()
			el.Default = p.parseExpr()
			unambiguous = true
		}
		return el, unambiguous, false
	case token.Ident:
		name := p.parseIdent()
		if p.token == token.Period || p.token == token.LBrack ||
			p.token == token.LParen {
			return nil, false, true
		}
		el = &BindingElem{Name: name}
		if p.token == token.Assign {
			p.next()
			el.Default = p.parseExpr()
			return el, true, false
		}
		return el, false, false
	default:
		return nil, false, true
	}
}
