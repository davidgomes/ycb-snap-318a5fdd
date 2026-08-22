package tengo_test

import (
	"strings"
	"testing"

	"github.com/d5/tengo/v2"
	"github.com/d5/tengo/v2/require"
)

func TestDestructureArray(t *testing.T) {
	c := compile(t, `[a, b] := [1, 2]`, nil)
	compiledRun(t, c)
	compiledGet(t, c, "a", int64(1))
	compiledGet(t, c, "b", int64(2))
}

func TestDestructureMapShorthand(t *testing.T) {
	c := compile(t, `{x, y} := {x: 10, y: 20}`, nil)
	compiledRun(t, c)
	compiledGet(t, c, "x", int64(10))
	compiledGet(t, c, "y", int64(20))
}

func TestDestructureMapRename(t *testing.T) {
	c := compile(t, `{x: a, y: b} := {x: 1, y: 2}`, nil)
	compiledRun(t, c)
	compiledGet(t, c, "a", int64(1))
	compiledGet(t, c, "b", int64(2))
}

func TestDestructureMapDefault(t *testing.T) {
	c := compile(t, `{x: a = 50} := {y: 1}`, nil)
	compiledRun(t, c)
	compiledGet(t, c, "a", int64(50))
}

func TestDestructureArrayDefaultLazy(t *testing.T) {
	c := compile(t, `called := 0
f := func() { called++; return 99 }
[a, b = f()] := [1]`, nil)
	compiledRun(t, c)
	compiledGet(t, c, "a", int64(1))
	compiledGet(t, c, "b", int64(99))
	compiledGet(t, c, "called", int64(1))
}

func TestDestructureDefaultUsesEarlierBinding(t *testing.T) {
	c := compile(t, `[a, b = a + 1] := [10]`, nil)
	compiledRun(t, c)
	compiledGet(t, c, "a", int64(10))
	compiledGet(t, c, "b", int64(11))
}

func TestDestructureMissingBindsUndefined(t *testing.T) {
	c := compile(t, `[a, b] := [1]`, nil)
	compiledRun(t, c)
	compiledGet(t, c, "a", int64(1))
	v := c.Get("b")
	require.NotNil(t, v)
	require.True(t, v.IsUndefined())
}

func TestDestructureMapMissingKey(t *testing.T) {
	c := compile(t, `{x: a} := {}`, nil)
	compiledRun(t, c)
	v := c.Get("a")
	require.NotNil(t, v)
	require.True(t, v.IsUndefined())
}

func TestDestructureRest(t *testing.T) {
	c := compile(t, `[a, ...rest] := [1, 2, 3]`, nil)
	compiledRun(t, c)
	compiledGet(t, c, "a", int64(1))
	restVar := c.Get("rest")
	require.NotNil(t, restVar)
	rest := restVar.Array()
	require.Equal(t, 2, len(rest))
	require.Equal(t, int64(2), rest[0])
	require.Equal(t, int64(3), rest[1])
}

func TestDestructureNested(t *testing.T) {
	c := compile(t, `[[a, b], c] := [[1, 2], 3]`, nil)
	compiledRun(t, c)
	compiledGet(t, c, "a", int64(1))
	compiledGet(t, c, "b", int64(2))
	compiledGet(t, c, "c", int64(3))
}

func TestDestructureEmptyPatterns(t *testing.T) {
	c := compile(t, `[] := [1]
{} := {x: 1}`, nil)
	compiledRun(t, c)
}

func TestDestructureFuncParams(t *testing.T) {
	c := compile(t, `f := func([a, b]) { return a + b }
out := f([3, 4])`, nil)
	compiledRun(t, c)
	compiledGet(t, c, "out", int64(7))
}

func TestDestructureFuncMapParam(t *testing.T) {
	c := compile(t, `f := func({x, y: z}) { return x + z }
out := f({x: 5, y: 6})`, nil)
	compiledRun(t, c)
	compiledGet(t, c, "out", int64(11))
}

func TestDestructureWithEqualError(t *testing.T) {
	s := tengo.NewScript([]byte(`[a] = [1]`))
	_, err := s.Compile()
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "cannot use destructuring with ="))
}

func TestDestructureRestNotLastError(t *testing.T) {
	s := tengo.NewScript([]byte(`[...a, b] := [1, 2]`))
	_, err := s.Compile()
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "rest element must be last"))
}

func TestDestructurePositionDefault(t *testing.T) {
	c := compile(t, `[a, b = 5] := [1]`, nil)
	compiledRun(t, c)
	compiledGet(t, c, "a", int64(1))
	compiledGet(t, c, "b", int64(5))
}
