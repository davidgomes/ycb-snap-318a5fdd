package tengo_test

import (
	"strings"
	"testing"

	"github.com/d5/tengo/v2"
)

func TestDestructuringArray(t *testing.T) {
	expectRun(t, `
[a, b] := [1, 2]
out = [a, b]
`, nil, &tengo.Array{Value: []tengo.Object{
		&tengo.Int{Value: 1},
		&tengo.Int{Value: 2},
	}})
}

func TestDestructuringArrayMissing(t *testing.T) {
	expectRun(t, `
[a, b, c] := [1]
out = [a, b, c]
`, nil, &tengo.Array{Value: []tengo.Object{
		&tengo.Int{Value: 1},
		tengo.UndefinedValue,
		tengo.UndefinedValue,
	}})
}

func TestDestructuringArrayRest(t *testing.T) {
	expectRun(t, `
[a, ...rest] := [1, 2, 3, 4]
out = [a, rest]
`, nil, &tengo.Array{Value: []tengo.Object{
		&tengo.Int{Value: 1},
		&tengo.Array{Value: []tengo.Object{
			&tengo.Int{Value: 2},
			&tengo.Int{Value: 3},
			&tengo.Int{Value: 4},
		}},
	}})
}

func TestDestructuringArrayDefault(t *testing.T) {
	expectRun(t, `
base := 10
[a, b = base + 5] := [1]
out = [a, b]
`, nil, &tengo.Array{Value: []tengo.Object{
		&tengo.Int{Value: 1},
		&tengo.Int{Value: 15},
	}})
}

func TestDestructuringArrayNested(t *testing.T) {
	expectRun(t, `
[a, [b, c]] := [1, [2, 3]]
out = [a, b, c]
`, nil, &tengo.Array{Value: []tengo.Object{
		&tengo.Int{Value: 1},
		&tengo.Int{Value: 2},
		&tengo.Int{Value: 3},
	}})
}

func TestDestructuringMapShorthand(t *testing.T) {
	expectRun(t, `
{x, y} := {x: 1, y: 2}
out = [x, y]
`, nil, &tengo.Array{Value: []tengo.Object{
		&tengo.Int{Value: 1},
		&tengo.Int{Value: 2},
	}})
}

func TestDestructuringMapRename(t *testing.T) {
	expectRun(t, `
{x: a, y: b} := {x: 3, y: 4}
out = [a, b]
`, nil, &tengo.Array{Value: []tengo.Object{
		&tengo.Int{Value: 3},
		&tengo.Int{Value: 4},
	}})
}

func TestDestructuringMapDefault(t *testing.T) {
	expectRun(t, `
{x: a = 50} := {y: 1}
out = a
`, nil, &tengo.Int{Value: 50})
}

func TestDestructuringMapNested(t *testing.T) {
	expectRun(t, `
{x: [a, b]} := {x: [5, 6]}
out = [a, b]
`, nil, &tengo.Array{Value: []tengo.Object{
		&tengo.Int{Value: 5},
		&tengo.Int{Value: 6},
	}})
}

func TestDestructuringEmptyPatterns(t *testing.T) {
	expectRun(t, `
[] := [1, 2]
{} := {a: 1}
out = true
`, nil, true)
}

func TestDestructuringFunctionParams(t *testing.T) {
	expectRun(t, `
f := func([a, b]) { return [a, b] }
out = f([7, 8])
`, nil, &tengo.Array{Value: []tengo.Object{
		&tengo.Int{Value: 7},
		&tengo.Int{Value: 8},
	}})
}

func TestDestructuringFunctionParamsMixed(t *testing.T) {
	expectRun(t, `
f := func(x, {y: z}) { return [x, z] }
out = f(1, {y: 2})
`, nil, &tengo.Array{Value: []tengo.Object{
		&tengo.Int{Value: 1},
		&tengo.Int{Value: 2},
	}})
}

func TestDestructuringDefaultReferencesEarlierBinding(t *testing.T) {
	expectRun(t, `
[a, b = a + 1] := [5]
out = [a, b]
`, nil, &tengo.Array{Value: []tengo.Object{
		&tengo.Int{Value: 5},
		&tengo.Int{Value: 6},
	}})
}

func TestDestructuringAssignWithEqualsError(t *testing.T) {
	s := tengo.NewScript([]byte(`[a, b] = [1, 2]`))
	_, err := s.Compile()
	if err == nil || !strings.Contains(err.Error(), "cannot use destructuring with =") {
		t.Fatalf("expected destructuring = error, got: %v", err)
	}
}

func TestDestructuringRestNotLastError(t *testing.T) {
	s := tengo.NewScript([]byte(`[...a, b] := [1, 2, 3]`))
	_, err := s.Compile()
	if err == nil || !strings.Contains(err.Error(), "rest element must be last") {
		t.Fatalf("expected rest element error, got: %v", err)
	}
}
