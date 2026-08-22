package vm

import (
	"fmt"
	"testing"

	"github.com/mattn/anko/env"
	"github.com/mattn/anko/parser"
)

func TestVMFuncRegistration(t *testing.T) {
	e := env.NewEnv()
	stmts, err := parser.ParseSrc(`func f(a, b=2) { return a + b }`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Run(e, &Options{}, stmts)
	if err != nil {
		t.Fatal(err)
	}
	f, err := e.GetValue("f")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := getVMFuncExpr(f); !ok {
		t.Fatal("VM function not registered")
	}
}

func TestDefaultArgs(t *testing.T) {
	t.Parallel()

	tests := []Test{
		{Script: `func f(a, b=2) { return a + b }; f(1)`, RunOutput: int64(3)},
		{Script: `func f(a, b=2) { return a + b }; f(1, 5)`, RunOutput: int64(6)},
		{Script: `func f(a=1, b=2) { return a + b }; f()`, RunOutput: int64(3)},
		{Script: `func f(a=1, b=2) { return a + b }; f(5)`, RunOutput: int64(7)},
		{Script: `func f(a, b=2, c=3) { return a + b + c }; f(1)`, RunOutput: int64(6)},
		{Script: `x = 10; func f(a=x) { return a }; f()`, RunOutput: int64(10), Output: map[string]interface{}{"x": int64(10)}},
		{Script: `func f(a, b=a+1) { return b }; f(5)`, RunOutput: int64(6)},
		{Script: `func f(a, b=2, c...) { return len(c) }; f(1)`, RunOutput: int64(0)},
		{Script: `func f(a, b=2, c...) { return c }; f(1, 5, 6)`, RunOutput: []interface{}{int64(6)}},
		{Script: `func f(a=1, b) { return b }`, ParseError: fmt.Errorf("invalid default argument declaration")},
		{Script: `func f(a=1, b, c) { return c }`, ParseError: fmt.Errorf("invalid default argument declaration")},
		{Script: `func f(a=1 ...) { return a }`, ParseError: fmt.Errorf("invalid default argument declaration")},
		{Script: `func f(a, b=2 ...) { return b }`, ParseError: fmt.Errorf("invalid default argument declaration")},
		{Script: `func f(a, b=2) { return a + b }; f()`, RunError: fmt.Errorf("function wants 1 arguments but received 0")},
		{Script: `func f(a, b=2) { return a + b }; f(1, 2, 3)`, RunError: fmt.Errorf("function wants 2 arguments but received 3")},
	}

	runTests(t, tests, nil, &Options{Debug: true})
}
