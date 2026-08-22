package parser

import (
	"strconv"

	"github.com/d5/tengo/v2/token"
)

func (p *Parser) isDestructuringAssign() bool {
	if p.token != token.LBrack && p.token != token.LBrace {
		return false
	}

	open := p.token
	closeTok := token.RBrack
	if open == token.LBrace {
		closeTok = token.RBrace
	}

	saved := p.saveState()
	defer p.restoreState(saved)

	p.next()
	depth := 1
	for depth > 0 && p.token != token.EOF {
		switch p.token {
		case open:
			depth++
		case closeTok:
			depth--
		}
		p.next()
	}

	return p.token == token.Define || p.token == token.Assign
}

type parserState struct {
	pos      Pos
	token    token.Token
	tokenLit string
	scanner  scannerState
}

func (p *Parser) saveState() parserState {
	return parserState{
		pos:      p.pos,
		token:    p.token,
		tokenLit: p.tokenLit,
		scanner:  p.scanner.save(),
	}
}

func (p *Parser) restoreState(st parserState) {
	p.pos = st.pos
	p.token = st.token
	p.tokenLit = st.tokenLit
	p.scanner.restore(st.scanner)
}

func (p *Parser) parsePattern() Pattern {
	switch p.token {
	case token.LBrack:
		return p.parseArrayPattern()
	case token.LBrace:
		return p.parseMapPattern()
	default:
		return p.parseIdentPattern()
	}
}

func (p *Parser) parseIdentPattern() *IdentPattern {
	ident := p.parseIdent()
	var def Expr
	if p.token == token.Assign {
		p.next()
		def = p.parseExpr()
	}
	return &IdentPattern{
		Name:    ident.Name,
		NamePos: ident.NamePos,
		Default: def,
	}
}

func (p *Parser) parseArrayPattern() *ArrayPattern {
	lbrack := p.expect(token.LBrack)

	var elements []Pattern
	if p.token != token.RBrack {
		elements = append(elements, p.parseArrayPatternElement())
		for p.token == token.Comma {
			p.next()
			if p.token == token.RBrack {
				break
			}
			elements = append(elements, p.parseArrayPatternElement())
		}
	}

	rbrack := p.expect(token.RBrack)

	for i, elem := range elements {
		if _, isRest := elem.(*RestPattern); isRest && i != len(elements)-1 {
			p.error(elem.Pos(), "rest element must be last")
		}
	}

	return &ArrayPattern{
		Elements: elements,
		LBrack:   lbrack,
		RBrack:   rbrack,
	}
}

func (p *Parser) parseArrayPatternElement() Pattern {
	if p.token == token.Ellipsis {
		ellipsisPos := p.pos
		p.next()
		ident := p.parseIdent()
		return &RestPattern{
			EllipsisPos: ellipsisPos,
			Name:        ident.Name,
			NamePos:     ident.NamePos,
		}
	}

	switch p.token {
	case token.LBrack:
		return p.parseArrayPattern()
	case token.LBrace:
		return p.parseMapPattern()
	default:
		return p.parseIdentPattern()
	}
}

func (p *Parser) parseMapPattern() *MapPattern {
	lbrace := p.expect(token.LBrace)

	var elements []*MapPatternElement
	for p.token != token.RBrace && p.token != token.EOF {
		if p.token == token.Ellipsis {
			p.error(p.pos, "rest element must be last")
			p.next()
			continue
		}

		elem := p.parseMapPatternElement()
		elements = append(elements, elem)

		if !p.expectComma(token.RBrace, "map pattern element") {
			break
		}
	}

	rbrace := p.expect(token.RBrace)

	return &MapPattern{
		Elements: elements,
		LBrace:   lbrace,
		RBrace:   rbrace,
	}
}

func (p *Parser) parseMapPatternElement() *MapPatternElement {
	pos := p.pos
	key := "_"

	switch p.token {
	case token.Ident:
		key = p.tokenLit
		p.next()
		if p.token == token.Colon {
			p.next()
			pattern := p.parseMapPatternValue()
			return &MapPatternElement{
				Key:     key,
				KeyPos:  pos,
				Pattern: pattern,
			}
		}
		return &MapPatternElement{
			Key:    key,
			KeyPos: pos,
			Pattern: &IdentPattern{
				Name:    key,
				NamePos: pos,
			},
		}
	case token.String:
		v, _ := strconv.Unquote(p.tokenLit)
		key = v
		p.next()
		p.expect(token.Colon)
		pattern := p.parseMapPatternValue()
		return &MapPatternElement{
			Key:     key,
			KeyPos:  pos,
			Pattern: pattern,
		}
	default:
		p.errorExpected(pos, "map pattern key")
		p.next()
		return &MapPatternElement{
			Key:    key,
			KeyPos: pos,
			Pattern: &IdentPattern{
				Name:    key,
				NamePos: pos,
			},
		}
	}
}

func (p *Parser) parseMapPatternValue() Pattern {
	switch p.token {
	case token.LBrack:
		return p.parseArrayPattern()
	case token.LBrace:
		return p.parseMapPattern()
	default:
		return p.parseIdentPattern()
	}
}

func (p *Parser) parseParamList() *IdentList {
	if p.trace {
		defer untracep(tracep(p, "ParamList"))
	}

	var params []Pattern
	lparen := p.expect(token.LParen)
	isVarArgs := false
	if p.token != token.RParen {
		if p.token == token.Ellipsis {
			isVarArgs = true
			p.next()
			ident := p.parseIdent()
			params = append(params, &IdentPattern{
				Name:    ident.Name,
				NamePos: ident.NamePos,
			})
		} else {
			params = append(params, p.parseParam())
			for !isVarArgs && p.token == token.Comma {
				p.next()
				if p.token == token.Ellipsis {
					isVarArgs = true
					p.next()
				}
				params = append(params, p.parseParam())
			}
		}
	}

	rparen := p.expect(token.RParen)
	return &IdentList{
		LParen:  lparen,
		RParen:  rparen,
		VarArgs: isVarArgs,
		Params:  params,
	}
}

func (p *Parser) parseParam() Pattern {
	switch p.token {
	case token.LBrack:
		return p.parseArrayPattern()
	case token.LBrace:
		return p.parseMapPattern()
	default:
		ident := p.parseIdent()
		return &IdentPattern{
			Name:    ident.Name,
			NamePos: ident.NamePos,
		}
	}
}

// patternToExpr converts a pattern to an equivalent expression (for non-destructure contexts).
func patternToExpr(pat Pattern) Expr {
	switch p := pat.(type) {
	case *IdentPattern:
		return &Ident{Name: p.Name, NamePos: p.NamePos}
	case *ArrayPattern:
		var elems []Expr
		for _, e := range p.Elements {
			elems = append(elems, patternToExpr(e))
		}
		return &ArrayLit{
			Elements: elems,
			LBrack:   p.LBrack,
			RBrack:   p.RBrack,
		}
	case *MapPattern:
		var elems []*MapElementLit
		for _, e := range p.Elements {
			ip, ok := e.Pattern.(*IdentPattern)
			val := patternToExpr(e.Pattern)
			if ok && ip.Default == nil && ip.Name == e.Key {
				elems = append(elems, &MapElementLit{
					Key:      e.Key,
					KeyPos:   e.KeyPos,
					ColonPos: e.KeyPos,
					Value:    val,
				})
			} else {
				elems = append(elems, &MapElementLit{
					Key:      e.Key,
					KeyPos:   e.KeyPos,
					ColonPos: e.KeyPos,
					Value:    val,
				})
			}
		}
		return &MapLit{
			Elements: elems,
			LBrace:   p.LBrace,
			RBrace:   p.RBrace,
		}
	case *RestPattern:
		return &Ident{Name: p.Name, NamePos: p.NamePos}
	default:
		return &BadExpr{From: 0, To: 0}
	}
}
