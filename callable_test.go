package tengo_test

import (
	"errors"
	"testing"

	"github.com/d5/tengo/v2"
	"github.com/d5/tengo/v2/require"
)

func compileRun(t *testing.T, src string, vars map[string]interface{}) *tengo.Compiled {
	t.Helper()
	s := tengo.NewScript([]byte(src))
	for name, value := range vars {
		require.NoError(t, s.Add(name, value))
	}
	mods := tengo.NewModuleMap()
	mods.AddSourceModule("counter", []byte(`
count := 0
export {
	inc: func(n) { count += n; return count },
	get: func() { return count }
}`))
	s.SetImports(mods)
	c, err := s.Compile()
	require.NoError(t, err)
	require.NoError(t, c.Run())
	return c
}

func callObj(t *testing.T, fn tengo.Object, args ...tengo.Object) tengo.Object {
	t.Helper()
	require.True(t, fn.CanCall())
	ret, err := fn.Call(args...)
	require.NoError(t, err)
	return ret
}

func intObj(v int64) tengo.Object {
	return &tengo.Int{Value: v}
}

func TestCompiledFunction_Call(t *testing.T) {
	c := compileRun(t, `
base := 10
add := func(a, b) { return a + b + base }
sum := func(first, ...rest) {
	s := first
	for v in rest { s += v }
	return s
}
fib := func(n) { return n < 2 ? n : fib(n-1) + fib(n-2) }
fact := func(n, acc) { if n == 0 { return acc }; return fact(n-1, n*acc) }
localFib := func() {
	f := func(n) { return n < 2 ? n : f(n-1) + f(n-2) }
	return f
}()
counter := func() {
	n := 0
	return func() { n++; return n }
}()
adder := func(x) { return func(y) { return x + y } }
pair := func(x) { return [x, {double: func() { return x * 2 }}] }
nested := {arr: [func(x) { return x * 3 }], m: {f: func() { return base }}}
counterMod := import("counter")
nothing := func() {}
`, nil)

	require.Equal(t, intObj(13), callObj(t, c.Get("add").Object(), intObj(1), intObj(2)))

	// globals are shared with the compiled instance
	require.NoError(t, c.Set("base", 100))
	require.Equal(t, intObj(103), callObj(t, c.Get("add").Object(), intObj(1), intObj(2)))

	sum := c.Get("sum").Object()
	require.Equal(t, intObj(1), callObj(t, sum, intObj(1)))
	require.Equal(t, intObj(10), callObj(t, sum, intObj(1), intObj(2), intObj(3), intObj(4)))

	require.Equal(t, intObj(55), callObj(t, c.Get("fib").Object(), intObj(10)))
	require.Equal(t, intObj(3628800), callObj(t, c.Get("fact").Object(), intObj(10), intObj(1)))
	require.Equal(t, intObj(55), callObj(t, c.Get("localFib").Object(), intObj(10)))

	counter := c.Get("counter").Object()
	require.Equal(t, intObj(1), callObj(t, counter))
	require.Equal(t, intObj(2), callObj(t, counter))

	// returned closures stay callable
	add5 := callObj(t, c.Get("adder").Object(), intObj(5))
	require.Equal(t, intObj(12), callObj(t, add5, intObj(7)))

	// returned composite values stay callable
	pair := callObj(t, c.Get("pair").Object(), intObj(4)).(*tengo.Array)
	require.Equal(t, intObj(4), pair.Value[0])
	double := pair.Value[1].(*tengo.Map).Value["double"]
	require.Equal(t, intObj(8), callObj(t, double))

	// functions in nested arrays and maps
	nested := c.Get("nested").Object().(*tengo.Map)
	triple := nested.Value["arr"].(*tengo.Array).Value[0]
	require.Equal(t, intObj(9), callObj(t, triple, intObj(3)))
	require.Equal(t, intObj(100),
		callObj(t, nested.Value["m"].(*tengo.Map).Value["f"]))

	// source module exports
	mod := c.Get("counterMod").Object().(*tengo.ImmutableMap)
	require.Equal(t, intObj(2), callObj(t, mod.Value["inc"], intObj(2)))
	require.Equal(t, intObj(5), callObj(t, mod.Value["inc"], intObj(3)))
	require.Equal(t, intObj(5), callObj(t, mod.Value["get"]))

	require.Equal(t, tengo.UndefinedValue, callObj(t, c.Get("nothing").Object()))
}

func TestCompiledFunction_CallErrors(t *testing.T) {
	c := compileRun(t, `
add := func(a, b) { return a + b }
sum := func(first, ...rest) { return first }
fail := func(x) {
	return x + "a"
}
outer := func(x) {
	return fail(x)
}
`, nil)

	_, err := c.Get("add").Object().Call(intObj(1))
	require.Error(t, err)
	require.Equal(t,
		"Runtime Error: wrong number of arguments: want=2, got=1", err.Error())

	_, err = c.Get("sum").Object().Call()
	require.Error(t, err)
	require.Equal(t,
		"Runtime Error: wrong number of arguments: want>=1, got=0", err.Error())

	_, err = c.Get("fail").Object().Call(intObj(1))
	require.Error(t, err)
	require.Equal(t,
		"Runtime Error: invalid operation: int + string\n\tat (main):5:9",
		err.Error())

	_, err = c.Get("outer").Object().Call(intObj(1))
	require.Error(t, err)
	require.Equal(t,
		"Runtime Error: invalid operation: int + string\n\tat (main):5:9"+
			"\n\tat (main):8:9",
		err.Error())

	_, err = (&tengo.CompiledFunction{}).Call()
	require.True(t, errors.Is(err, tengo.ErrNotBoundFunction))
}

func TestCompiledFunction_CallFromGoCallback(t *testing.T) {
	userErr := errors.New("user error")
	each := func(args ...tengo.Object) (tengo.Object, error) {
		var res []tengo.Object
		for _, e := range args[0].(*tengo.Array).Value {
			r, err := args[1].Call(e)
			if err != nil {
				return nil, err
			}
			res = append(res, r)
		}
		return &tengo.Array{Value: res}, nil
	}
	fail := func(args ...tengo.Object) (tengo.Object, error) {
		return nil, userErr
	}

	c := compileRun(t, `
total := 0
out := each([1, 2, 3], func(x) { total += x; return x * factor })
mk := func() { n := 0; return func(x) { n += x; return n } }
acc := mk()
running := each([1, 2, 3], acc)
`, map[string]interface{}{
		"each":   each,
		"factor": 10,
	})
	requireInts(t, c.Get("out").Object(), 10, 20, 30)
	require.Equal(t, 6, c.Get("total").Int())
	requireInts(t, c.Get("running").Object(), 1, 3, 6)

	s := tengo.NewScript([]byte(`
bad := func(x) {
	return x + "a"
}
each([1], bad)
`))
	require.NoError(t, s.Add("each", each))
	_, err := s.Run()
	require.Error(t, err)
	require.Equal(t,
		"Runtime Error: invalid operation: int + string\n\tat (main):3:9"+
			"\n\tat (main):5:1",
		err.Error())

	s = tengo.NewScript([]byte(`
f := func() {
	return fail()
}
each([1], func(x) { return f() })
`))
	require.NoError(t, s.Add("each", each))
	require.NoError(t, s.Add("fail", fail))
	_, err = s.Run()
	require.Error(t, err)
	require.True(t, errors.Is(err, userErr))
	require.Equal(t,
		"Runtime Error: user error\n\tat (main):3:9\n\tat (main):5:28"+
			"\n\tat (main):5:1",
		err.Error())
}

func TestCompiled_CloneIsolatesCallables(t *testing.T) {
	c := compileRun(t, `
total := 0
counter := func() {
	n := 0
	return func() { n++; total++; return [n, total] }
}()
fns := [counter, {c: counter}]
counterMod := import("counter")
`, nil)

	requireInts(t, callObj(t, c.Get("counter").Object()), 1, 1)

	clone := c.Clone()
	cloneCounter := clone.Get("counter").Object()
	requireInts(t, callObj(t, cloneCounter), 2, 2)
	// aliases of the same closure keep sharing their captures in the clone
	fns := clone.Get("fns").Object().(*tengo.Array)
	requireInts(t, callObj(t, fns.Value[0]), 3, 3)
	requireInts(t, callObj(t, fns.Value[1].(*tengo.Map).Value["c"]), 4, 4)
	require.Equal(t, 4, clone.Get("total").Int())

	// source instance is unaffected
	require.Equal(t, 1, c.Get("total").Int())
	requireInts(t, callObj(t, c.Get("counter").Object()), 2, 2)

	mod := c.Get("counterMod").Object().(*tengo.ImmutableMap)
	require.Equal(t, intObj(1), callObj(t, mod.Value["inc"], intObj(1)))
	cloneMod := c.Clone().Get("counterMod").Object().(*tengo.ImmutableMap)
	require.Equal(t, intObj(11), callObj(t, cloneMod.Value["inc"], intObj(10)))
	require.Equal(t, intObj(1), callObj(t, mod.Value["get"]))

	// running the clone does not touch the source
	require.NoError(t, clone.Run())
	require.Equal(t, 0, clone.Get("total").Int())
	require.Equal(t, 2, c.Get("total").Int())
}

func TestCompiled_SetTransfersCallables(t *testing.T) {
	src := `
total := 0
counter := func() {
	n := 0
	return func() { n++; total++; return [n, total] }
}()
slot := undefined
run := func() { return slot() }
`
	a := compileRun(t, src, nil)
	b := compileRun(t, src, nil)
	require.NoError(t, b.Set("total", 100))

	counter := a.Get("counter").Object()
	requireInts(t, callObj(t, counter), 1, 1)
	requireInts(t, callObj(t, counter), 2, 2)

	require.NoError(t, b.Set("slot", counter))
	// captures as of transfer time, globals of the destination
	requireInts(t, callObj(t, b.Get("slot").Object()), 3, 101)
	requireInts(t, callObj(t, b.Get("run").Object()), 4, 102)
	require.Equal(t, 102, b.Get("total").Int())

	// source is unaffected
	require.Equal(t, 2, a.Get("total").Int())
	requireInts(t, callObj(t, counter), 3, 3)

	// callables nested in arrays and maps are transferred as well
	require.NoError(t, b.Set("slot", &tengo.Array{Value: []tengo.Object{
		counter,
		&tengo.Map{Value: map[string]tengo.Object{"f": counter}},
	}}))
	arr := b.Get("slot").Object().(*tengo.Array)
	requireInts(t, callObj(t, arr.Value[0]), 4, 103)
	requireInts(t, callObj(t, arr.Value[1].(*tengo.Map).Value["f"]), 5, 104)
	require.Equal(t, 3, a.Get("total").Int())
	requireInts(t, callObj(t, counter), 4, 4)

	// values set within the same instance are kept as-is
	own := b.Get("counter").Object()
	require.NoError(t, b.Set("slot", own))
	require.True(t, b.Get("slot").Object() == own)
}

func TestCompiled_SetTransfersAcrossPrograms(t *testing.T) {
	a := compileRun(t, `
name := "a"
helper := func() { return "helper-a" }
greet := func(x) { return name + ":" + x + ":" + helper() }
`, map[string]interface{}{"extra1": 1, "extra2": 2})

	b := compileRun(t, `
pad1 := 0
pad2 := 0
name := "b"
out := is_callable(slot) ? slot("x") : undefined
`, map[string]interface{}{"slot0": 0, "slot": 0})

	require.NoError(t, b.Set("slot", a.Get("greet").Object()))
	// name resolves against b; helper is missing in b and keeps its value
	require.Equal(t, "b:y:helper-a", callObj(t, b.Get("slot").Object(),
		&tengo.String{Value: "y"}).(*tengo.String).Value)
	require.NoError(t, b.Run())
	require.Equal(t, "b:x:helper-a", b.Get("out").String())
	require.Equal(t, "a", a.Get("name").String())

	bc := b.Clone()
	require.NoError(t, bc.Set("name", "bc"))
	require.Equal(t, "bc:w:helper-a", callObj(t, bc.Get("slot").Object(),
		&tengo.String{Value: "w"}).(*tengo.String).Value)
	require.Equal(t, "b:w:helper-a", callObj(t, b.Get("slot").Object(),
		&tengo.String{Value: "w"}).(*tengo.String).Value)
	require.NoError(t, bc.Run())
	require.Equal(t, "b:x:helper-a", bc.Get("out").String())

	// functions added to a script before compilation are transferred too
	s := tengo.NewScript([]byte(`name := "c"; out := greet("z")`))
	require.NoError(t, s.Add("greet", a.Get("greet").Object()))
	c, err := s.Run()
	require.NoError(t, err)
	require.Equal(t, "c:z:helper-a", c.Get("out").String())
}

func requireInts(t *testing.T, o tengo.Object, expected ...int64) {
	t.Helper()
	arr, ok := o.(*tengo.Array)
	require.True(t, ok, "not an array: %s", o.TypeName())
	require.Equal(t, len(expected), len(arr.Value))
	for i, e := range expected {
		require.Equal(t, intObj(e), arr.Value[i])
	}
}
