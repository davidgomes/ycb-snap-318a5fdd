package tengo_test

import (
	"testing"
)

func TestDestructuring(t *testing.T) {
	// Array patterns bind by position. Missing positions are undefined.
	expectRun(t, `
	[a, b, c] := [1, 2]
	out = [a, b, is_undefined(c)]
	`, nil, ARR{1, 2, true})

	expectRun(t, `
	[a, b] := [1]
	out = [a, is_undefined(b)]
	`, nil, ARR{1, true})

	// Explicit undefined is present, so the default is not used.
	expectRun(t, `
	[a = 5] := [undefined]
	out = is_undefined(a)
	`, nil, true)

	// Defaults apply only when the position does not exist.
	expectRun(t, `
	[a = 4 + 5, b = 3] := []
	out = [a, b]
	`, nil, ARR{9, 3})

	expectRun(t, `
	[a = 1/0] := [3]
	out = a
	`, nil, 3)

	// Defaults can read bindings established earlier in the same operation.
	expectRun(t, `
	[a, b = a + 10, c = b + 1] := [1]
	out = [a, b, c]
	`, nil, ARR{1, 11, 12})

	expectRun(t, `
	[a = 3, b = a * 2] := []
	out = [a, b]
	`, nil, ARR{3, 6})

	// The source is evaluated once.
	expectRun(t, `
	n := 0
	f := func() { n++; return [1, 2] }
	[a, b] := f()
	out = [n, a, b]
	`, nil, ARR{1, 1, 2})

	// Blank discards a position.
	expectRun(t, `
	[_, b, _] := [1, 2, 3]
	out = b
	`, nil, 2)

	// Rest collects the tail and must be a fresh array.
	expectRun(t, `
	[a, ...rest] := [1, 2, 3, 4]
	out = [a, rest]
	`, nil, ARR{1, ARR{2, 3, 4}})

	expectRun(t, `
	[a, ...rest] := [1]
	out = [a, rest, is_undefined(a)]
	`, nil, ARR{1, ARR{}, false})

	expectRun(t, `
	[...rest] := [1, 2]
	out = rest
	`, nil, ARR{1, 2})

	expectRun(t, `
	[...rest] := undefined
	out = rest
	`, nil, ARR{})

	expectRun(t, `
	[a, b, ...rest] := [1]
	out = [a, is_undefined(b), rest]
	`, nil, ARR{1, true, ARR{}})

	// Nested array patterns, including rest and defaults on a missing inner value.
	expectRun(t, `
	[[a, b = 9], c] := [[1], 3]
	out = [a, b, c]
	`, nil, ARR{1, 9, 3})

	expectRun(t, `
	[a, [b, ...rest]] := [1, [2, 3, 4]]
	out = [a, b, rest]
	`, nil, ARR{1, 2, ARR{3, 4}})

	expectRun(t, `
	[[a = 1, ...rest]] := undefined
	out = [a, rest]
	`, nil, ARR{1, ARR{}})

	// Map shorthand, renaming, and defaults.
	expectRun(t, `
	{x, y: a, z: b = 50} := {x: 1, y: 2}
	out = [x, a, b]
	`, nil, ARR{1, 2, 50})

	expectRun(t, `
	{x = 7} := {}
	out = x
	`, nil, 7)

	expectRun(t, `
	{x: a = 50} := {}
	out = a
	`, nil, 50)

	expectRun(t, `
	{x: a = 50} := {x: 8}
	out = a
	`, nil, 8)

	expectRun(t, `
	{"a-b": v = 3} := {}
	out = v
	`, nil, 3)

	expectRun(t, `
	{"a-b": w} := {"a-b": 4}
	out = w
	`, nil, 4)

	expectRun(t, `
	{a = 5} := {a: undefined}
	out = is_undefined(a)
	`, nil, true)

	expectRun(t, `
	{a = 5} := {}
	out = a
	`, nil, 5)

	expectRun(t, `
	{b, a} := {a: 1, b: 2, c: 3}
	out = [a, b]
	`, nil, ARR{1, 2})

	expectRun(t, `
	{x, x: y} := {x: 4}
	out = [x, y]
	`, nil, ARR{4, 4})

	expectRun(t, `
	{x = 10, y = x + 1} := {}
	out = [x, y]
	`, nil, ARR{10, 11})

	expectRun(t, `
	{x = 10, y = x + 1} := {y: 3}
	out = [x, y]
	`, nil, ARR{10, 3})

	// Nested map and mixed patterns.
	expectRun(t, `
	{user: {name, age = 0}} := {user: {name: "ada"}}
	out = [name, age]
	`, nil, ARR{"ada", 0})

	expectRun(t, `
	{user: {name = "x"}} := {}
	out = name
	`, nil, "x")

	expectRun(t, `
	{u: {n = "z"}} := undefined
	out = n
	`, nil, "z")

	expectRun(t, `
	[{a, b = 4}] := [{a: 1}]
	out = [a, b]
	`, nil, ARR{1, 4})

	expectRun(t, `
	{p: [a, b = 2]} := {p: [7]}
	out = [a, b]
	`, nil, ARR{7, 2})

	// Empty patterns are valid and leave the surrounding scope alone.
	expectRun(t, `
	[] := [1, 2]
	{} := {a: 1}
	[] := undefined
	{} := undefined
	out = 1
	`, nil, 1)

	// Immutable sources use the same position and key rules.
	expectRun(t, `
	[a, ...rest] := immutable([1, 2, 3])
	out = [a, rest]
	`, nil, ARR{1, ARR{2, 3}})

	expectRun(t, `
	{k} := immutable({k: 8, z: 9})
	out = k
	`, nil, 8)

	// Patterns in function parameters, including defaults and nesting.
	expectRun(t, `
	out = func([a, b = 2], {x: y = 5}) { return a + b + y }([10], {})
	`, nil, 17)

	expectRun(t, `
	out = func({x, y}) { return x - y }({x: 5, y: 2})
	`, nil, 3)

	expectRun(t, `
	out = func([a, [b, c = 4]]) { return a + b + c }([1, [2]])
	`, nil, 7)

	expectRun(t, `
	out = func([], {}) { return 1 }([9], {z: 1})
	`, nil, 1)

	expectRun(t, `
	out = func(a, [b = a + 1]) { return b }(6, [])
	`, nil, 7)

	expectRun(t, `
	out = func([a, b = a * 2]) { return b }([4])
	`, nil, 8)

	expectRun(t, `
	out = func({p: [a, b = 2]}) { return a + b }({p: [7]})
	`, nil, 9)

	expectRun(t, `
	out = func([a], ...xs) { return a + len(xs) }([3], 1, 2)
	`, nil, 5)

	// Closures see bindings established by the destructuring.
	expectRun(t, `
	out = func() {
		[a] := [6]
		return func() { return a }()
	}()
	`, nil, 6)

	expectRun(t, `
	out = func() {
		[a, b = func() { return a + 1 }] := [4]
		return b()
	}()
	`, nil, 5)

	// A nested block does not assign the outer name.
	expectRun(t, `
	out = func() {
		x := 1
		if true {
			[x] := [5]
			if x != 5 {
				return -1
			}
		}
		return x
	}()
	`, nil, 1)

	// for/if inits accept both array and map patterns.
	expectRun(t, `
	s := 0
	for [i] := [1]; i < 4; i++ {
		s += i
	}
	out = s
	`, nil, 6)

	expectRun(t, `
	n := 0
	m := {x: 0}
	for {x} := m; x < 3; x++ {
		n += x
	}
	out = n
	`, nil, 3)

	expectRun(t, `
	m := {x: 4}
	r := 0
	if {x} := m; x > 0 {
		r = x
	}
	out = r
	`, nil, 4)

	expectRun(t, `
	s := 0
	arr := [[1], [2], [3]]
	for i := 0; i < len(arr); i++ {
		[v] := arr[i]
		s += v
	}
	out = s
	`, nil, 6)

	// Newlines and comments inside a pattern.
	expectRun(t, `
	[
		a, // first
		b = 2] := [1]
	out = [a, b]
	`, nil, ARR{1, 2})

	// Existing literal and assignment syntax is unchanged.
	expectRun(t, `out = [1, 2, 3][1]`, nil, 2)
	expectRun(t, `out = {a: 1, b: 2}.b`, nil, 2)
	expectRun(t, `
	a := [1, 2]
	a[0] = 3
	out = a[0]
	`, nil, 3)
	expectRun(t, `
	m := {a: 1}
	m.a = 2
	out = m.a
	`, nil, 2)

	// Compile-time errors.
	expectError(t, `[a, ...rest, b] := [1, 2, 3]`,
		nil, "rest element must be last")
	expectError(t, `[[...rest, b]] := [[1, 2]]`,
		nil, "rest element must be last")
	expectError(t, `[a] = [1]`,
		nil, "cannot use destructuring with =")
	expectError(t, `{a} = {a: 1}`,
		nil, "cannot use destructuring with =")
	expectError(t, `{a: b} = {a: 1}`,
		nil, "cannot use destructuring with =")
	expectError(t, `{...rest} := {a: 1}`,
		nil, "rest element not allowed in map pattern")
	expectError(t, `
	a := 1
	[a] := [2]
	`, nil, "redeclared in this block")
	expectError(t, `[...rest = 1] := [1, 2]`,
		nil, "rest element cannot have a default")

	// Wrong container types are runtime errors.
	expectError(t, `[a] := 1`, nil, "not indexable")
	expectError(t, `{a} := 1`, nil, "not indexable")
	expectError(t, `[a] := {a: 1}`, nil, "not indexable")
	expectError(t, `{a} := [1]`, nil, "not indexable")
	expectError(t, `func([a]) { return a }(1)`, nil, "not indexable")
}
