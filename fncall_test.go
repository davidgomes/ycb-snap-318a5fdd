package tengo_test

import (
	"strings"
	"testing"

	"github.com/d5/tengo/v2"
	"github.com/d5/tengo/v2/require"
	"github.com/d5/tengo/v2/stdlib"
)

func compileRun(t *testing.T, src string, mods tengo.ModuleGetter) *tengo.Compiled {
	t.Helper()
	s := tengo.NewScript([]byte(src))
	if mods != nil {
		s.SetImports(mods)
	}
	c, err := s.Run()
	require.NoError(t, err)
	return c
}

func mustCompiledFn(t *testing.T, obj tengo.Object) *tengo.CompiledFunction {
	t.Helper()
	fn, ok := obj.(*tengo.CompiledFunction)
	require.True(t, ok, "got %T", obj)
	require.True(t, fn.CanCall())
	return fn
}

func globalFn(t *testing.T, c *tengo.Compiled, name string) *tengo.CompiledFunction {
	t.Helper()
	return mustCompiledFn(t, c.Get(name).Object())
}

func callInt(t *testing.T, fn *tengo.CompiledFunction, args ...tengo.Object) int64 {
	t.Helper()
	ret, err := fn.Call(args...)
	require.NoError(t, err)
	n, ok := tengo.ToInt64(ret)
	require.True(t, ok, "got %v (%T)", ret, ret)
	return n
}

func intObj(n int64) tengo.Object {
	return &tengo.Int{Value: n}
}

func TestCompiledFunctionCall(t *testing.T) {
	c := compileRun(t, `
glob := 10
add := func(a, b) { return a + b + glob }
inc := func() { glob++; return glob }
nested := {fns: [func(x) { return x + glob }]}
id := func(x) { return x }
nothing := func() {}
`, nil)

	add := globalFn(t, c, "add")
	require.Equal(t, int64(15), callInt(t, add, intObj(2), intObj(3)))

	inc := globalFn(t, c, "inc")
	require.Equal(t, int64(11), callInt(t, inc))
	require.Equal(t, 11, c.Get("glob").Int())
	require.Equal(t, int64(16), callInt(t, add, intObj(2), intObj(3)))

	bag := c.Get("nested").Object().(*tengo.Map)
	inner := bag.Value["fns"].(*tengo.Array)
	nested := mustCompiledFn(t, inner.Value[0])
	require.Equal(t, int64(14), callInt(t, nested, intObj(3)))

	id := globalFn(t, c, "id")
	ret, err := id.Call(tengo.UndefinedValue)
	require.NoError(t, err)
	require.Equal(t, tengo.UndefinedValue, ret)

	ret, err = globalFn(t, c, "nothing").Call()
	require.NoError(t, err)
	require.Equal(t, tengo.UndefinedValue, ret)

	_, err = add.Call(intObj(1))
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "Runtime Error: wrong number of arguments: want=2, got=1"), err.Error())
	require.True(t, strings.Contains(err.Error(), "\n\tat "), err.Error())
}

func TestCompiledFunctionVariadicAndRecursion(t *testing.T) {
	c := compileRun(t, `
sum := func(a, ...b) {
	s := a
	for x in b { s += x }
	return s
}
fib := func(n) {
	if n < 2 { return n }
	return fib(n-1) + fib(n-2)
}
iter := func(n, max) {
	if n == max { return n }
	return iter(n+1, max)
}
self := func(n) {
	if n == 0 { return 0 }
	f := func(n) { return self(n) }
	return f(n-1) + 1
}
`, nil)

	sum := globalFn(t, c, "sum")
	require.Equal(t, int64(10), callInt(t, sum, intObj(1), intObj(2), intObj(3), intObj(4)))
	require.Equal(t, int64(1), callInt(t, sum, intObj(1)))
	_, err := sum.Call()
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "wrong number of arguments: want>=1, got=0"), err.Error())

	require.Equal(t, int64(55), callInt(t, globalFn(t, c, "fib"), intObj(10)))
	require.Equal(t, int64(5000), callInt(t, globalFn(t, c, "iter"), intObj(0), intObj(5000)))
	require.Equal(t, int64(4), callInt(t, globalFn(t, c, "self"), intObj(4)))
}

func TestCompiledFunctionImportsAndCallbacks(t *testing.T) {
	stdlibMods := stdlib.GetModuleMap("math")
	stdlibMods.AddSourceModule("lib", []byte(`
n := 1
export {
	add: func(x) { return x + n },
	bump: func() { n++; return n }
}
`))

	c := compileRun(t, `
math := import("math")
lib := import("lib")
abs := func(x) { return math.abs(x) }
use := func(x) { return lib.add(x) }
`, stdlibMods)

	abs := globalFn(t, c, "abs")
	ret, err := abs.Call(&tengo.Float{Value: -3.5})
	require.NoError(t, err)
	f, ok := tengo.ToFloat64(ret)
	require.True(t, ok)
	require.Equal(t, 3.5, f)

	use := globalFn(t, c, "use")
	require.Equal(t, int64(8), callInt(t, use, intObj(7)))

	lib := c.Get("lib").Object().(*tengo.ImmutableMap)
	add := mustCompiledFn(t, lib.Value["add"])
	bump := mustCompiledFn(t, lib.Value["bump"])
	require.Equal(t, int64(6), callInt(t, add, intObj(5)))
	require.Equal(t, int64(2), callInt(t, bump))
	require.Equal(t, int64(7), callInt(t, add, intObj(5)))

	s := tengo.NewScript([]byte(`
glob := 1
f := func(x) { glob += x; return glob }
out := apply(f, 4)
after := glob
cb := func(fn) { return fn() + glob }
out2 := cb(func() { glob++; return glob })
`))
	require.NoError(t, s.Add("apply", func(args ...tengo.Object) (tengo.Object, error) {
		if len(args) == 0 || !args[0].CanCall() {
			return nil, tengo.ErrWrongNumArguments
		}
		return args[0].Call(args[1:]...)
	}))
	compiled, err := s.Run()
	require.NoError(t, err)
	require.Equal(t, 5, compiled.Get("out").Int())
	require.Equal(t, 5, compiled.Get("after").Int())
	require.Equal(t, 12, compiled.Get("out2").Int())
}

func TestCompiledFunctionReturnsCallable(t *testing.T) {
	c := compileRun(t, `
make := func(x) {
	return func(y) { x += y; return x }
}
both := func() {
	n := 0
	return {
		inc: func() { n++; return n },
		get: func() { return n },
		arr: [func() { n += 10; return n }]
	}
}
`, nil)

	make := globalFn(t, c, "make")
	ret, err := make.Call(intObj(10))
	require.NoError(t, err)
	clos := mustCompiledFn(t, ret)
	require.Equal(t, int64(11), callInt(t, clos, intObj(1)))
	require.Equal(t, int64(13), callInt(t, clos, intObj(2)))

	ret, err = globalFn(t, c, "both").Call()
	require.NoError(t, err)
	m := ret.(*tengo.Map)
	inc := mustCompiledFn(t, m.Value["inc"])
	get := mustCompiledFn(t, m.Value["get"])
	arr := m.Value["arr"].(*tengo.Array)
	step := mustCompiledFn(t, arr.Value[0])
	require.Equal(t, int64(1), callInt(t, inc))
	require.Equal(t, int64(1), callInt(t, get))
	require.Equal(t, int64(11), callInt(t, step))
	require.Equal(t, int64(11), callInt(t, get))
}

func TestCompiledFunctionRuntimeError(t *testing.T) {
	c := compileRun(t, `
bad := func() { return 1 + "x" }
wrap := func() { return bad() }
`, nil)
	_, err := globalFn(t, c, "bad").Call()
	require.Error(t, err)
	msg := err.Error()
	require.True(t, strings.HasPrefix(msg, "Runtime Error: invalid operation: int + string\n\tat "), msg)
	require.True(t, strings.Contains(msg, "(main):"), msg)

	_, err = globalFn(t, c, "wrap").Call()
	require.Error(t, err)
	msg = err.Error()
	require.True(t, strings.Contains(msg, "Runtime Error: invalid operation: int + string\n\tat "), msg)
	// error frame inside bad, then the call inside wrap
	require.Equal(t, 2, strings.Count(msg, "\n\tat "))

	s := tengo.NewScript([]byte(`
bad := func() { return 1 + "x" }
wrap := func() { return bad() }
wrap()
`))
	_, inScript := s.Run()
	require.Error(t, inScript)
	// in-script trace has one extra frame for the top-level call
	require.True(t, strings.HasPrefix(inScript.Error(), msg+"\n\tat "), inScript.Error())
}

func TestCompiledFunctionCloneAndTransfer(t *testing.T) {
	src := `
glob := 1
factory := func() {
	loc := 10
	return {
		inc: func() { loc++; return loc + glob },
		get: func() { return loc + glob }
	}
}
pair := factory()
pair.inc()
bag := {inner: [pair]}
alias := pair
`
	c1 := compileRun(t, src, nil)
	// loc is 11 after the in-script inc
	require.Equal(t, int64(12), callInt(t, pairFn(t, c1, "get")))

	c2 := c1.Clone()
	// clone sees the capture at clone time and its own global
	require.Equal(t, int64(12), callInt(t, pairFn(t, c2, "get")))
	require.NoError(t, c2.Set("glob", 100))
	require.Equal(t, int64(111), callInt(t, pairFn(t, c2, "get")))
	require.Equal(t, int64(112), callInt(t, pairFn(t, c2, "inc")))
	// source global and capture are unchanged
	require.Equal(t, 1, c1.Get("glob").Int())
	require.Equal(t, int64(12), callInt(t, pairFn(t, c1, "get")))
	// aliased closures inside the clone still share the capture
	require.Equal(t, int64(112), callInt(t, pairFn(t, c2, "aliasGet")))
	require.Equal(t, int64(112), callInt(t, nestedBagFn(t, c2)))

	// source inc does not move the clone
	require.Equal(t, int64(13), callInt(t, pairFn(t, c1, "inc")))
	require.Equal(t, int64(112), callInt(t, pairFn(t, c2, "get")))

	// transfer onto a fresh instance of the same script
	c3 := compileRun(t, src, nil)
	require.NoError(t, c3.Set("glob", 7))
	require.NoError(t, c3.Set("pair", c1.Get("pair").Object()))
	// capture was snapshotted at transfer (loc is 12 after the source inc above)
	// glob resolves against c3
	require.Equal(t, int64(19), callInt(t, pairFn(t, c3, "get")))
	require.Equal(t, int64(20), callInt(t, pairFn(t, c3, "inc")))
	require.Equal(t, int64(13), callInt(t, pairFn(t, c1, "get")))
	require.Equal(t, 1, c1.Get("glob").Int())
	require.Equal(t, 7, c3.Get("glob").Int())

	// nested callables inside the transferred bag are rebound too
	require.NoError(t, c3.Set("bag", c1.Get("bag").Object()))
	require.NoError(t, c3.Set("glob", 40))
	// bag was snapshotted from c1, whose loc is still 12
	require.Equal(t, int64(52), callInt(t, nestedBagFn(t, c3)))
	require.Equal(t, int64(13), callInt(t, pairFn(t, c1, "get")))
	// the earlier pair transfer keeps its own capture (loc 13 after its inc)
	require.Equal(t, int64(53), callInt(t, pairFn(t, c3, "get")))
}

func pairFn(t *testing.T, c *tengo.Compiled, which string) *tengo.CompiledFunction {
	t.Helper()
	if which == "aliasGet" {
		pair := c.Get("alias").Object().(*tengo.Map)
		return mustCompiledFn(t, pair.Value["get"])
	}
	pair := c.Get("pair").Object().(*tengo.Map)
	return mustCompiledFn(t, pair.Value[which])
}

func nestedBagFn(t *testing.T, c *tengo.Compiled) *tengo.CompiledFunction {
	t.Helper()
	bag := c.Get("bag").Object().(*tengo.Map)
	inner := bag.Value["inner"].(*tengo.Array)
	pair, ok := inner.Value[0].(*tengo.Map)
	require.True(t, ok, "got %T", inner.Value[0])
	return mustCompiledFn(t, pair.Value["get"])
}

func TestCompiledFunctionModuleExportIsolation(t *testing.T) {
	mods := tengo.NewModuleMap()
	mods.AddSourceModule("lib", []byte(`
n := 1
export {
	add: func(x) { return x + n },
	bump: func() { n++; return n }
}
`))
	c1 := compileRun(t, `lib := import("lib")`, mods)
	lib1 := c1.Get("lib").Object().(*tengo.ImmutableMap)
	add1 := mustCompiledFn(t, lib1.Value["add"])
	bump1 := mustCompiledFn(t, lib1.Value["bump"])

	c2 := c1.Clone()
	lib2, ok := c2.Get("lib").Object().(*tengo.ImmutableMap)
	require.True(t, ok, "got %T", c2.Get("lib").Object())
	add2 := mustCompiledFn(t, lib2.Value["add"])
	bump2 := mustCompiledFn(t, lib2.Value["bump"])

	require.Equal(t, int64(2), callInt(t, bump2))
	require.Equal(t, int64(7), callInt(t, add2, intObj(5)))
	require.Equal(t, int64(6), callInt(t, add1, intObj(5)))
	require.Equal(t, int64(2), callInt(t, bump1))
	require.Equal(t, int64(7), callInt(t, add2, intObj(5)))

	c3 := compileRun(t, `
glob := 9
lib := import("lib")
`, mods)
	require.NoError(t, c3.Set("lib", c1.Get("lib").Object()))
	lib3, ok := c3.Get("lib").Object().(*tengo.ImmutableMap)
	require.True(t, ok, "got %T", c3.Get("lib").Object())
	// n was 2 after bump1; glob is not closed over by add
	require.Equal(t, int64(7), callInt(t, mustCompiledFn(t, lib3.Value["add"]), intObj(5)))
	require.Equal(t, int64(3), callInt(t, mustCompiledFn(t, lib3.Value["bump"])))
	require.Equal(t, int64(7), callInt(t, add1, intObj(5)))
}

func TestCompiledFunctionCaptureAliasAndReentrancy(t *testing.T) {
	c := compileRun(t, `
inner := {n: 1}
box := {inner: inner}
f := func() { inner.n++; return inner.n }
g := func() { return box.inner.n }
`, nil)
	c2 := c.Clone()
	require.Equal(t, int64(2), callInt(t, globalFn(t, c2, "f")))
	require.Equal(t, int64(2), callInt(t, globalFn(t, c2, "g")))
	require.Equal(t, int64(1), callInt(t, globalFn(t, c, "g")))

	s := tengo.NewScript([]byte(`
loc := 1
f := func() { return loc }
hook(f)
loc = 5
out := f()
deep := func(n) {
	if n == 0 { return 0 }
	return again(deep, n)
}
out2 := deep(4)
`))
	var saved *tengo.CompiledFunction
	require.NoError(t, s.Add("hook", func(args ...tengo.Object) (tengo.Object, error) {
		saved = args[0].(*tengo.CompiledFunction)
		return tengo.UndefinedValue, nil
	}))
	require.NoError(t, s.Add("again", func(args ...tengo.Object) (tengo.Object, error) {
		fn := args[0].(*tengo.CompiledFunction)
		n, _ := tengo.ToInt64(args[1])
		return fn.Call(intObj(n - 1))
	}))
	compiled, err := s.Run()
	require.NoError(t, err)
	require.Equal(t, 5, compiled.Get("out").Int())
	require.Equal(t, int64(5), callInt(t, saved))
	require.Equal(t, 0, compiled.Get("out2").Int())

	mods := stdlib.GetModuleMap()
	mods.AddSourceModule("lib", []byte(`
export {
	add: func(x) { return x + 1 },
	bad: func() { return 1 + "z" }
}
`))
	c3 := compileRun(t, `
f := func(x) {
	lib := import("lib")
	return lib.add(x)
}
bad := func() {
	lib := import("lib")
	return lib.bad()
}
`, mods)
	require.Equal(t, int64(4), callInt(t, globalFn(t, c3, "f"), intObj(3)))
	_, err = globalFn(t, c3, "bad").Call()
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "\n\tat lib:"), err.Error())
}
