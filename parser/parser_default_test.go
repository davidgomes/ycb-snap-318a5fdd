package parser

import (
	"testing"

	"github.com/mattn/anko/ast"
)

func findFuncExpr(stmts ast.Stmt) *ast.FuncExpr {
	switch s := stmts.(type) {
	case *ast.StmtsStmt:
		for _, st := range s.Stmts {
			if fe := findFuncExpr(st); fe != nil {
				return fe
			}
		}
	case *ast.ExprStmt:
		if fe, ok := s.Expr.(*ast.FuncExpr); ok {
			return fe
		}
	}
	return nil
}

func TestDefaultParamParse(t *testing.T) {
	stmts, err := ParseSrc(`func f(a, b=2, c...) { return len(c) }`)
	if err != nil {
		t.Fatal(err)
	}
	fe := findFuncExpr(stmts)
	if fe == nil {
		t.Fatal("FuncExpr not found")
	}
	if len(fe.Params) != 3 {
		t.Fatalf("params len = %d", len(fe.Params))
	}
	if fe.Params[1].Default == nil {
		t.Fatal("expected default on param b")
	}
	if !fe.VarArg {
		t.Fatal("expected variadic function")
	}
}
