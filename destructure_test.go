package tengo_test

import (
	"testing"

	"github.com/d5/tengo/v2"
)

func TestDestructureArrays(t *testing.T) {
	expectRun(t, `
[a, b, c] := [1, 2, 3]
out = a + b + c
`, nil, 6)

	expectRun(t, `
[a, b, c] := [1]
out = [a, b, c]
`, nil, ARR{1, tengo.UndefinedValue, tengo.UndefinedValue})

	expectRun(t, `
[a, b = 9] := [1]
out = [a, b]
`, nil, ARR{1, 9})

	expectRun(t, `
[a = 9] := [undefined]
out = a
`, nil, tengo.UndefinedValue)

	expectRun(t, `
[a, b = a + 10] := [1]
out = b
`, nil, 11)

	expectRun(t, `
[a, b = a + 10] := [1, 2]
out = b
`, nil, 2)

	expectRun(t, `
[] := [1, 2, 3]
{} := {a: 1}
[] := 1
out = true
`, nil, true)

	expectRun(t, `
[a, b] := immutable([3, 4])
out = a + b
`, nil, 7)
}

func TestDestructureRest(t *testing.T) {
	expectRun(t, `
[a, b, ...rest] := [1, 2, 3, 4]
out = [a, b, rest]
`, nil, ARR{1, 2, ARR{3, 4}})

	expectRun(t, `
[a, ...rest] := [1]
out = [a, rest]
`, nil, ARR{1, ARR{}})

	expectRun(t, `
[...rest] := [1, 2, 3]
out = rest
`, nil, ARR{1, 2, 3})

	expectRun(t, `
[a, ...rest] := []
out = [a, rest]
`, nil, ARR{tengo.UndefinedValue, ARR{}})

	expectRun(t, `
[a = 1, ...rest] := []
out = [a, rest]
`, nil, ARR{1, ARR{}})

	expectRun(t, `
[a, [b, ...rest]] := [1, [2, 3, 4]]
out = [a, b, rest]
`, nil, ARR{1, 2, ARR{3, 4}})

	expectCompileError(t, `[a, ...b, c] := [1, 2, 3]`, "rest element must be last")
	expectCompileError(t, `[...a, b] := [1, 2]`, "rest element must be last")
	expectCompileError(t, `func(...a, b) { return a }`, "rest element must be last")
	expectCompileError(t, `func(a, ...b, c) { return b }`, "rest element must be last")
	expectCompileError(t, `{...a} := {a: 1}`, "rest element")
}

func TestDestructureMaps(t *testing.T) {
	expectRun(t, `
{x, y} := {x: 1, y: 2}
out = x + y
`, nil, 3)

	expectRun(t, `
{x: a, y: b} := {x: 1, y: 2}
out = a + b
`, nil, 3)

	expectRun(t, `
{x: a = 50} := {}
out = a
`, nil, 50)

	expectRun(t, `
{x: a = 50} := {x: 7}
out = a
`, nil, 7)

	expectRun(t, `
{x: a = 50} := {x: undefined}
out = a
`, nil, tengo.UndefinedValue)

	expectRun(t, `
{x, y: z = x} := {x: 4}
out = z
`, nil, 4)

	expectRun(t, `
{x = 8, y = x} := {}
out = x + y
`, nil, 16)

	expectRun(t, `
{"a-b": v} := {"a-b": 7}
out = v
`, nil, 7)

	expectRun(t, `
{a} := immutable({a: 6})
out = a
`, nil, 6)

	expectRun(t, `
{a, b} := {a: 1}
out = [a, b]
`, nil, ARR{1, tengo.UndefinedValue})
}

func TestDestructureNested(t *testing.T) {
	expectRun(t, `
[a, [b, {c}]] := [1, [2, {c: 3}]]
out = a + b + c
`, nil, 6)

	expectRun(t, `
[[a = 1, b = 2]] := []
out = a + b
`, nil, 3)

	expectRun(t, `
[[a = 1]] := [[undefined]]
out = a
`, nil, tengo.UndefinedValue)

	expectRun(t, `
{a: {b: c = 5}} := {}
out = c
`, nil, 5)

	expectRun(t, `
n := 0
for [i] := [0]; i < 3; i++ {
	n += i
}
out = n
`, nil, 3)

	expectRun(t, `
out = 0
if [a] := [2]; a == 2 {
	out = a
}
`, nil, 2)
}

func TestDestructureLazyDefaults(t *testing.T) {
	expectRun(t, `
n := 0
f := func(x) { n++; return x }
[a = f(1), b = f(2)] := [9]
out = [a, b, n]
`, nil, ARR{9, 2, 1})

	expectRun(t, `
n := 0
f := func(x) { n++; return x }
[a = f(1), b = f(2)] := []
out = [a, b, n]
`, nil, ARR{1, 2, 2})

	expectRun(t, `
n := 0
f := func() { n++; return [] }
[] := f()
out = n
`, nil, 1)

	expectRun(t, `
b := 5
[a = b] := []
out = a
`, nil, 5)

	expectRun(t, `
b := 5
if true {
	[a = b, b = 1] := []
	out = a*10 + b
}
`, nil, 51)
}

func TestDestructureAssignRejected(t *testing.T) {
	expectCompileError(t, `[a, b] = [1, 2]`, "cannot use destructuring with =")
	expectCompileError(t, `{x} = {x: 1}`, "cannot use destructuring with =")
	expectCompileError(t, `{x: a} = {x: 1}`, "cannot use destructuring with =")
	expectCompileError(t, `{x: a = 50} = {}`, "cannot use destructuring with =")
	expectCompileError(t, `
a := 1
[a] := [2]
`, "redeclared")
}

func TestDestructureFuncParams(t *testing.T) {
	expectRun(t, `
f := func([a, b], {x: c = 10}) {
	return a + b + c
}
out = f([1, 2], {}) + f([1, 2], {x: 3})
`, nil, 19)

	expectRun(t, `
f := func(a = 1, b = a + 2) { return a + b }
out = f() + f(5) + f(5, 6)
`, nil, 27)

	expectRun(t, `
f := func(a, b = 1) { return a }
out = f()
`, nil, tengo.UndefinedValue)

	expectRun(t, `
f := func([a, ...rest]) { return rest }
out = f([1, 2, 3])
`, nil, ARR{2, 3})

	expectRun(t, `
f := func(a, b = a) { return b }
out = f(4) + f(4, 9)
`, nil, 13)

	expectRun(t, `
f := func([[a, b], c]) { return a + b + c }
out = f([[1, 2], 3])
`, nil, 6)

	expectRun(t, `
n := 0
inc := func() { n++; return 5 }
f := func(a = inc()) { return a }
x := f(1)
y := f()
out = [x, y, n]
`, nil, ARR{1, 5, 1})

	expectRun(t, `
f := func(x) {
	return func(a = x) { return a }
}
out = f(7)() + f(7)(3)
`, nil, 10)

	expectRun(t, `
f := func({x, y: z = 3}) { return x + z }
out = f({x: 4}) + f({x: 1, y: 2})
`, nil, 10)

	expectRun(t, `
f := func({x} = {x: 5}) { return x }
out = f() + f({x: 1})
`, nil, 6)

	expectRun(t, `
f := func([a] = [2]) { return a }
out = [f(), f([9]), f([])]
`, nil, ARR{2, 9, tengo.UndefinedValue})

	expectRun(t, `
f := func(a, {b: c = a}) { return c }
out = f(8, {})
`, nil, 8)

	expectRun(t, `
a := 1
f := func() {
	[a] := [2]
	return a
}
out = f() + a
`, nil, 3)

	expectRun(t, `
f := func(a = 1, ...rest) { return [a, rest] }
out = f()
`, nil, ARR{1, ARR{}})

	expectRun(t, `
f := func(a = 1, ...rest) { return [a, rest] }
out = f(2, 3, 4)
`, nil, ARR{2, ARR{3, 4}})

	expectRun(t, `
f := func(a = 1, b = 2, ...rest) { return [a, b, rest] }
out = f(9)
`, nil, ARR{9, 2, ARR{}})

	expectRun(t, `
f := func(n = 0) {
	if n == 0 { return 1 }
	return n * f(n-1)
}
out = f(4)
`, nil, 24)

	expectRun(t, `
f := func(n, acc = 0) {
	if n == 0 { return acc }
	return f(n-1, acc+n)
}
out = f(3)
`, nil, 6)

	expectError(t, `
f := func([a]) { return a }
f()
`, nil, "wrong number of arguments")

	expectError(t, `[a] := 1`, nil, "not an array")
	expectError(t, `{a} := [1]`, nil, "not a map")
	expectError(t, `[a] := {a: 1}`, nil, "not an array")

	expectRun(t, `
[a] := [1]
a = a + 2
out = a
`, nil, 3)

	expectRun(t, `
[a, b = a > 0 && a < 10 ? a : 0] := [4]
out = b
`, nil, 4)

	expectRun(t, `
[a, b = a > 0 && a < 10 ? a : 0] := [0]
out = b
`, nil, 0)

	expectRun(t, `
[[a] = [9]] := []
out = a
`, nil, 9)

	expectRun(t, `
[[a] = [9]] := [[3]]
out = a
`, nil, 3)

	expectRun(t, `
f := func() {
	[a] := [7]
	return func() { return a }
}
out = f()()
`, nil, 7)

	expectRun(t, `
[a = 1, b = func() { return a }()] := []
out = b
`, nil, 1)

	expectRun(t, `
n := 0
f := func() { n++; return 1 }
{a = f(), b = f()} := {b: 2}
out = [a, b, n]
`, nil, ARR{1, 2, 1})

	expectRun(t, `
[a] := undefined
out = a
`, nil, tengo.UndefinedValue)

	expectCompileError(t, `[[...a, b]] := [[1, 2]]`, "rest element must be last")
	expectCompileError(t, `{a: [...b, c]} := {a: [1]}`, "rest element must be last")
}
