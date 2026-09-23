package tengo_test

import (
	"strings"
	"testing"

	"github.com/d5/tengo/v2"
	"github.com/d5/tengo/v2/parser"
	"github.com/d5/tengo/v2/require"
)

func TestDestructuring(t *testing.T) {
	// array patterns
	expectRun(t, `[a, b] := [1, 2]; out = a + b`, nil, 3)
	expectRun(t, `[a, b, c] := [1, 2]; out = c`, nil, tengo.UndefinedValue)
	expectRun(t, `[a] := [1, 2, 3]; out = a`, nil, 1)
	expectRun(t, `[a, b] := immutable([4, 5]); out = b`, nil, 5)
	expectRun(t, `[] := [1, 2]; out = 1`, nil, 1)

	// map patterns
	expectRun(t, `{x, y} := {x: 1, y: 2}; out = x * 10 + y`, nil, 12)
	expectRun(t, `{x: a} := {x: 7}; out = a`, nil, 7)
	expectRun(t, `{x: a = 50} := {}; out = a`, nil, 50)
	expectRun(t, `{x: a = 50} := {x: 1}; out = a`, nil, 1)
	expectRun(t, `{x = 3} := {}; out = x`, nil, 3)
	expectRun(t, `{"a-b": v} := {"a-b": 9}; out = v`, nil, 9)
	expectRun(t, `{z} := {x: 1}; out = z`, nil, tengo.UndefinedValue)
	expectRun(t, `{} := {a: 1}; out = 1`, nil, 1)
	expectRun(t, `{sq} := import("mod"); out = sq(4)`,
		Opts().Module("mod", `export {sq: func(x) { return x * x }}`), 16)

	// defaults apply only to missing positions/keys
	expectRun(t, `[a = 5] := [undefined]; out = a`, nil, tengo.UndefinedValue)
	expectRun(t, `{a = 5} := {a: undefined}; out = a`, nil, tengo.UndefinedValue)
	expectRun(t, `[a, b = 5] := [1]; out = b`, nil, 5)

	// defaults are lazy
	expectRun(t, `
n := 0
f := func() { n++; return 9 }
[a = f()] := [1]
[b = f()] := []
out = [a, b, n]`, nil, ARR{1, 9, 1})

	// defaults may reference earlier bindings
	expectRun(t, `[a, b = a * 2] := [3]; out = b`, nil, 6)
	expectRun(t, `[{x}, y = x + 1] := [{x: 1}]; out = y`, nil, 2)

	// nested patterns
	expectRun(t, `[a, [b, c]] := [1, [2, 3]]; out = a + b + c`, nil, 6)
	expectRun(t, `{p: [a, {q}]} := {p: [1, {q: 2}]}; out = [a, q]`,
		nil, ARR{1, 2})
	expectRun(t, `[[a, b] = [1, 2]] := []; out = [a, b]`, nil, ARR{1, 2})

	// rest
	expectRun(t, `[a, ...r] := [1, 2, 3]; out = r`, nil, ARR{2, 3})
	expectRun(t, `[a, b, ...r] := [1]; out = r`, nil, ARR{})
	expectRun(t, `[...r] := []; out = r`, nil, ARR{})
	expectRun(t, `x := [1, 2]; [...r] := x; r[0] = 5; out = x[0]`, nil, 1)

	// "_" discards
	expectRun(t, `[_, _, c] := [1, 2, 3]; out = c`, nil, 3)

	// locals and closures
	expectRun(t, `
f := func() {
	[a, {b}] := [1, {b: 2}]
	return func() { return a + b }
}
out = f()()`, nil, 3)
	expectRun(t, `
out = 0
for [i, n] := [0, 3]; i < n; i++ { out += i }`, nil, 3)

	// function parameters
	expectRun(t, `f := func([a, b]) { return a + b }; out = f([1, 2])`,
		nil, 3)
	expectRun(t, `f := func({x, y: z = 5}) { return x + z }; out = f({x: 1})`,
		nil, 6)
	expectRun(t, `
f := func(a, [b, ...r], {c = a}, ...v) { return [a, b, r, c, v] }
out = f(1, [2, 3, 4], {}, 5, 6)`, nil,
		ARR{1, 2, ARR{3, 4}, 1, ARR{5, 6}})
	expectRun(t, `f := func([a], {b = a}) { return b }; out = f([7], {})`,
		nil, 7)

	// existing literal syntax is unchanged
	expectRun(t, `a := [1, 2]; b := {x: 1}; out = [a, b]`,
		nil, ARR{ARR{1, 2}, MAP{"x": 1}})

	// runtime errors
	expectError(t, `[a] := 1`, nil, "cannot destructure int as array")
	expectError(t, `{a} := [1]`, nil, "cannot destructure array as map")
	expectError(t, `[...a] := undefined`, nil,
		"cannot destructure undefined as array")

	// compile errors
	expectError(t, `[...a, b] := [1, 2]`, nil, "rest element must be last")
	expectError(t, `f := func([...a, b]) {}`, nil,
		"rest element must be last")
	expectError(t, `a := 0; b := 0; [a, b] = [1, 2]`, nil,
		"cannot use destructuring with =")
	expectError(t, `a := 0; {a} = {a: 1}`, nil,
		"cannot use destructuring with =")
	expectError(t, `{...a} := {}`, nil,
		"rest element is not supported in map patterns")
	expectError(t, `[1] := [1]`, nil, "invalid destructuring target")
	expectError(t, `a := 1; [a] := [2]`, nil, "'a' redeclared in this block")
	expectError(t, `[a, a] := [1, 2]`, nil, "'a' redeclared in this block")
	expectError(t, `f := func(a, [a]) {}`, nil,
		"'a' redeclared in this block")
}

func TestDestructuringParseErrors(t *testing.T) {
	for _, src := range []string{
		`x := [a = 1]`,
		`x := [...a]`,
		`x := {a}`,
		`f([a = 1])`,
	} {
		fileSet := parser.NewFileSet()
		file := fileSet.AddFile("test", -1, len(src))
		_, err := parser.NewParser(file, []byte(src), nil).ParseFile()
		require.Error(t, err, src)
		require.True(t, strings.Contains(err.Error(),
			"destructuring syntax is only allowed"), err.Error())
	}
}
