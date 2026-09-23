package tengo_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/d5/tengo/v2"
	"github.com/d5/tengo/v2/require"
	"github.com/d5/tengo/v2/stdlib"
	"github.com/d5/tengo/v2/token"
)

func TestScript_Add(t *testing.T) {
	s := tengo.NewScript([]byte(`a := b; c := test(b); d := test(5)`))
	require.NoError(t, s.Add("b", 5))     // b = 5
	require.NoError(t, s.Add("b", "foo")) // b = "foo"  (re-define before compilation)
	require.NoError(t, s.Add("test",
		func(args ...tengo.Object) (ret tengo.Object, err error) {
			if len(args) > 0 {
				switch arg := args[0].(type) {
				case *tengo.Int:
					return &tengo.Int{Value: arg.Value + 1}, nil
				}
			}

			return &tengo.Int{Value: 0}, nil
		}))
	c, err := s.Compile()
	require.NoError(t, err)
	require.NoError(t, c.Run())
	require.Equal(t, "foo", c.Get("a").Value())
	require.Equal(t, "foo", c.Get("b").Value())
	require.Equal(t, int64(0), c.Get("c").Value())
	require.Equal(t, int64(6), c.Get("d").Value())
}

func TestScript_Remove(t *testing.T) {
	s := tengo.NewScript([]byte(`a := b`))
	err := s.Add("b", 5)
	require.NoError(t, err)
	require.True(t, s.Remove("b")) // b is removed
	_, err = s.Compile()           // should not compile because b is undefined
	require.Error(t, err)
}

func TestScript_Run(t *testing.T) {
	s := tengo.NewScript([]byte(`a := b`))
	err := s.Add("b", 5)
	require.NoError(t, err)
	c, err := s.Run()
	require.NoError(t, err)
	require.NotNil(t, c)
	compiledGet(t, c, "a", int64(5))
}

func TestScript_BuiltinModules(t *testing.T) {
	s := tengo.NewScript([]byte(`math := import("math"); a := math.abs(-19.84)`))
	s.SetImports(stdlib.GetModuleMap("math"))
	c, err := s.Run()
	require.NoError(t, err)
	require.NotNil(t, c)
	compiledGet(t, c, "a", 19.84)

	c, err = s.Run()
	require.NoError(t, err)
	require.NotNil(t, c)
	compiledGet(t, c, "a", 19.84)

	s.SetImports(stdlib.GetModuleMap("os"))
	_, err = s.Run()
	require.Error(t, err)

	s.SetImports(nil)
	_, err = s.Run()
	require.Error(t, err)
}

func TestScript_SourceModules(t *testing.T) {
	s := tengo.NewScript([]byte(`
enum := import("enum")
a := enum.all([1,2,3], func(_, v) { 
	return v > 0 
})
`))
	s.SetImports(stdlib.GetModuleMap("enum"))
	c, err := s.Run()
	require.NoError(t, err)
	require.NotNil(t, c)
	compiledGet(t, c, "a", true)

	s.SetImports(nil)
	_, err = s.Run()
	require.Error(t, err)
}

func TestScript_SetMaxConstObjects(t *testing.T) {
	// one constant '5'
	s := tengo.NewScript([]byte(`a := 5`))
	s.SetMaxConstObjects(1) // limit = 1
	_, err := s.Compile()
	require.NoError(t, err)
	s.SetMaxConstObjects(0) // limit = 0
	_, err = s.Compile()
	require.Error(t, err)
	require.Equal(t, "exceeding constant objects limit: 1", err.Error())

	// two constants '5' and '1'
	s = tengo.NewScript([]byte(`a := 5 + 1`))
	s.SetMaxConstObjects(2) // limit = 2
	_, err = s.Compile()
	require.NoError(t, err)
	s.SetMaxConstObjects(1) // limit = 1
	_, err = s.Compile()
	require.Error(t, err)
	require.Equal(t, "exceeding constant objects limit: 2", err.Error())

	// duplicates will be removed
	s = tengo.NewScript([]byte(`a := 5 + 5`))
	s.SetMaxConstObjects(1) // limit = 1
	_, err = s.Compile()
	require.NoError(t, err)
	s.SetMaxConstObjects(0) // limit = 0
	_, err = s.Compile()
	require.Error(t, err)
	require.Equal(t, "exceeding constant objects limit: 1", err.Error())

	// no limit set
	s = tengo.NewScript([]byte(`a := 1 + 2 + 3 + 4 + 5`))
	_, err = s.Compile()
	require.NoError(t, err)
}

func TestScriptConcurrency(t *testing.T) {
	solve := func(a, b, c int) (d, e int) {
		a += 2
		b += c
		a += b * 2
		d = a + b + c
		e = 0
		for i := 1; i <= d; i++ {
			e += i
		}
		e *= 2
		return
	}

	code := []byte(`
mod1 := import("mod1")

a += 2
b += c
a += b * 2

arr := [a, b, c]
arrstr := string(arr)
map := {a: a, b: b, c: c}

d := a + b + c
s := 0

for i:=1; i<=d; i++ {
	s += i
}

e := mod1.double(s)
`)
	mod1 := map[string]tengo.Object{
		"double": &tengo.UserFunction{
			Value: func(args ...tengo.Object) (
				ret tengo.Object,
				err error,
			) {
				arg0, _ := tengo.ToInt64(args[0])
				ret = &tengo.Int{Value: arg0 * 2}
				return
			},
		},
	}

	scr := tengo.NewScript(code)
	_ = scr.Add("a", 0)
	_ = scr.Add("b", 0)
	_ = scr.Add("c", 0)
	mods := tengo.NewModuleMap()
	mods.AddBuiltinModule("mod1", mod1)
	scr.SetImports(mods)
	compiled, err := scr.Compile()
	require.NoError(t, err)

	executeFn := func(compiled *tengo.Compiled, a, b, c int) (d, e int) {
		_ = compiled.Set("a", a)
		_ = compiled.Set("b", b)
		_ = compiled.Set("c", c)
		err := compiled.Run()
		require.NoError(t, err)
		d = compiled.Get("d").Int()
		e = compiled.Get("e").Int()
		return
	}

	concurrency := 500
	var wg sync.WaitGroup
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func(compiled *tengo.Compiled) {
			time.Sleep(time.Duration(rand.Int63n(50)) * time.Millisecond)
			defer wg.Done()

			a := rand.Intn(10)
			b := rand.Intn(10)
			c := rand.Intn(10)

			d, e := executeFn(compiled, a, b, c)
			expectedD, expectedE := solve(a, b, c)

			require.Equal(t, expectedD, d, "input: %d, %d, %d", a, b, c)
			require.Equal(t, expectedE, e, "input: %d, %d, %d", a, b, c)
		}(compiled.Clone())
	}
	wg.Wait()
}

type Counter struct {
	tengo.ObjectImpl
	value int64
}

func (o *Counter) TypeName() string {
	return "counter"
}

func (o *Counter) String() string {
	return fmt.Sprintf("Counter(%d)", o.value)
}

func (o *Counter) BinaryOp(
	op token.Token,
	rhs tengo.Object,
) (tengo.Object, error) {
	switch rhs := rhs.(type) {
	case *Counter:
		switch op {
		case token.Add:
			return &Counter{value: o.value + rhs.value}, nil
		case token.Sub:
			return &Counter{value: o.value - rhs.value}, nil
		}
	case *tengo.Int:
		switch op {
		case token.Add:
			return &Counter{value: o.value + rhs.Value}, nil
		case token.Sub:
			return &Counter{value: o.value - rhs.Value}, nil
		}
	}

	return nil, errors.New("invalid operator")
}

func (o *Counter) IsFalsy() bool {
	return o.value == 0
}

func (o *Counter) Equals(t tengo.Object) bool {
	if tc, ok := t.(*Counter); ok {
		return o.value == tc.value
	}

	return false
}

func (o *Counter) Copy() tengo.Object {
	return &Counter{value: o.value}
}

func (o *Counter) Call(_ ...tengo.Object) (tengo.Object, error) {
	return &tengo.Int{Value: o.value}, nil
}

func (o *Counter) CanCall() bool {
	return true
}

func TestScript_CustomObjects(t *testing.T) {
	c := compile(t, `a := c1(); s := string(c1); c2 := c1; c2++`, M{
		"c1": &Counter{value: 5},
	})
	compiledRun(t, c)
	compiledGet(t, c, "a", int64(5))
	compiledGet(t, c, "s", "Counter(5)")
	compiledGetCounter(t, c, "c2", &Counter{value: 6})

	c = compile(t, `
arr := [1, 2, 3, 4]
for x in arr {
	c1 += x
}
out := c1()
`, M{
		"c1": &Counter{value: 5},
	})
	compiledRun(t, c)
	compiledGet(t, c, "out", int64(15))
}

func compiledGetCounter(
	t *testing.T,
	c *tengo.Compiled,
	name string,
	expected *Counter,
) {
	v := c.Get(name)
	require.NotNil(t, v)

	actual := v.Value().(*Counter)
	require.NotNil(t, actual)
	require.Equal(t, expected.value, actual.value)
}

func TestScriptSourceModule(t *testing.T) {
	// script1 imports "mod1"
	scr := tengo.NewScript([]byte(`out := import("mod")`))
	mods := tengo.NewModuleMap()
	mods.AddSourceModule("mod", []byte(`export 5`))
	scr.SetImports(mods)
	c, err := scr.Run()
	require.NoError(t, err)
	require.Equal(t, int64(5), c.Get("out").Value())

	// executing module function
	scr = tengo.NewScript([]byte(`fn := import("mod"); out := fn()`))
	mods = tengo.NewModuleMap()
	mods.AddSourceModule("mod",
		[]byte(`a := 3; export func() { return a + 5 }`))
	scr.SetImports(mods)
	c, err = scr.Run()
	require.NoError(t, err)
	require.Equal(t, int64(8), c.Get("out").Value())

	scr = tengo.NewScript([]byte(`out := import("mod")`))
	mods = tengo.NewModuleMap()
	mods.AddSourceModule("mod",
		[]byte(`text := import("text"); export text.title("foo")`))
	mods.AddBuiltinModule("text",
		map[string]tengo.Object{
			"title": &tengo.UserFunction{
				Name: "title",
				Value: func(args ...tengo.Object) (tengo.Object, error) {
					s, _ := tengo.ToString(args[0])
					return &tengo.String{Value: strings.Title(s)}, nil
				}},
		})
	scr.SetImports(mods)
	c, err = scr.Run()
	require.NoError(t, err)
	require.Equal(t, "Foo", c.Get("out").Value())
	scr.SetImports(nil)
	_, err = scr.Run()
	require.Error(t, err)
}

func BenchmarkArrayIndex(b *testing.B) {
	bench(b.N, `a := [1, 2, 3, 4, 5, 6, 7, 8, 9];
        for i := 0; i < 1000; i++ {
            a[0]; a[1]; a[2]; a[3]; a[4]; a[5]; a[6]; a[7]; a[7];
        }
    `)
}

func BenchmarkArrayIndexCompare(b *testing.B) {
	bench(b.N, `a := [1, 2, 3, 4, 5, 6, 7, 8, 9];
        for i := 0; i < 1000; i++ {
            1; 2; 3; 4; 5; 6; 7; 8; 9;
        }
    `)
}

func bench(n int, input string) {
	s := tengo.NewScript([]byte(input))
	c, err := s.Compile()
	if err != nil {
		panic(err)
	}

	for i := 0; i < n; i++ {
		if err := c.Run(); err != nil {
			panic(err)
		}
	}
}

type M map[string]interface{}

func TestCompiled_Get(t *testing.T) {
	// simple script
	c := compile(t, `a := 5`, nil)
	compiledRun(t, c)
	compiledGet(t, c, "a", int64(5))

	// user-defined variables
	compileError(t, `a := b`, nil)          // compile error because "b" is not defined
	c = compile(t, `a := b`, M{"b": "foo"}) // now compile with b = "foo" defined
	compiledGet(t, c, "a", nil)             // a = undefined; because it's before Compiled.Run()
	compiledRun(t, c)                       // Compiled.Run()
	compiledGet(t, c, "a", "foo")           // a = "foo"
}

func TestCompiled_GetAll(t *testing.T) {
	c := compile(t, `a := 5`, nil)
	compiledRun(t, c)
	compiledGetAll(t, c, M{"a": int64(5)})

	c = compile(t, `a := b`, M{"b": "foo"})
	compiledRun(t, c)
	compiledGetAll(t, c, M{"a": "foo", "b": "foo"})

	c = compile(t, `a := b; b = 5`, M{"b": "foo"})
	compiledRun(t, c)
	compiledGetAll(t, c, M{"a": "foo", "b": int64(5)})
}

func TestCompiled_IsDefined(t *testing.T) {
	c := compile(t, `a := 5`, nil)
	compiledIsDefined(t, c, "a", false) // a is not defined before Run()
	compiledRun(t, c)
	compiledIsDefined(t, c, "a", true)
	compiledIsDefined(t, c, "b", false)
}

func TestCompiled_Set(t *testing.T) {
	c := compile(t, `a := b`, M{"b": "foo"})
	compiledRun(t, c)
	compiledGet(t, c, "a", "foo")

	// replace value of 'b'
	err := c.Set("b", "bar")
	require.NoError(t, err)
	compiledRun(t, c)
	compiledGet(t, c, "a", "bar")

	// try to replace undefined variable
	err = c.Set("c", 1984)
	require.Error(t, err) // 'c' is not defined

	// case #2
	c = compile(t, `
a := func() { 
	return func() {
		return b + 5
	}() 
}()`, M{"b": 5})
	compiledRun(t, c)
	compiledGet(t, c, "a", int64(10))
	err = c.Set("b", 10)
	require.NoError(t, err)
	compiledRun(t, c)
	compiledGet(t, c, "a", int64(15))
}

func TestCompiled_RunContext(t *testing.T) {
	// machine completes normally
	c := compile(t, `a := 5`, nil)
	err := c.RunContext(context.Background())
	require.NoError(t, err)
	compiledGet(t, c, "a", int64(5))

	// timeout
	c = compile(t, `for true {}`, nil)
	ctx, cancel := context.WithTimeout(context.Background(),
		1*time.Millisecond)
	defer cancel()
	err = c.RunContext(ctx)
	require.Equal(t, context.DeadlineExceeded, err)
}

func TestCompiled_CustomObject(t *testing.T) {
	c := compile(t, `r := (t<130)`, M{"t": &customNumber{value: 123}})
	compiledRun(t, c)
	compiledGet(t, c, "r", true)

	c = compile(t, `r := (t>13)`, M{"t": &customNumber{value: 123}})
	compiledRun(t, c)
	compiledGet(t, c, "r", true)
}

// customNumber is a user defined object that can compare to tengo.Int
// very shitty implementation, just to test that token.Less and token.Greater in BinaryOp works
type customNumber struct {
	tengo.ObjectImpl
	value int64
}

func (n *customNumber) TypeName() string {
	return "Number"
}

func (n *customNumber) String() string {
	return strconv.FormatInt(n.value, 10)
}

func (n *customNumber) BinaryOp(op token.Token, rhs tengo.Object) (tengo.Object, error) {
	tengoInt, ok := rhs.(*tengo.Int)
	if !ok {
		return nil, tengo.ErrInvalidOperator
	}
	return n.binaryOpInt(op, tengoInt)
}

func (n *customNumber) binaryOpInt(op token.Token, rhs *tengo.Int) (tengo.Object, error) {
	i := n.value

	switch op {
	case token.Less:
		if i < rhs.Value {
			return tengo.TrueValue, nil
		}
		return tengo.FalseValue, nil
	case token.Greater:
		if i > rhs.Value {
			return tengo.TrueValue, nil
		}
		return tengo.FalseValue, nil
	case token.LessEq:
		if i <= rhs.Value {
			return tengo.TrueValue, nil
		}
		return tengo.FalseValue, nil
	case token.GreaterEq:
		if i >= rhs.Value {
			return tengo.TrueValue, nil
		}
		return tengo.FalseValue, nil
	}
	return nil, tengo.ErrInvalidOperator
}

func TestScript_ImportError(t *testing.T) {
	m := `
	exp := import("expression")
	r := exp(ctx)
`

	src := `
export func(ctx) {
	closure := func() {
		if ctx.actiontimes < 0 { // an error is thrown here because actiontimes is undefined
			return true
		}
		return false
	}

	return closure()
}`

	s := tengo.NewScript([]byte(m))
	mods := tengo.NewModuleMap()
	mods.AddSourceModule("expression", []byte(src))
	s.SetImports(mods)

	err := s.Add("ctx", map[string]interface{}{
		"ctx": 12,
	})
	require.NoError(t, err)

	_, err = s.Run()
	require.True(t, strings.Contains(err.Error(), "expression:4:6"))
}

func compile(t *testing.T, input string, vars M) *tengo.Compiled {
	s := tengo.NewScript([]byte(input))
	for vn, vv := range vars {
		err := s.Add(vn, vv)
		require.NoError(t, err)
	}

	c, err := s.Compile()
	require.NoError(t, err)
	require.NotNil(t, c)
	return c
}

func compileError(t *testing.T, input string, vars M) {
	s := tengo.NewScript([]byte(input))
	for vn, vv := range vars {
		err := s.Add(vn, vv)
		require.NoError(t, err)
	}
	_, err := s.Compile()
	require.Error(t, err)
}

func compiledRun(t *testing.T, c *tengo.Compiled) {
	err := c.Run()
	require.NoError(t, err)
}

func compiledGet(
	t *testing.T,
	c *tengo.Compiled,
	name string,
	expected interface{},
) {
	v := c.Get(name)
	require.NotNil(t, v)
	require.Equal(t, expected, v.Value())
}

func compiledGetAll(
	t *testing.T,
	c *tengo.Compiled,
	expected M,
) {
	vars := c.GetAll()
	require.Equal(t, len(expected), len(vars))

	for k, v := range expected {
		var found bool
		for _, e := range vars {
			if e.Name() == k {
				require.Equal(t, v, e.Value())
				found = true
			}
		}
		require.True(t, found, "variable '%s' not found", k)
	}
}

func compiledIsDefined(
	t *testing.T,
	c *tengo.Compiled,
	name string,
	expected bool,
) {
	require.Equal(t, expected, c.IsDefined(name))
}
func TestCompiledFunction_Call(t *testing.T) {
	c := compile(t, `
g := 10
add := func(a, b) { return a + b + g }
incr := func(x) { g += x; return g }
counter := func() {
	n := 0
	return func() { n++; return n }
}()
vargs := func(a, ...rest) { return [a, rest] }
fib := func(n) { if n < 2 { return n }; return fib(n-1) + fib(n-2) }
sum := func(n, acc) { if n == 0 { return acc }; return sum(n-1, acc+n) }
fact := func() {
	f := func(n) { if n == 0 { return 1 }; return n * f(n-1) }
	return f
}()
adder := func(x) { return func(y) { return x + y + g } }
ops := func(x) { return {inc: func() { x++; return x }, get: [func() { return x }]} }
nested := {a: [1, {f: func(x) { return x * g }}], b: immutable({f: func() { return g }})}
`, nil)
	compiledRun(t, c)

	compiledCall(t, c.Get("add").Object(), ARR{1, 2}, 13)
	compiledCall(t, c.Get("incr").Object(), ARR{5}, 15)
	compiledGet(t, c, "g", int64(15))
	compiledCall(t, c.Get("add").Object(), ARR{1, 2}, 18)

	compiledCall(t, c.Get("counter").Object(), nil, 1)
	compiledCall(t, c.Get("counter").Object(), nil, 2)

	compiledCall(t, c.Get("vargs").Object(), ARR{1}, ARR{1, ARR{}})
	compiledCall(t, c.Get("vargs").Object(), ARR{1, 2, 3}, ARR{1, ARR{2, 3}})

	compiledCall(t, c.Get("fib").Object(), ARR{15}, 610)
	compiledCall(t, c.Get("sum").Object(), ARR{10000, 0}, 50005000)
	compiledCall(t, c.Get("fact").Object(), ARR{5}, 120)

	// returned closures and composite values stay callable
	add1 := compiledCall(t, c.Get("adder").Object(), ARR{1}, nil)
	compiledCall(t, add1, ARR{2}, 18)
	ops := compiledCall(t, c.Get("ops").Object(), ARR{7}, nil).(*tengo.Map)
	compiledCall(t, ops.Value["inc"], nil, 8)
	compiledCall(t, ops.Value["get"].(*tengo.Array).Value[0], nil, 8)

	nested := c.Get("nested").Object().(*tengo.Map)
	compiledCall(t, nested.Value["a"].(*tengo.Array).Value[1].(*tengo.Map).
		Value["f"], ARR{2}, 30)
	compiledCall(t, nested.Value["b"].(*tengo.ImmutableMap).Value["f"], nil, 15)

	// the script sees the effects of calls made from Go
	c = compile(t, `
n := 0
f := func() { n++; return n }
out := f() + f()
`, nil)
	compiledRun(t, c)
	compiledCall(t, c.Get("f").Object(), nil, 3)
	compiledGet(t, c, "n", int64(3))
	compiledRun(t, c)
	compiledGet(t, c, "out", int64(3))

	_, err := (&tengo.CompiledFunction{}).Call()
	require.Error(t, err)
}

func TestCompiledFunction_CallErrors(t *testing.T) {
	src := `
inner := func(x) {
	return x + "a"
}
outer := func(x) {
	return inner(x)
}
`
	c := compile(t, src, nil)
	compiledRun(t, c)
	_, err := c.Get("outer").Object().Call(&tengo.Int{Value: 1})
	require.Error(t, err)
	require.Equal(t, "Runtime Error: invalid operation: int + string"+
		"\n\tat (main):3:9\n\tat (main):6:9", err.Error())

	// same as the error of a call from the script, minus its call site
	_, err2 := tengo.NewScript([]byte(src + "outer(1)")).Run()
	require.Error(t, err2)
	require.Equal(t, err.Error()+"\n\tat (main):8:1", err2.Error())

	_, err = c.Get("outer").Object().Call()
	require.Error(t, err)
	require.Equal(t, "Runtime Error: wrong number of arguments: want=1, got=0",
		err.Error())

	userErr := errors.New("user error")
	c = compile(t, `f := func() { return fail() }`, M{
		"fail": &tengo.UserFunction{
			Value: func(args ...tengo.Object) (tengo.Object, error) {
				return nil, userErr
			},
		},
	})
	compiledRun(t, c)
	_, err = c.Get("f").Object().Call()
	require.True(t, errors.Is(err, userErr))
	require.Equal(t, "Runtime Error: user error\n\tat (main):1:22", err.Error())

	s := tengo.NewScript([]byte(`f := func() { a := []; for true { a = append(a, 1) } }`))
	s.SetMaxAllocs(100)
	c, err = s.Run()
	require.NoError(t, err)
	_, err = c.Get("f").Object().Call()
	require.True(t, errors.Is(err, tengo.ErrObjectAllocLimit))
}

func TestCompiledFunction_CallFromCallback(t *testing.T) {
	var kept tengo.Object
	apply := &tengo.UserFunction{
		Value: func(args ...tengo.Object) (tengo.Object, error) {
			return args[0].Call(args[1:]...)
		},
	}
	c := compile(t, `
g := 2
out := apply(func(x) { return x * g }, 21)
keep(func(x) { g += x; return g })
`, M{
		"apply": apply,
		"keep": &tengo.UserFunction{
			Value: func(args ...tengo.Object) (tengo.Object, error) {
				kept = args[0]
				return nil, nil
			},
		},
	})
	compiledRun(t, c)
	compiledGet(t, c, "out", int64(42))
	compiledCall(t, kept, ARR{3}, 5)
	compiledGet(t, c, "g", int64(5))

	// an error passed back by the callback reads like an in-script call's
	c = compile(t, `bad := func(x) {
	return x + "a"
}
out := apply(bad, 1)`, M{"apply": apply})
	err := c.Run()
	require.Error(t, err)
	_, err2 := tengo.NewScript([]byte(`bad := func(x) {
	return x + "a"
}
out := bad(1)`)).Run()
	require.Error(t, err2)
	require.Equal(t, err2.Error(), err.Error())
	require.Equal(t, "Runtime Error: invalid operation: int + string"+
		"\n\tat (main):2:9\n\tat (main):4:8", err.Error())

	// calls made while the script is aborted stop with it
	var callErr error
	c = compile(t, `run(func() { for true {} })`, M{
		"run": &tengo.UserFunction{
			Value: func(args ...tengo.Object) (tengo.Object, error) {
				_, callErr = args[0].Call()
				return nil, callErr
			},
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(),
		10*time.Millisecond)
	defer cancel()
	require.Equal(t, context.DeadlineExceeded, c.RunContext(ctx))
	require.True(t, errors.Is(callErr, tengo.ErrVMAborted))

	// calls nested through Go inside a call from Go share its call depth and
	// allocation limit, as they would inside a run of the script
	s := tengo.NewScript([]byte(`
depth := func(n) { if n == 0 { return 0 }; return 1 + apply(depth, n - 1) }
loop := func() { return apply(loop) }
alloc := func(n) { if n == 0 { return 0 }; [1]; [2]; return apply(alloc, n - 1) }
`))
	require.NoError(t, s.Add("apply", apply))
	s.SetMaxAllocs(50)
	c, err = s.Run()
	require.NoError(t, err)
	compiledCall(t, c.Get("depth").Object(), ARR{10}, 10)
	_, err = c.Get("loop").Object().Call()
	require.True(t, errors.Is(err, tengo.ErrStackOverflow))
	_, err = c.Get("alloc").Object().Call(&tengo.Int{Value: 40})
	require.True(t, errors.Is(err, tengo.ErrObjectAllocLimit))
}

func TestCompiledFunction_CallSourceModule(t *testing.T) {
	mods := stdlib.GetModuleMap("math")
	mods.AddSourceModule("mod", []byte(`
math := import("math")
count := 0
export {
	abs: func(x) { return math.abs(x) },
	next: func() { count++; return count },
	num: func(...xs) { return len(xs) }
}`))
	mods.AddSourceModule("double", []byte(`export func(x) { return x * 2 }`))
	s := tengo.NewScript([]byte(`mod := import("mod"); double := import("double")`))
	s.SetImports(mods)
	c, err := s.Run()
	require.NoError(t, err)

	mod := c.Get("mod").Object().(*tengo.ImmutableMap)
	compiledCall(t, mod.Value["abs"], ARR{-2.5}, 2.5)
	compiledCall(t, mod.Value["next"], nil, 1)
	compiledCall(t, mod.Value["next"], nil, 2)
	compiledCall(t, mod.Value["num"], ARR{1, 2, 3}, 3)
	compiledCall(t, c.Get("double").Object(), ARR{4}, 8)
}

func TestCompiled_CloneFunctions(t *testing.T) {
	c := compile(t, `
count := 0
mk := func() {
	n := 0
	data := {k: 0}
	return func() { n++; count++; data.k += 10; return [n, count, data.k] }
}
f := mk()
f()
m := {fns: [f], other: mk()}
`, nil)
	compiledRun(t, c)
	clone := c.Clone()

	// the clone has its own globals and captured variables
	compiledCall(t, clone.Get("f").Object(), nil, ARR{2, 2, 20})
	compiledGet(t, clone, "count", int64(2))
	compiledGet(t, c, "count", int64(1))
	compiledCall(t, c.Get("f").Object(), nil, ARR{2, 2, 20})
	compiledCall(t, c.Get("f").Object(), nil, ARR{3, 3, 30})
	compiledGet(t, clone, "count", int64(2))

	// callables inside arrays and maps too, keeping captures they share
	fns := clone.Get("m").Object().(*tengo.Map).Value["fns"].(*tengo.Array)
	compiledCall(t, fns.Value[0], nil, ARR{3, 3, 30})
	other := clone.Get("m").Object().(*tengo.Map).Value["other"]
	compiledCall(t, other, nil, ARR{1, 4, 10})
	compiledGet(t, c, "count", int64(3))

	// running the clone leaves the original alone
	compiledRun(t, clone)
	compiledGet(t, clone, "count", int64(1))
	compiledCall(t, c.Get("f").Object(), nil, ARR{4, 4, 40})
}

func TestCompiled_SetFunctions(t *testing.T) {
	src := `
count := 0
mk := func() {
	n := 0
	return func() { n++; count++; return [n, count] }
}
f := mk()
m := {fns: [f, mk()]}
`
	c1 := compile(t, src, nil)
	compiledRun(t, c1)
	f1 := c1.Get("f").Object()
	compiledCall(t, f1, nil, ARR{1, 1})
	compiledCall(t, f1, nil, ARR{2, 2})

	c2 := compile(t, src, nil)
	compiledRun(t, c2)
	require.NoError(t, c2.Set("f", f1))

	// captures as they were when assigned, globals of the destination
	compiledCall(t, c2.Get("f").Object(), nil, ARR{3, 1})
	compiledGet(t, c1, "count", int64(2))
	compiledCall(t, f1, nil, ARR{3, 3})
	compiledCall(t, c2.Get("f").Object(), nil, ARR{4, 2})

	// callables inside arrays and maps are moved the same way
	require.NoError(t, c2.Set("m", c1.Get("m").Object()))
	m2 := c2.Get("m").Object().(*tengo.Map)
	require.True(t, m2 != c1.Get("m").Object())
	compiledCall(t, m2.Value["fns"].(*tengo.Array).Value[0], nil, ARR{4, 3})
	compiledCall(t, m2.Value["fns"].(*tengo.Array).Value[1], nil, ARR{1, 4})
	compiledGet(t, c1, "count", int64(3))
	m1 := c1.Get("m").Object().(*tengo.Map)
	compiledCall(t, m1.Value["fns"].(*tengo.Array).Value[1], nil, ARR{1, 4})

	// values without functions are assigned as they are
	arr := &tengo.Array{Value: []tengo.Object{&tengo.Int{Value: 1}}}
	require.NoError(t, c2.Set("m", arr))
	require.True(t, c2.Get("m").Object() == arr)

	// functions from another script run their own code against the globals
	// of the destination
	c3 := compile(t, `count := 0; f = func() { count += 5; return "n" + count }`,
		M{"f": 0})
	compiledRun(t, c3)
	c4 := compile(t, `count := 100; out := f()`, M{"f": 0})
	require.NoError(t, c4.Set("f", c3.Get("f").Object()))
	compiledRun(t, c4)
	compiledGet(t, c4, "out", "n105")
	compiledGet(t, c3, "count", int64(0))

	// variables added to a script are moved into each compiled instance
	counter := compile(t, `f := func() { n := 0; return func() { n++; return n } }()
f()`, nil)
	compiledRun(t, counter)
	s := tengo.NewScript([]byte(`a := f(); b := f()`))
	require.NoError(t, s.Add("f", counter.Get("f").Object()))
	ca, err := s.Run()
	require.NoError(t, err)
	cb, err := s.Run()
	require.NoError(t, err)
	compiledGet(t, ca, "b", int64(3))
	compiledGet(t, cb, "b", int64(3))
	compiledCall(t, counter.Get("f").Object(), nil, 2)
}

func TestCompiled_SetFunctionsSharing(t *testing.T) {
	src := `
pair := func() {
	n := 0
	return {inc: func() { n++; return n }, get: func() { return n }}
}()
pair.inc()
fact := func() {
	f := func(n) { if n == 0 { return 1 }; return n * f(n-1) }
	return f
}()
m := {f: func() { return 1 }}
m.self = m
`
	c1 := compile(t, src, nil)
	compiledRun(t, c1)
	c2 := compile(t, src, nil)
	require.NoError(t, c2.Set("pair", c1.Get("pair").Object()))
	require.NoError(t, c2.Set("fact", c1.Get("fact").Object()))
	require.NoError(t, c2.Set("m", c1.Get("m").Object()))

	// closures sharing a variable still share it, and only with each other
	pair := c2.Get("pair").Object().(*tengo.Map)
	compiledCall(t, pair.Value["inc"], nil, 2)
	compiledCall(t, pair.Value["get"], nil, 2)
	compiledCall(t, c1.Get("pair").Object().(*tengo.Map).Value["get"], nil, 1)

	compiledCall(t, c2.Get("fact").Object(), ARR{5}, 120)

	m := c2.Get("m").Object().(*tengo.Map)
	require.True(t, m.Value["self"] == m)
	compiledCall(t, m.Value["f"], nil, 1)
}

func compiledCall(
	t *testing.T,
	fn tengo.Object,
	args ARR,
	expected interface{},
) tengo.Object {
	var objs []tengo.Object
	for _, arg := range args {
		objs = append(objs, toObject(arg))
	}
	require.True(t, fn.CanCall())
	res, err := fn.Call(objs...)
	require.NoError(t, err)
	if expected != nil {
		require.Equal(t, toObject(expected), res)
	}
	return res
}

func TestCompiled_Clone(t *testing.T) {
	script := tengo.NewScript([]byte(`
count += 1
data["b"] = 2
`))

	err := script.Add("data", map[string]interface{}{"a": 1})
	require.NoError(t, err)

	err = script.Add("count", 1000)
	require.NoError(t, err)

	compiled, err := script.Compile()
	require.NoError(t, err)

	clone := compiled.Clone()
	err = clone.RunContext(context.Background())
	require.NoError(t, err)

	require.Equal(t, 1000, compiled.Get("count").Int())
	require.Equal(t, 1, len(compiled.Get("data").Map()))

	require.Equal(t, 1001, clone.Get("count").Int())
	require.Equal(t, 2, len(clone.Get("data").Map()))
}
