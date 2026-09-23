package tengo_test

import (
	"strings"
	"testing"

	"github.com/d5/tengo/v2"
	"github.com/d5/tengo/v2/require"
)

func callGlobal(t *testing.T, c *tengo.Compiled, name string,
	args ...tengo.Object) tengo.Object {
	t.Helper()
	fn := c.Get(name).Object()
	require.True(t, fn.CanCall())
	ret, err := fn.Call(args...)
	require.NoError(t, err)
	return ret
}

func TestCompiledFunctionCall(t *testing.T) {
	mods := tengo.NewModuleMap()
	mods.AddSourceModule("mod", []byte(`
base := 100
export { add: func(x) { return base + x } }`))
	s := tengo.NewScript([]byte(`
mod := import("mod")
g := 10
add := func(a, b) { return a + b + g }
sum := func(...xs) { t := 0; for x in xs { t += x }; return t }
fib := func(n) { return n < 2 ? n : fib(n-1) + fib(n-2) }
counter := func() { n := 0; return func() { n++; return n } }()
nested := {list: [func(x) { return x * 2 }]}
modAdd := mod.add
fail := func() { return 1 + "a" + [] }
mk := func() { return {f: func() { return g }} }
res := 0
if cb != undefined { res = cb(func(x) { return x + g }) }
`))
	s.SetImports(mods)
	_ = s.Add("cb", nil)
	c, err := s.Compile()
	require.NoError(t, err)
	require.NoError(t, c.Run())

	require.Equal(t, int64(13), callGlobal(t, c, "add",
		&tengo.Int{Value: 1}, &tengo.Int{Value: 2}).(*tengo.Int).Value)
	require.Equal(t, int64(6), callGlobal(t, c, "sum", &tengo.Int{Value: 1},
		&tengo.Int{Value: 2}, &tengo.Int{Value: 3}).(*tengo.Int).Value)
	require.Equal(t, int64(0), callGlobal(t, c, "sum").(*tengo.Int).Value)
	require.Equal(t, int64(55), callGlobal(t, c, "fib",
		&tengo.Int{Value: 10}).(*tengo.Int).Value)
	require.Equal(t, int64(1), callGlobal(t, c, "counter").(*tengo.Int).Value)
	require.Equal(t, int64(2), callGlobal(t, c, "counter").(*tengo.Int).Value)
	require.Equal(t, int64(105), callGlobal(t, c, "modAdd",
		&tengo.Int{Value: 5}).(*tengo.Int).Value)

	f := c.Get("nested").Object().(*tengo.Map).Value["list"].(*tengo.Array).Value[0]
	ret, err := f.Call(&tengo.Int{Value: 4})
	require.NoError(t, err)
	require.Equal(t, int64(8), ret.(*tengo.Int).Value)

	m := callGlobal(t, c, "mk").(*tengo.Map)
	ret, err = m.Value["f"].Call()
	require.NoError(t, err)
	require.Equal(t, int64(10), ret.(*tengo.Int).Value)

	_, err = c.Get("add").Object().Call(&tengo.Int{Value: 1})
	require.Error(t, err)
	require.True(t, strings.HasPrefix(err.Error(),
		"Runtime Error: wrong number of arguments"))
	_, err = c.Get("fail").Object().Call()
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "Runtime Error: invalid operation"))
	require.True(t, strings.Contains(err.Error(), "at (main):"))

	// Go callback receives a closure
	require.NoError(t, c.Set("cb", &tengo.UserFunction{
		Value: func(args ...tengo.Object) (tengo.Object, error) {
			return args[0].Call(&tengo.Int{Value: 5})
		}}))
	require.NoError(t, c.Run())
	require.Equal(t, int64(15), c.Get("res").Int64())
}

func TestCompiledFunctionIsolation(t *testing.T) {
	src := []byte(`
g := 1
v := undefined
counter := func() { n := 0; return func() { n++; return n + g } }()
box := {fns: [counter]}
`)
	c, err := tengo.NewScript(src).Compile()
	require.NoError(t, err)
	require.NoError(t, c.Run())
	callGlobal(t, c, "counter") // n = 1

	clone := c.Clone()
	require.NoError(t, clone.Set("g", 100))
	require.Equal(t, int64(102), callGlobal(t, clone, "counter").(*tengo.Int).Value)
	require.Equal(t, int64(3), callGlobal(t, c, "counter").(*tengo.Int).Value)

	// assign a (nested) closure from c into another instance
	other, err := tengo.NewScript(src).Compile()
	require.NoError(t, err)
	require.NoError(t, other.Run())
	require.NoError(t, other.Set("g", 1000))
	require.NoError(t, other.Set("v", c.Get("box").Object()))
	fn := other.Get("v").Object().(*tengo.Map).Value["fns"].(*tengo.Array).Value[0]
	ret, err := fn.Call()
	require.NoError(t, err)
	require.Equal(t, int64(1003), ret.(*tengo.Int).Value) // n was 2 at transfer
	require.Equal(t, int64(4), callGlobal(t, c, "counter").(*tengo.Int).Value)
}
