package tengo_test

import (
	"testing"

	"github.com/d5/tengo/v2"
)

func TestDestructureArray(t *testing.T) {
	expectRun(t, `[a, b, c] := [1, 2, 3]; out = [a, b, c]`, nil, ARR{1, 2, 3})
	expectRun(t, `[a, b] := [1, 2, 3]; out = [a, b]`, nil, ARR{1, 2})
	expectRun(t, `[a, b, c] := [1]; out = [a, is_undefined(b), is_undefined(c)]`, nil, ARR{1, true, true})
	expectRun(t, `[a] := [0]; out = a`, nil, 0)
	expectRun(t, `[a] := [false]; out = a`, nil, false)
	expectRun(t, `[_, b] := [1, 2]; out = b`, nil, 2)
	expectRun(t, `[_, _] := [1, 2]; out = true`, nil, true)
	expectRun(t, `[] := [1, 2, 3]; out = true`, nil, true)
	expectRun(t, `[a, b] := immutable([1, 2]); out = a + b`, nil, 3)
	expectRun(t, `out = func() { [a, b] := [1, 2]; return a + b }()`, nil, 3)
	expectRun(t, `[a] := undefined; out = is_undefined(a)`, nil, true)
}

func TestDestructureDefaults(t *testing.T) {
	expectRun(t, `[a, b = 10] := [1]; out = [a, b]`, nil, ARR{1, 10})
	expectRun(t, `[a, b = 10] := [1, 2]; out = [a, b]`, nil, ARR{1, 2})
	expectRun(t, `[a = 5] := [undefined]; out = is_undefined(a)`, nil, true)
	expectRun(t, `[a = 5] := [0]; out = a`, nil, 0)
	expectRun(t, `[a = true] := [false]; out = a`, nil, false)
	expectRun(t, `[a, b = a + 1] := [3]; out = b`, nil, 4)
	expectRun(t, `[a = 1, b = a + 1] := []; out = [a, b]`, nil, ARR{1, 2})
	expectRun(t, `[a = 1, b = a + 1, c = b + 1] := []; out = c`, nil, 3)

	expectRun(t, `
n := 0
f := func() { n++; return 9 }
[a, b = f()] := [1, 2]
out = [a, b, n]
`, nil, ARR{1, 2, 0})

	expectRun(t, `
n := 0
f := func() { n++; return 9 }
[a, b = f()] := [1]
out = [a, b, n]
`, nil, ARR{1, 9, 1})
}

func TestDestructureRest(t *testing.T) {
	expectRun(t, `[a, ...rest] := [1, 2, 3, 4]; out = [a, rest]`, nil, ARR{1, ARR{2, 3, 4}})
	expectRun(t, `[...rest] := [1, 2]; out = rest`, nil, ARR{1, 2})
	expectRun(t, `[a, b, ...rest] := [1]; out = [a, is_undefined(b), rest]`, nil, ARR{1, true, ARR{}})
	expectRun(t, `[a, ...rest] := []; out = [is_undefined(a), rest]`, nil, ARR{true, ARR{}})
	expectRun(t, `[...rest] := undefined; out = rest`, nil, ARR{})
	expectRun(t, `[...rest] := immutable([1, 2, 3]); out = rest`, nil, ARR{1, 2, 3})
}

func TestDestructureMap(t *testing.T) {
	expectRun(t, `{x, y} := {x: 1, y: 2}; out = [x, y]`, nil, ARR{1, 2})
	expectRun(t, `{x: a, y: b} := {x: 1, y: 2}; out = [a, b]`, nil, ARR{1, 2})
	expectRun(t, `{x} := {x: 1, y: 2}; out = x`, nil, 1)
	expectRun(t, `{x: a = 50} := {}; out = a`, nil, 50)
	expectRun(t, `{x: a = 50} := {y: 1}; out = a`, nil, 50)
	expectRun(t, `{x: a = 50} := {x: 7}; out = a`, nil, 7)
	expectRun(t, `{x: a = 50} := {x: undefined}; out = is_undefined(a)`, nil, true)
	expectRun(t, `{x = 50} := {}; out = x`, nil, 50)
	expectRun(t, `{x = 50} := {x: 1}; out = x`, nil, 1)
	expectRun(t, `{"x": a} := {"x": 8}; out = a`, nil, 8)
	expectRun(t, `{} := {a: 1}; out = true`, nil, true)
	expectRun(t, `{x} := immutable({x: 4}); out = x`, nil, 4)
	expectRun(t, `{x} := undefined; out = is_undefined(x)`, nil, true)
	expectRun(t, `{x, y: z = x} := {x: 9}; out = z`, nil, 9)
	expectRun(t, `{x: a = 1, y: b = a + 2} := {}; out = [a, b]`, nil, ARR{1, 3})

	expectRun(t, `
n := 0
f := func() { n++; return 5 }
{x: a = f()} := {x: 1}
out = [a, n]
`, nil, ARR{1, 0})
}

func TestDestructureNested(t *testing.T) {
	expectRun(t, `[[a, b], c] := [[1, 2], 3]; out = [a, b, c]`, nil, ARR{1, 2, 3})
	expectRun(t, `{a: [b, c]} := {a: [1, 2]}; out = [b, c]`, nil, ARR{1, 2})
	expectRun(t, `[{x}] := [{x: 9}]; out = x`, nil, 9)
	expectRun(t, `[[a, b = 4]] := []; out = [is_undefined(a), b]`, nil, ARR{true, 4})
	expectRun(t, `[[a, b = 4]] := [[1]]; out = [a, b]`, nil, ARR{1, 4})
	expectRun(t, `[[a, ...rest]] := [[1, 2, 3]]; out = [a, rest]`, nil, ARR{1, ARR{2, 3}})
	expectRun(t, `{a: {b: c = 8}} := {}; out = c`, nil, 8)
	expectRun(t, `out = func() { [a] := [41]; return func() { return a + 1 } }()()`, nil, 42)
}

func TestDestructureFuncParams(t *testing.T) {
	expectRun(t, `f := func([a, b]) { return a + b }; out = f([1, 2])`, nil, 3)
	expectRun(t, `f := func([a, b = 10], {x: c}) { return a + b + c }; out = f([1], {x: 3})`, nil, 14)
	expectRun(t, `f := func({x: a = 50}) { return a }; out = [f({}), f({x: 7})]`, nil, ARR{50, 7})
	expectRun(t, `f := func([a, ...rest]) { return rest }; out = f([1, 2, 3])`, nil, ARR{2, 3})
	expectRun(t, `f := func([a, b = a]) { return b }; out = [f([1]), f([1, 2])]`, nil, ARR{1, 2})
	expectRun(t, `f := func([]) { return 1 }; out = f([9])`, nil, 1)
	expectRun(t, `f := func({}) { return 2 }; out = f({a: 1})`, nil, 2)
	expectRun(t, `f := func(a = 10, b = a + 1) { return [a, b] }; out = f()`, nil, ARR{10, 11})
	expectRun(t, `f := func(a = 10, b = a + 1) { return [a, b] }; out = f(2)`, nil, ARR{2, 3})
	expectRun(t, `f := func(a = 10, b = a + 1) { return [a, b] }; out = f(2, 4)`, nil, ARR{2, 4})
	expectRun(t, `f := func(a, b = 5) { return a + b }; out = f(3)`, nil, 8)
	expectRun(t, `f := func([a, ...rest] = [1, 2, 3]) { return [a, rest] }; out = f()`, nil, ARR{1, ARR{2, 3}})
	expectRun(t, `f := func(a, ...b) { return [a, b] }; out = f(1, 2, 3)`, nil, ARR{1, ARR{2, 3}})
	expectRun(t, `f := func(a = 4, ...b) { return [a, b] }; out = f()`, nil, ARR{4, ARR{}})
	expectRun(t, `f := func(a = 4, ...b) { return [a, b] }; out = f(1, 2)`, nil, ARR{1, ARR{2}})
	expectRun(t, `f := func(a, b = 5, ...c) { return [a, b, c] }; out = f(1)`, nil, ARR{1, 5, ARR{}})
	expectCompileError(t, `f := func([a, ...b, c]) { return a }`, "rest element must be last")
	expectCompileError(t, `f := func({...a}) { return a }`, "rest not supported in map patterns")
}

func TestDestructureErrors(t *testing.T) {
	expectCompileError(t, `[a, ...b, c] := [1, 2, 3]`, "rest element must be last")
	expectCompileError(t, `[...a, b] := [1, 2]`, "rest element must be last")
	expectCompileError(t, `[[...a, b]] := [[1]]`, "rest element must be last")
	expectCompileError(t, `[a, b] = [1, 2]`, "cannot use destructuring with =")
	expectCompileError(t, `{x} = {x: 1}`, "cannot use destructuring with =")
	expectCompileError(t, `[1, 2] = x`, "cannot use destructuring with =")
	expectCompileError(t, `{...rest} := {a: 1}`, "rest not supported in map patterns")
	expectCompileError(t, `[a] := [1]; [a] := [2]`, "redeclared in this block")
	expectError(t, `[a] := 1`, nil, "not indexable")
	expectError(t, `f := func(a = 1) { return a }; f(1, 2)`, nil, "wrong number of arguments")
}

func TestDestructureScopes(t *testing.T) {
	expectRun(t, `
n := 0
for [i] := [1]; i <= 3; i++ {
	n += i
}
out = n
`, nil, 6)

	expectRun(t, `
out = 0
if [a] := [2]; a == 2 {
	out = a
}
`, nil, 2)

	expectRun(t, `
f := func() {
	[a, b = a] := [4]
	return b
}
out = f()
`, nil, 4)
}

func TestArrayLiteralUnchanged(t *testing.T) {
	expectRun(t, `out = [1, 2, 3][1]`, nil, 2)
	expectRun(t, `out = {a: 1, b: 2}["b"]`, nil, 2)
	expectRun(t, `out = {a: 1}.a`, nil, 1)
	expectRun(t, `a := [1, 2]; a[0] = 9; out = a[0]`, nil, 9)
	_ = tengo.UndefinedValue
}
