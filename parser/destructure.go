package parser

import (
	"strconv"

	"github.com/d5/tengo/v2/token"
)

type parserSnap struct {
	pos       Pos
	token     token.Token
	tokenLit  string
	errors    ErrorList
	scanner   Scanner
	exprLevel int
	syncPos   Pos
	syncCount int
}

func (p *Parser) snapshot() parserSnap {
	return parserSnap{
		pos:       p.pos,
		token:     p.token,
		tokenLit:  p.tokenLit,
		errors:    append(ErrorList(nil), p.errors...),
		scanner:   *p.scanner,
		exprLevel: p.exprLevel,
		syncPos:   p.syncPos,
		syncCount: p.syncCount,
	}
}

func (p *Parser) restore(s parserSnap) {
	p.pos = s.pos
	p.token = s.token
	p.tokenLit = s.tokenLit
	p.errors = s.errors
	*p.scanner = s.scanner
	p.exprLevel = s.exprLevel
	p.syncPos = s.syncPos
	p.syncCount = s.syncCount
	p.speculative = false
	p.specErrors = nil
}

// tryParseDestructure parses `[...] :=` / `{...} :=` (and the same with `=`).
// Any other `[` or `{` is left for the ordinary expression parser.
func (p *Parser) tryParseDestructure() (Stmt, bool) {
	snap := p.snapshot()
	p.speculative = true
	p.specErrors = nil
	pat := p.parsePattern()
	specErrs := p.specErrors
	p.speculative = false
	p.specErrors = nil

	if p.token != token.Define && p.token != token.Assign {
		p.restore(snap)
		return nil, false
	}
	for _, err := range specErrs {
		p.errors = append(p.errors, err)
	}
	pos, tok := p.pos, p.token
	p.next()
	var rhs []Expr
	if p.token != token.Semicolon && p.token != token.RBrace &&
		p.token != token.EOF {
		rhs = p.parseExprList()
	}
	if len(specErrs) > 0 || pat == nil {
		return &BadStmt{From: snap.pos, To: p.pos}, true
	}
	return &DestructureStmt{
		Pattern:  pat,
		RHS:      rhs,
		Token:    tok,
		TokenPos: pos,
	}, true
}

func (p *Parser) parseIdentList() *IdentList {
	if p.trace {
		defer untracep(tracep(p, "IdentList"))
	}

	var idents []*Ident
	var patterns []*Pattern
	lparen := p.expect(token.LParen)
	complex := false
	isVarArgs := false
	if p.token != token.RParen {
		for {
			if p.token == token.Ellipsis {
				ell := p.pos
				p.next()
				name := p.parseIdent()
				if p.token == token.Assign {
					p.error(p.pos, "rest element cannot have a default")
					p.next()
					_ = p.parseExpr()
				}
				pat := &Pattern{
					Kind: PatternIdent,
					Name: name,
					Rest: true,
					LPos: ell,
					RPos: name.End(),
				}
				patterns = append(patterns, pat)
				idents = append(idents, name)
				isVarArgs = true
				if p.token == token.Comma {
					comma := p.pos
					p.next()
					if p.token != token.RParen && p.token != token.EOF {
						p.error(comma, "rest element must be last")
					}
				}
				break
			}

			pat := p.parsePattern()
			if pat == nil {
				break
			}
			p.parsePatternDefault(pat)
			patterns = append(patterns, pat)
			if pat.Kind == PatternIdent && pat.Default == nil &&
				pat.Name != nil && !pat.Rest {
				idents = append(idents, pat.Name)
			} else {
				complex = true
			}
			if p.token != token.Comma {
				break
			}
			p.next()
			if p.token == token.RParen || p.token == token.EOF {
				p.errorExpected(p.pos, "parameter")
				break
			}
		}
	}
	for i, pat := range patterns {
		if pat != nil && pat.Rest && i != len(patterns)-1 {
			p.error(pat.Pos(), "rest element must be last")
			break
		}
	}
	rparen := p.expect(token.RParen)
	list := &IdentList{
		LParen:  lparen,
		RParen:  rparen,
		VarArgs: isVarArgs,
		List:    idents,
	}
	if complex {
		list.Patterns = patterns
		if n := len(patterns); n > 0 && patterns[n-1] != nil && patterns[n-1].Rest {
			list.VarArgs = true
		}
	}
	return list
}

func (p *Parser) parsePattern() *Pattern {
	var pat *Pattern
	switch p.token {
	case token.Ident:
		name := p.parseIdent()
		pat = &Pattern{
			Kind: PatternIdent,
			Name: name,
			LPos: name.NamePos,
			RPos: name.End(),
		}
	case token.LBrack:
		pat = p.parseArrayPattern()
	case token.LBrace:
		pat = p.parseMapPattern()
	default:
		p.errorExpected(p.pos, "pattern")
		p.next()
		return nil
	}
	return pat
}

// parsePatternDefault attaches `= expr` when it is a default value.
// The `=` of a destructuring statement is not a default; callers that own
// a parameter or a pattern element decide when to consume it.
func (p *Parser) parsePatternDefault(pat *Pattern) {
	if pat == nil || p.token != token.Assign {
		return
	}
	p.next()
	pat.Default = p.parseExpr()
	if pat.Default != nil {
		pat.RPos = pat.Default.End()
	}
}

func (p *Parser) parseArrayPattern() *Pattern {
	lbrack := p.expect(token.LBrack)
	var elems []*Pattern
	for p.token != token.RBrack && p.token != token.EOF {
		var el *Pattern
		if p.token == token.Ellipsis {
			ell := p.pos
			p.next()
			name := p.parseIdent()
			if p.token == token.Assign {
				p.error(p.pos, "rest element cannot have a default")
				p.next()
				_ = p.parseExpr()
			}
			el = &Pattern{
				Kind: PatternIdent,
				Name: name,
				Rest: true,
				LPos: ell,
				RPos: name.End(),
			}
		} else {
			el = p.parsePattern()
			if el == nil {
				break
			}
			p.parsePatternDefault(el)
		}
		if len(elems) > 0 && elems[len(elems)-1].Rest {
			p.error(elems[len(elems)-1].Pos(), "rest element must be last")
		}
		elems = append(elems, el)
		if !p.expectComma(token.RBrack, "pattern element") {
			break
		}
	}
	rbrack := p.expect(token.RBrack)
	return &Pattern{
		Kind:     PatternArray,
		Elements: elems,
		LPos:     lbrack,
		RPos:     rbrack,
	}
}

func (p *Parser) parseMapPattern() *Pattern {
	lbrace := p.expect(token.LBrace)
	var elems []*Pattern
	for p.token != token.RBrace && p.token != token.EOF {
		if p.token == token.Ellipsis {
			p.error(p.pos, "rest element not supported in map pattern")
			p.next()
			if p.token == token.Ident {
				p.next()
			}
		} else {
			el := p.parseMapElementPattern()
			if el == nil {
				break
			}
			elems = append(elems, el)
		}
		if !p.expectComma(token.RBrace, "pattern element") {
			break
		}
	}
	rbrace := p.expect(token.RBrace)
	return &Pattern{
		Kind:     PatternMap,
		Elements: elems,
		LPos:     lbrace,
		RPos:     rbrace,
	}
}

func (p *Parser) parseMapElementPattern() *Pattern {
	pos := p.pos
	var key string
	var identName *Ident
	switch p.token {
	case token.Ident:
		identName = p.parseIdent()
		key = identName.Name
	case token.String:
		key, _ = strconv.Unquote(p.tokenLit)
		p.next()
	default:
		p.errorExpected(pos, "map key")
		p.next()
		return nil
	}
	if p.token == token.Colon {
		p.next()
		pat := p.parsePattern()
		if pat == nil {
			return nil
		}
		pat.Key = key
		pat.KeyPos = pos
		p.parsePatternDefault(pat)
		return pat
	}
	if identName == nil {
		p.errorExpected(p.pos, "':'")
		return nil
	}
	pat := &Pattern{
		Kind:   PatternIdent,
		Name:   identName,
		Key:    key,
		KeyPos: pos,
		LPos:   pos,
		RPos:   identName.End(),
	}
	if p.token == token.Assign {
		p.next()
		pat.Default = p.parseExpr()
		if pat.Default != nil {
			pat.RPos = pat.Default.End()
		}
	}
	return pat
}
