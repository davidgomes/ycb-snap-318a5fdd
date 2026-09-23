package tengo_test

import (
	"strings"
	"testing"

	"github.com/d5/tengo/v2"
	"github.com/d5/tengo/v2/require"
	"github.com/d5/tengo/v2/stdlib"
)

func mustRun(t *testing.T, src string, imports *tengo.ModuleMap) *tengo.Compiled {
	t.Helper()
	s := tengo.NewScript([]byte(src))
	if imports != nil {
		s.SetImports(imports)
	}
	c, err := s.Run()
	require.NoError(t, err)
	return c
}

func fnOf(t *testing.T, c *tengo.Compiled, name string) *tengo.CompiledFunction {
	t.Helper()
	obj := c.Get(name).Object()
	fn, ok := obj.(*tengo.CompiledFunction)
	require.True(t, ok, "got %T", obj)
	require.True(t, fn.CanCall())
	return fn
}

func intOf(o tengo.Object) int64 {
	return o.(*tengo.Int).Value
}

func TestCompiledFunctionCall(t *testing.T) {
	c := mustRun(t, `
g := 10
f := func(x) { return x + g }
`, nil)
	ret, err := fnOf(t, c, "f").Call(&tengo.Int{Value: 5})
	require.NoError(t, err)
	require.Equal(t, int64(15), intOf(ret))

	// mutation of globals is visible on the compiled instance
	c = mustRun(t, `
g := 1
f := func() { g++; return g }
`, nil)
	ret, err = fnOf(t, c, "f").Call()
	require.NoError(t, err)
	require.Equal(t, int64(2), intOf(ret))
	require.Equal(t, int64(2), c.Get("g").Int64())
}

func TestCompiledFunctionCallClosureAndReturn(t *testing.T) {
	c := mustRun(t, `
g := 3
make := func(x) {
	return func(y) { return x + y + g }
}
`, nil)
	ret, err := fnOf(t, c, "make").Call(&tengo.Int{Value: 2})
	require.NoError(t, err)
	cl, ok := ret.(*tengo.CompiledFunction)
	require.True(t, ok)
	ret, err = cl.Call(&tengo.Int{Value: 4})
	require.NoError(t, err)
	require.Equal(t, int64(9), intOf(ret))

	// composite return stays callable
	c = mustRun(t, `
box := func() {
	return { inc: func(x) { return x + 1 }, arr: [func(x) { return x * 2 }] }
}
`, nil)
	ret, err = fnOf(t, c, "box").Call()
	require.NoError(t, err)
	m := ret.(*tengo.Map)
	ret, err = m.Value["inc"].(*tengo.CompiledFunction).Call(&tengo.Int{Value: 10})
	require.NoError(t, err)
	require.Equal(t, int64(11), intOf(ret))
	arr := m.Value["arr"].(*tengo.Array)
	ret, err = arr.Value[0].(*tengo.CompiledFunction).Call(&tengo.Int{Value: 10})
	require.NoError(t, err)
	require.Equal(t, int64(20), intOf(ret))
}

func TestCompiledFunctionCallVariadicRecursive(t *testing.T) {
	c := mustRun(t, `
f := func(a, ...b) { return [a, b] }
fact := func(x) {
	if x <= 1 { return 1 }
	return x * fact(x - 1)
}
`, nil)
	ret, err := fnOf(t, c, "f").Call(&tengo.Int{Value: 1})
	require.NoError(t, err)
	arr := ret.(*tengo.Array)
	require.Equal(t, int64(1), intOf(arr.Value[0]))
	require.Equal(t, 0, len(arr.Value[1].(*tengo.Array).Value))

	ret, err = fnOf(t, c, "f").Call(
		&tengo.Int{Value: 1}, &tengo.Int{Value: 2}, &tengo.Int{Value: 3})
	require.NoError(t, err)
	arr = ret.(*tengo.Array)
	require.Equal(t, int64(2), intOf(arr.Value[1].(*tengo.Array).Value[0]))

	_, err = fnOf(t, c, "f").Call()
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "Runtime Error: wrong number of arguments: want>=1, got=0"), err)
	require.True(t, strings.Contains(err.Error(), "\n\tat "), err)

	ret, err = fnOf(t, c, "fact").Call(&tengo.Int{Value: 5})
	require.NoError(t, err)
	require.Equal(t, int64(120), intOf(ret))

	c = mustRun(t, `
fact := func(x, acc) {
	if x <= 1 { return acc }
	return fact(x-1, x*acc)
}
`, nil)
	ret, err = fnOf(t, c, "fact").Call(&tengo.Int{Value: 5}, &tengo.Int{Value: 1})
	require.NoError(t, err)
	require.Equal(t, int64(120), intOf(ret))

	// closure recursion
	c = mustRun(t, `
fact := func() {
	f := func(x) {
		if x <= 1 { return 1 }
		return x * f(x - 1)
	}
	return f
}()
`, nil)
	ret, err = fnOf(t, c, "fact").Call(&tengo.Int{Value: 6})
	require.NoError(t, err)
	require.Equal(t, int64(720), intOf(ret))
}

func TestCompiledFunctionCallNestedAndModule(t *testing.T) {
	c := mustRun(t, `
g := 1
funcs := { a: [func(x) { return x + g }] }
`, nil)
	arr := c.Get("funcs").Object().(*tengo.Map).Value["a"].(*tengo.Array)
	ret, err := arr.Value[0].(*tengo.CompiledFunction).Call(&tengo.Int{Value: 4})
	require.NoError(t, err)
	require.Equal(t, int64(5), intOf(ret))

	mods := tengo.NewModuleMap()
	mods.AddSourceModule("double", []byte(`export func(x) { return x * 2 }`))
	c = mustRun(t, `d := import("double")`, mods)
	ret, err = fnOf(t, c, "d").Call(&tengo.Int{Value: 21})
	require.NoError(t, err)
	require.Equal(t, int64(42), intOf(ret))

	mods = stdlib.GetModuleMap("math")
	c = mustRun(t, `
f := func(x) {
	m := import("math")
	return m.abs(x)
}
`, mods)
	ret, err = fnOf(t, c, "f").Call(&tengo.Float{Value: -3.5})
	require.NoError(t, err)
	require.Equal(t, 3.5, ret.(*tengo.Float).Value)
}

func TestCompiledFunctionCallFromGoCallback(t *testing.T) {
	s := tengo.NewScript([]byte(`
g := 7
out := apply(func(x) { return x + g }, 5)
`))
	err := s.Add("apply", func(args ...tengo.Object) (tengo.Object, error) {
		require.True(t, args[0].CanCall())
		return args[0].Call(args[1])
	})
	require.NoError(t, err)
	c, err := s.Run()
	require.NoError(t, err)
	require.Equal(t, int64(12), c.Get("out").Int64())
}

func TestCompiledFunctionCallErrorFormat(t *testing.T) {
	c := mustRun(t, `
f := func() { return 1 + "a" }
`, nil)
	_, err := fnOf(t, c, "f").Call()
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "Runtime Error: invalid operation: int + string"), err)
	require.True(t, strings.Contains(err.Error(), "\n\tat (main):"), err)

	// nested call stack includes the callee and the caller frame
	c = mustRun(t, `
inner := func() { return 1 + "a" }
outer := func() { return inner() }
`, nil)
	_, err = fnOf(t, c, "outer").Call()
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "Runtime Error: invalid operation: int + string"), err)
	require.True(t, strings.Count(err.Error(), "\n\tat ") >= 2, err)
}

func TestCompiledCloneIsolatesCallables(t *testing.T) {
	c := mustRun(t, `
g := 1
f := func() { g++; return g }
mk := func() {
	x := 0
	return func() { x++; return x }
}
inc := mk()
`, nil)
	clone := c.Clone()

	ret, err := fnOf(t, c, "f").Call()
	require.NoError(t, err)
	require.Equal(t, int64(2), intOf(ret))
	ret, err = fnOf(t, clone, "f").Call()
	require.NoError(t, err)
	require.Equal(t, int64(2), intOf(ret))
	require.Equal(t, int64(2), c.Get("g").Int64())
	require.Equal(t, int64(2), clone.Get("g").Int64())

	ret, err = fnOf(t, c, "inc").Call()
	require.NoError(t, err)
	require.Equal(t, int64(1), intOf(ret))
	ret, err = fnOf(t, clone, "inc").Call()
	require.NoError(t, err)
	require.Equal(t, int64(1), intOf(ret))
	ret, err = fnOf(t, c, "inc").Call()
	require.NoError(t, err)
	require.Equal(t, int64(2), intOf(ret))
}

func TestCompiledSetIsolatesTransferredClosure(t *testing.T) {
	src := `
g := 10
f := func() { g++; return g }
mk := func() {
	x := 5
	return func() { x++; return x }
}
inc := mk()
box := { inc: inc, nested: [inc] }
`
	c1 := mustRun(t, src, nil)
	c2 := mustRun(t, src, nil)

	// mutate the capture before transfer
	ret, err := fnOf(t, c1, "inc").Call()
	require.NoError(t, err)
	require.Equal(t, int64(6), intOf(ret))

	require.NoError(t, c2.Set("f", c1.Get("f").Object()))
	require.NoError(t, c2.Set("inc", c1.Get("inc").Object()))
	require.NoError(t, c2.Set("box", c1.Get("box").Object()))

	ret, err = fnOf(t, c2, "f").Call()
	require.NoError(t, err)
	require.Equal(t, int64(11), intOf(ret))
	require.Equal(t, int64(10), c1.Get("g").Int64())
	require.Equal(t, int64(11), c2.Get("g").Int64())

	// destination sees the capture as it was at transfer (6), then increments
	ret, err = fnOf(t, c2, "inc").Call()
	require.NoError(t, err)
	require.Equal(t, int64(7), intOf(ret))
	ret, err = fnOf(t, c1, "inc").Call()
	require.NoError(t, err)
	require.Equal(t, int64(7), intOf(ret))

	// nested callables inside the transferred map/array were rebound too.
	// box was transferred separately from inc, so it keeps the capture
	// value from transfer time (6), and functions that shared a cell
	// inside that value still share it.
	box := c2.Get("box").Object().(*tengo.Map)
	ret, err = box.Value["inc"].(*tengo.CompiledFunction).Call()
	require.NoError(t, err)
	require.Equal(t, int64(7), intOf(ret))
	nested := box.Value["nested"].(*tengo.Array)
	ret, err = nested.Value[0].(*tengo.CompiledFunction).Call()
	require.NoError(t, err)
	require.Equal(t, int64(8), intOf(ret))

	// source capture was not moved by destination calls
	ret, err = fnOf(t, c1, "inc").Call()
	require.NoError(t, err)
	require.Equal(t, int64(8), intOf(ret))

	// globals inside a nested transferred callable resolve on the destination
	c1 = mustRun(t, `
g := 1
box := { add: func() { g += 5; return g } }
`, nil)
	c2 = mustRun(t, `
g := 100
box := undefined
`, nil)
	require.NoError(t, c2.Set("box", c1.Get("box").Object()))
	box = c2.Get("box").Object().(*tengo.Map)
	ret, err = box.Value["add"].(*tengo.CompiledFunction).Call()
	require.NoError(t, err)
	require.Equal(t, int64(105), intOf(ret))
	require.Equal(t, int64(1), c1.Get("g").Int64())
	require.Equal(t, int64(105), c2.Get("g").Int64())

	// Script.Add of a closure also snapshots captures onto the new instance.
	c1 = mustRun(t, `
mk := func() {
	x := 1
	return func() { x++; return x }
}
inc := mk()
`, nil)
	_, err = fnOf(t, c1, "inc").Call()
	require.NoError(t, err)
	s := tengo.NewScript([]byte(`out := inc()`))
	require.NoError(t, s.Add("inc", c1.Get("inc").Object()))
	added, err := s.Run()
	require.NoError(t, err)
	require.Equal(t, int64(3), added.Get("out").Int64())
	ret, err = fnOf(t, c1, "inc").Call()
	require.NoError(t, err)
	require.Equal(t, int64(3), intOf(ret))
}
