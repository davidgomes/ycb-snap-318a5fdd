package tengo_test

import (
	"testing"

	"github.com/d5/tengo/v2"
	"github.com/d5/tengo/v2/require"
)

func runScript(t *testing.T, src string, setup func(s *tengo.Script)) *tengo.Compiled {
	s := tengo.NewScript([]byte(src))
	if setup != nil {
		setup(s)
	}
	c, err := s.Run()
	require.NoError(t, err)
	return c
}

func callFn(t *testing.T, fn tengo.Object, args ...tengo.Object) tengo.Object {
	require.True(t, fn.CanCall())
	ret, err := fn.Call(args...)
	require.NoError(t, err)
	return ret
}

func intObj(v int64) tengo.Object { return &tengo.Int{Value: v} }

func TestCompiledFunction_Call(t *testing.T) {
	c := runScript(t, `
g := 10
add := func(a, b) { return a + b + g }
counter := func() {
	n := 0
	return func() { n++; return n }
}()
sum := func(x, ...rest) {
	for v in rest { x += v }
	return x
}
fib := func(n) { return n < 2 ? n : fib(n-1) + fib(n-2) }
make_adder := func(n) { return func(x) { return x + n } }
nested := [1, {f: func(x) { return x * g }}]
`, nil)

	require.Equal(t, intObj(13), callFn(t, c.Get("add").Object(),
		intObj(1), intObj(2)))

	counter := c.Get("counter").Object()
	require.Equal(t, intObj(1), callFn(t, counter))
	require.Equal(t, intObj(2), callFn(t, counter))

	sum := c.Get("sum").Object()
	require.Equal(t, intObj(1), callFn(t, sum, intObj(1)))
	require.Equal(t, intObj(10), callFn(t, sum, intObj(1), intObj(2),
		intObj(3), intObj(4)))

	require.Equal(t, intObj(55), callFn(t, c.Get("fib").Object(), intObj(10)))

	adder := callFn(t, c.Get("make_adder").Object(), intObj(5))
	require.Equal(t, intObj(8), callFn(t, adder, intObj(3)))

	nf := c.Get("nested").Object().(*tengo.Array).Value[1].(*tengo.Map).Value["f"]
	require.Equal(t, intObj(30), callFn(t, nf, intObj(3)))

	// globals are shared with the compiled instance
	require.NoError(t, c.Set("g", 100))
	require.Equal(t, intObj(103), callFn(t, c.Get("add").Object(),
		intObj(1), intObj(2)))

	_, err := c.Get("add").Object().Call(intObj(1))
	require.Error(t, err)
	require.Equal(t,
		"Runtime Error: wrong number of arguments: want=2, got=1",
		err.Error())

	_, err = sum.Call()
	require.Error(t, err)
	require.Equal(t,
		"Runtime Error: wrong number of arguments: want>=1, got=0",
		err.Error())

	_, err = c.Get("add").Object().Call(intObj(1), &tengo.String{Value: "x"})
	require.Error(t, err)
	require.Equal(t,
		"Runtime Error: invalid operation: int + string\n\tat (main):3:28",
		err.Error())
}

func TestCompiledFunction_CallModuleAndCallback(t *testing.T) {
	var cbResult tengo.Object
	c := runScript(t, `
mod := import("mod")
callback(func(x) { return mod.double(x) + 1 })
exported := mod.double
`, func(s *tengo.Script) {
		mm := tengo.NewModuleMap()
		mm.AddSourceModule("mod", []byte(`
k := 2
export { double: func(x) { return x * k } }
`))
		s.SetImports(mm)
		_ = s.Add("callback", &tengo.UserFunction{
			Value: func(args ...tengo.Object) (tengo.Object, error) {
				ret, err := args[0].Call(intObj(20))
				cbResult = ret
				return ret, err
			},
		})
	})
	require.Equal(t, intObj(41), cbResult)
	require.Equal(t, intObj(14), callFn(t, c.Get("exported").Object(),
		intObj(7)))
}

func TestCompiledFunction_CloneIsolation(t *testing.T) {
	c := runScript(t, `
g := 1
counter := func() {
	n := 0
	return func() { n++; g++; return n }
}()
counter()
fns := [counter, {c: counter}]
`, nil)

	clone := c.Clone()
	cc := clone.Get("counter").Object()
	require.Equal(t, intObj(2), callFn(t, cc))
	require.Equal(t, intObj(3), callFn(t, cc))
	require.Equal(t, 4, clone.Get("g").Int())

	// nested copies share captures with the top-level clone copy
	nested := clone.Get("fns").Object().(*tengo.Array)
	require.Equal(t, intObj(4), callFn(t, nested.Value[0]))
	require.Equal(t, intObj(5),
		callFn(t, nested.Value[1].(*tengo.Map).Value["c"]))

	require.Equal(t, 2, c.Get("g").Int())
	require.Equal(t, intObj(2), callFn(t, c.Get("counter").Object()))
	require.Equal(t, 3, c.Get("g").Int())
	require.Equal(t, 6, clone.Get("g").Int())
}

func TestCompiledFunction_TransferBetweenInstances(t *testing.T) {
	script := tengo.NewScript([]byte(`
g := is_undefined(f) ? "src" : "dst"
if is_undefined(f) {
	f = func() {
		n := 0
		return func() { n++; return [n, g] }
	}()
	f()
}
box := [{f: f}]
out := f()
`))
	require.NoError(t, script.Add("f", nil))

	src, err := script.Compile()
	require.NoError(t, err)
	require.NoError(t, src.Run())
	require.Equal(t, "[2, \"src\"]", src.Get("out").Object().String())

	dst, err := script.Compile()
	require.NoError(t, err)

	// captures are snapshotted at transfer time, globals resolve in dst
	require.NoError(t, dst.Set("f", src.Get("f").Object()))
	require.NoError(t, dst.Run())
	require.Equal(t, "[3, \"dst\"]", dst.Get("out").Object().String())
	require.Equal(t, "[4, \"dst\"]", callFn(t, dst.Get("f").Object()).String())

	// source is untouched
	require.Equal(t, "[3, \"src\"]", callFn(t, src.Get("f").Object()).String())

	// callables nested in containers are transferred as well
	require.NoError(t, dst.Set("box", src.Get("box").Object()))
	dstBox := dst.Get("box").Object().(*tengo.Array)
	boxed := dstBox.Value[0].(*tengo.Map).Value["f"]
	require.Equal(t, "[4, \"dst\"]", callFn(t, boxed).String())
	require.Equal(t, "[5, \"dst\"]", callFn(t, dst.Get("f").Object()).String())

	// mutating the transferred container does not affect the source
	dstBox.Value[0].(*tengo.Map).Value["f"] = tengo.UndefinedValue
	srcBoxed := src.Get("box").Object().(*tengo.Array).Value[0].(*tengo.Map).
		Value["f"]
	require.Equal(t, "[4, \"src\"]", callFn(t, srcBoxed).String())

	// a clone receiving a callable from its source keeps isolated captures
	clone := src.Clone()
	require.NoError(t, clone.Set("f", src.Get("f").Object()))
	require.Equal(t, "[5, \"src\"]", callFn(t, clone.Get("f").Object()).String())
	require.Equal(t, "[5, \"src\"]", callFn(t, src.Get("f").Object()).String())
}
