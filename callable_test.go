package tengo_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/d5/tengo/v2"
	"github.com/d5/tengo/v2/require"
	"github.com/d5/tengo/v2/stdlib"
)

func compileRun(t *testing.T, src string, mods *tengo.ModuleMap) *tengo.Compiled {
	t.Helper()
	s := tengo.NewScript([]byte(src))
	if mods != nil {
		s.SetImports(mods)
	}
	c, err := s.Run()
	require.NoError(t, err)
	return c
}

func fnOf(t *testing.T, c *tengo.Compiled, name string) *tengo.CompiledFunction {
	t.Helper()
	obj := c.Get(name).Object()
	fn, ok := obj.(*tengo.CompiledFunction)
	require.True(t, ok, "global %s is %T", name, obj)
	require.True(t, fn.CanCall())
	return fn
}

func intResult(t *testing.T, obj tengo.Object) int64 {
	t.Helper()
	n, ok := obj.(*tengo.Int)
	require.True(t, ok, "got %T (%v)", obj, obj)
	return n.Value
}

func TestCompiledFunctionCallBasics(t *testing.T) {
	mods := stdlib.GetModuleMap("text")
	c := compileRun(t, `
text := import("text")
g := 10
add := func(a, b) { return a + b + g }
variadic := func(a, ...rest) { return [a, rest] }
recur := func(n, a, b) {
	if n == 0 { return a }
	return recur(n-1, b, a+b)
}
bad := func() { return 1 + "x" }
inner := func() { return 1 + true }
outer := func() { return inner() }
usesImport := func(s) { return text.contains(s, "ell") }
nested := {
	items: [
		func(x) { return x + g },
		{ deeper: func() { return g } }
	]
}
factory := func() {
	n := 1
	return {
		inc: func() { n++; return n },
		cur: func() { return n },
		both: [func() { return n }]
	}
}
`, mods)

	add := fnOf(t, c, "add")
	got, err := add.Call(&tengo.Int{Value: 2}, &tengo.Int{Value: 3})
	require.NoError(t, err)
	require.Equal(t, int64(15), intResult(t, got))

	_, err = add.Call(&tengo.Int{Value: 1})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "Runtime Error: wrong number of arguments: want=2, got=1"), err.Error())
	require.True(t, strings.Contains(err.Error(), "\n\tat "), err.Error())

	variadic := fnOf(t, c, "variadic")
	got, err = variadic.Call(&tengo.Int{Value: 1})
	require.NoError(t, err)
	require.Equal(t, "[1, []]", got.String())
	got, err = variadic.Call(&tengo.Int{Value: 1}, &tengo.Int{Value: 2}, &tengo.Int{Value: 3})
	require.NoError(t, err)
	require.Equal(t, "[1, [2, 3]]", got.String())
	_, err = variadic.Call()
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "wrong number of arguments: want>=1, got=0"), err.Error())

	recur := fnOf(t, c, "recur")
	got, err = recur.Call(&tengo.Int{Value: 10}, &tengo.Int{Value: 0}, &tengo.Int{Value: 1})
	require.NoError(t, err)
	require.Equal(t, int64(55), intResult(t, got))
	// Tail call must not overflow the frame stack.
	_, err = recur.Call(&tengo.Int{Value: 9999}, &tengo.Int{Value: 0}, &tengo.Int{Value: 1})
	require.NoError(t, err)

	_, err = fnOf(t, c, "bad").Call()
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "Runtime Error: invalid operation: int + string"), err.Error())
	require.True(t, strings.Contains(err.Error(), "(main):"), err.Error())

	_, err = fnOf(t, c, "outer").Call()
	require.Error(t, err)
	require.Equal(t, 2, strings.Count(err.Error(), "\n\tat "))

	got, err = fnOf(t, c, "usesImport").Call(&tengo.String{Value: "hello"})
	require.NoError(t, err)
	require.Equal(t, tengo.TrueValue, got)

	// Globals stay live for later calls.
	require.NoError(t, c.Set("g", 100))
	got, err = add.Call(&tengo.Int{Value: 2}, &tengo.Int{Value: 3})
	require.NoError(t, err)
	require.Equal(t, int64(105), intResult(t, got))

	box := c.Get("nested").Object().(*tengo.Map)
	items := box.Value["items"].(*tengo.Array)
	got, err = items.Value[0].(*tengo.CompiledFunction).Call(&tengo.Int{Value: 1})
	require.NoError(t, err)
	require.Equal(t, int64(101), intResult(t, got))
	deeper := items.Value[1].(*tengo.Map).Value["deeper"].(*tengo.CompiledFunction)
	got, err = deeper.Call()
	require.NoError(t, err)
	require.Equal(t, int64(100), intResult(t, got))

	ret, err := fnOf(t, c, "factory").Call()
	require.NoError(t, err)
	retMap := ret.(*tengo.Map)
	inc := retMap.Value["inc"].(*tengo.CompiledFunction)
	cur := retMap.Value["cur"].(*tengo.CompiledFunction)
	both := retMap.Value["both"].(*tengo.Array).Value[0].(*tengo.CompiledFunction)
	got, err = inc.Call()
	require.NoError(t, err)
	require.Equal(t, int64(2), intResult(t, got))
	got, err = cur.Call()
	require.NoError(t, err)
	require.Equal(t, int64(2), intResult(t, got))
	got, err = both.Call()
	require.NoError(t, err)
	require.Equal(t, int64(2), intResult(t, got))
}

func TestCompiledFunctionSourceModuleAndCallback(t *testing.T) {
	mods := tengo.NewModuleMap()
	mods.AddSourceModule("counter", []byte(`
a := 10
export func(x) {
	a += x
	return a
}
`))
	mods.AddSourceModule("box", []byte(`
export {
	double: func(x) { return x * 2 },
	nested: [func(x) { return x + 1 }]
}
`))

	var fromGo tengo.Object
	var fromGoErr error
	s := tengo.NewScript([]byte(`
counter := import("counter")
box := import("box")
g := 7
out := hook(func(x) { return x + g }, 5)
mk := func(x) { return func() { return x + g } }
out2 := hook(mk(3))
`))
	s.SetImports(mods)
	require.NoError(t, s.Add("hook", &tengo.UserFunction{
		Name: "hook",
		Value: func(args ...tengo.Object) (tengo.Object, error) {
			require.True(t, args[0].CanCall())
			fromGo, fromGoErr = args[0].Call(args[1:]...)
			return fromGo, fromGoErr
		},
	}))
	c, err := s.Run()
	require.NoError(t, err)
	require.NoError(t, fromGoErr)
	require.Equal(t, int64(10), c.Get("out2").Int64())
	require.Equal(t, int64(12), c.Get("out").Int64())

	counter := c.Get("counter").Object().(*tengo.CompiledFunction)
	got, err := counter.Call(&tengo.Int{Value: 1})
	require.NoError(t, err)
	require.Equal(t, int64(11), intResult(t, got))
	got, err = counter.Call(&tengo.Int{Value: 4})
	require.NoError(t, err)
	require.Equal(t, int64(15), intResult(t, got))

	box := c.Get("box").Object().(*tengo.ImmutableMap)
	got, err = box.Value["double"].(*tengo.CompiledFunction).Call(&tengo.Int{Value: 21})
	require.NoError(t, err)
	require.Equal(t, int64(42), intResult(t, got))
	nested := box.Value["nested"].(*tengo.Array)
	got, err = nested.Value[0].(*tengo.CompiledFunction).Call(&tengo.Int{Value: 8})
	require.NoError(t, err)
	require.Equal(t, int64(9), intResult(t, got))

	// A Go callback invoked from inside a compiled function, which then calls
	// back into the script, must see the same globals.
	s = tengo.NewScript([]byte(`
g := 1
id := func(x) { return second(x) + g }
out := hook(id, 4)
`))
	require.NoError(t, s.Add("second", &tengo.UserFunction{
		Name: "second",
		Value: func(args ...tengo.Object) (tengo.Object, error) {
			n := args[0].(*tengo.Int).Value
			return &tengo.Int{Value: n + 1}, nil
		},
	}))
	require.NoError(t, s.Add("hook", &tengo.UserFunction{
		Name: "hook",
		Value: func(args ...tengo.Object) (tengo.Object, error) {
			return args[0].Call(args[1:]...)
		},
	}))
	c, err = s.Run()
	require.NoError(t, err)
	require.Equal(t, int64(6), c.Get("out").Int64())
}

func TestCompiledFunctionCloneAndTransfer(t *testing.T) {
	src := `
g := 10
mk := func() {
	x := 1
	return func(d) { x += d; return x + g }
}
f := mk()
f(4)
arr := [f, { nested: [f] }]
`
	origin := compileRun(t, src, nil)
	originFn := fnOf(t, origin, "f")
	got, err := originFn.Call(&tengo.Int{Value: 1})
	require.NoError(t, err)
	// x was 5 after the script's f(4); this call makes it 6. g is 10.
	require.Equal(t, int64(16), intResult(t, got))

	clone := origin.Clone()
	cloneFn := fnOf(t, clone, "f")
	require.NoError(t, clone.Set("g", 100))
	got, err = cloneFn.Call(&tengo.Int{Value: 1})
	require.NoError(t, err)
	// Capture x is snapshotted at 6; g resolves on the clone.
	require.Equal(t, int64(107), intResult(t, got))
	got, err = originFn.Call(&tengo.Int{Value: 1})
	require.NoError(t, err)
	require.Equal(t, int64(17), intResult(t, got))
	require.Equal(t, int64(10), origin.Get("g").Int64())
	require.Equal(t, int64(100), clone.Get("g").Int64())

	// A second compiled script receives the callable graph.
	destSrc := `
g := 1000
f := undefined
arr := undefined
`
	dest := compileRun(t, destSrc, nil)
	require.NoError(t, dest.Set("arr", origin.Get("arr").Object()))
	arr := dest.Get("arr").Object().(*tengo.Array)
	moved := arr.Value[0].(*tengo.CompiledFunction)
	nested := arr.Value[1].(*tengo.Map).Value["nested"].(*tengo.Array).Value[0].(*tengo.CompiledFunction)

	got, err = moved.Call(&tengo.Int{Value: 1})
	require.NoError(t, err)
	// x was 7 on origin after the previous call. Dest g is 1000.
	require.Equal(t, int64(1008), intResult(t, got))
	got, err = nested.Call(&tengo.Int{Value: 0})
	require.NoError(t, err)
	// The two transferred closures shared the captured cell.
	require.Equal(t, int64(1008), intResult(t, got))

	got, err = originFn.Call(&tengo.Int{Value: 1})
	require.NoError(t, err)
	require.Equal(t, int64(18), intResult(t, got))
	require.Equal(t, int64(10), origin.Get("g").Int64())
	require.Equal(t, int64(1000), dest.Get("g").Int64())

	// Mutating through the destination does not move the source global.
	require.NoError(t, dest.Set("g", 3))
	got, err = moved.Call(&tengo.Int{Value: 0})
	require.NoError(t, err)
	require.Equal(t, int64(11), intResult(t, got))
	require.Equal(t, int64(10), origin.Get("g").Int64())

	// Literals stay in the function's constant pool. Globals are the destination's.
	litOrigin := compileRun(t, `
g := 1
f := func(x) { return x + 1000 + g }
bad := func() { return 1 + true }
mk := func() {
	tag := "src"
	return func(s) { tag = tag + "-" + s; return tag + "!" }
}
clos := mk()
clos("a")
box := [f, {raw: [1], clos: clos}]
`, nil)
	litDest := compileRun(t, `
g := 5
f := undefined
bad := undefined
box := undefined
callf := func(x) { return f(x) }
`, nil)
	require.NoError(t, litDest.Set("f", litOrigin.Get("f").Object()))
	require.NoError(t, litDest.Set("bad", litOrigin.Get("bad").Object()))
	require.NoError(t, litDest.Set("box", litOrigin.Get("box").Object()))
	got, err = fnOf(t, litDest, "f").Call(&tengo.Int{Value: 1})
	require.NoError(t, err)
	require.Equal(t, int64(1006), intResult(t, got))
	// An in-script call on the destination VM uses the same split.
	got, err = fnOf(t, litDest, "callf").Call(&tengo.Int{Value: 2})
	require.NoError(t, err)
	require.Equal(t, int64(1007), intResult(t, got))

	boxMoved := litDest.Get("box").Object().(*tengo.Array)
	closMoved := boxMoved.Value[1].(*tengo.Map).Value["clos"].(*tengo.CompiledFunction)
	got, err = closMoved.Call(&tengo.String{Value: "b"})
	require.NoError(t, err)
	// tag was already "src-a" at transfer; the "!" literal is the function's.
	require.Equal(t, "src-a-b!", got.(*tengo.String).Value)
	got, err = litOrigin.Get("clos").Object().(*tengo.CompiledFunction).Call(&tengo.String{Value: "c"})
	require.NoError(t, err)
	require.Equal(t, "src-a-c!", got.(*tengo.String).Value)

	raw := boxMoved.Value[1].(*tengo.Map).Value["raw"].(*tengo.Array)
	require.NoError(t, raw.IndexSet(&tengo.Int{Value: 0}, &tengo.Int{Value: 9}))
	originRaw := litOrigin.Get("box").Object().(*tengo.Array).Value[1].(*tengo.Map).Value["raw"].(*tengo.Array)
	require.Equal(t, int64(1), originRaw.Value[0].(*tengo.Int).Value)
	require.Equal(t, int64(9), raw.Value[0].(*tengo.Int).Value)

	_, err = fnOf(t, litDest, "bad").Call()
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "Runtime Error: invalid operation: int + bool"), err.Error())
	require.True(t, strings.Contains(err.Error(), "(main):"), err.Error())
}

func TestCompiledFunctionCloneConcurrent(t *testing.T) {
	origin := compileRun(t, `
g := 0
f := func() { g++; return g }
`, nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			clone := origin.Clone()
			fn := fnOf(t, clone, "f")
			for n := 0; n < 20; n++ {
				got, err := fn.Call()
				require.NoError(t, err)
				require.Equal(t, int64(n+1), intResult(t, got))
			}
			require.Equal(t, int64(20), clone.Get("g").Int64())
		}()
	}
	wg.Wait()
	require.Equal(t, int64(0), origin.Get("g").Int64())
}
