package parser

import (
	"strings"

	"github.com/mattn/anko/ast"
)

func applyFuncDefaults(yylex yyLexer, fn *ast.FuncExpr) {
	l, ok := yylex.(*Lexer)
	if !ok {
		return
	}
	fn.Defaults = l.popFuncDefaults()
	if !validFuncDefaults(fn) {
		yylex.Error("invalid default argument declaration")
	}
}

func validFuncDefaults(fn *ast.FuncExpr) bool {
	seenDefault := false
	for i := range fn.Params {
		var def ast.Expr
		if i < len(fn.Defaults) {
			def = fn.Defaults[i]
		}
		isVarArg := fn.VarArg && i == len(fn.Params)-1
		if isVarArg && def != nil {
			return false
		}
		if def != nil {
			seenDefault = true
			continue
		}
		if seenDefault && !isVarArg {
			return false
		}
	}
	return true
}

func exprFromStmt(stmt ast.Stmt) (ast.Expr, bool) {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		return s.Expr, s.Expr != nil
	case *ast.StmtsStmt:
		if len(s.Stmts) == 1 {
			return exprFromStmt(s.Stmts[0])
		}
	}
	return nil, false
}

func (l *Lexer) pushFuncDefaults() {
	l.funcDefaultsStack = append(l.funcDefaultsStack, nil)
}

func (l *Lexer) appendFuncDefault(expr ast.Expr) {
	if len(l.funcDefaultsStack) == 0 {
		l.pushFuncDefaults()
	}
	i := len(l.funcDefaultsStack) - 1
	l.funcDefaultsStack[i] = append(l.funcDefaultsStack[i], expr)
}

func (l *Lexer) popFuncDefaults() []ast.Expr {
	if len(l.funcDefaultsStack) == 0 {
		return nil
	}
	last := l.funcDefaultsStack[len(l.funcDefaultsStack)-1]
	l.funcDefaultsStack = l.funcDefaultsStack[:len(l.funcDefaultsStack)-1]
	if !hasExpr(last) {
		return nil
	}
	return last
}

func hasExpr(exprs []ast.Expr) bool {
	for _, expr := range exprs {
		if expr != nil {
			return true
		}
	}
	return false
}

func (l *Lexer) skipSpaceAndNewlines() {
	for isBlank(l.s.peek()) || l.s.peek() == '\n' {
		l.s.next()
	}
}

func (l *Lexer) tryParseParamDefault() ast.Expr {
	l.skipSpaceAndNewlines()
	if l.s.peek() != '=' || l.s.peekPlus(1) == '=' {
		return nil
	}
	l.s.next()
	src, err := l.s.scanDefaultExprSource()
	if err != nil {
		l.e = &Error{Message: err.Error(), Pos: l.pos, Fatal: true}
		return nil
	}
	src = strings.TrimSpace(src)
	if src == "" {
		l.e = &Error{Message: "invalid default argument declaration", Pos: l.pos}
		return nil
	}
	stmt, err := ParseSrc(src)
	if err != nil {
		if l.e == nil {
			if pe, ok := err.(*Error); ok {
				l.e = pe
			} else {
				l.e = &Error{Message: err.Error(), Pos: l.pos}
			}
		}
		return nil
	}
	expr, ok := exprFromStmt(stmt)
	if !ok {
		l.e = &Error{Message: "invalid default argument declaration", Pos: l.pos}
		return nil
	}
	return expr
}

func (l *Lexer) rejectVariadicDefault() {
	l.skipSpaceAndNewlines()
	if l.s.peek() != '=' || l.s.peekPlus(1) == '=' {
		return
	}
	l.s.next()
	_, err := l.s.scanDefaultExprSource()
	if err != nil && l.e == nil {
		l.e = &Error{Message: err.Error(), Pos: l.pos, Fatal: true}
	}
	l.e = &Error{Message: "invalid default argument declaration", Pos: l.pos}
}

func (s *Scanner) scanDefaultExprSource() (string, error) {
	start := s.current()
	paren, brack, brace := 0, 0, 0
	for {
		ch := s.peek()
		if ch == EOF {
			if paren == 0 && brack == 0 && brace == 0 {
				break
			}
			return "", &Error{Message: "unexpected EOF", Pos: s.pos(), Fatal: true}
		}
		if paren == 0 && brack == 0 && brace == 0 {
			if ch == ',' || ch == ')' {
				break
			}
			if ch == '.' && s.peekPlus(1) == '.' && s.peekPlus(2) == '.' {
				break
			}
		}
		switch ch {
		case '"', '\'':
			if _, err := s.scanString(ch); err != nil {
				return "", err
			}
			continue
		case '`':
			if _, err := s.scanRawString('`'); err != nil {
				return "", err
			}
			continue
		case '#':
			for !isEOL(s.peek()) {
				s.next()
			}
			continue
		case '/':
			next := s.peekPlus(1)
			if next == '/' {
				for !isEOL(s.peek()) {
					s.next()
				}
				continue
			}
			if next == '*' {
				s.next()
				s.next()
				for {
					if s.peek() == EOF {
						return "", &Error{Message: "unexpected EOF", Pos: s.pos(), Fatal: true}
					}
					if s.peek() == '*' && s.peekPlus(1) == '/' {
						s.next()
						s.next()
						break
					}
					s.next()
				}
				continue
			}
		case '(':
			paren++
		case ')':
			paren--
		case '[':
			brack++
		case ']':
			brack--
		case '{':
			brace++
		case '}':
			brace--
		}
		s.next()
	}
	return string(s.src[start:s.current()]), nil
}
