package expr_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/internal/testify/assert"
	"github.com/expr-lang/expr/internal/testify/require"
	"github.com/expr-lang/expr/vm/runtime"
)

func TestErrorHandling_tryFunc(t *testing.T) {
	tests := []struct {
		input string
		want  any
	}{
		{`try(1 + 1, 0)`, 2},
		{`try(int("x"), 0)`, 0},
		{`try([1, 2][0], -1)`, 1},
		{`try([1, 2][9], -1)`, -1},
		{`try(int("4"), 0)`, 4},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			out, err := expr.Eval(tt.input, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.want, out)
		})
	}
}

func TestErrorHandling_tryFunc_lazyFallback(t *testing.T) {
	called := 0
	env := map[string]any{
		"boom": func() int {
			called++
			panic("should not evaluate fallback")
		},
	}
	out, err := expr.Eval(`try(41 + 1, boom())`, env)
	require.NoError(t, err)
	assert.Equal(t, 42, out)
	assert.Equal(t, 0, called)
}

func TestErrorHandling_tryFunc_arity(t *testing.T) {
	_, err := expr.Compile(`try(1)`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected 2")

	_, err = expr.Compile(`try(1, 2, 3)`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected 2")
}

func TestErrorHandling_tryCatch(t *testing.T) {
	tests := []struct {
		input string
		want  any
	}{
		{`try { 7 } catch { 0 }`, 7},
		{`try { int("x") } catch { 0 }`, 0},
		{`try { throw("oops") } catch e { string(e) }`, "oops"},
		{`try { throw(42) } catch e { string(e) }`, "42"},
		{`try { [1][9] } catch e is "out of range" { -1 }`, -1},
		{`try { int("x") } catch e is "out of range" { -1 } catch { 8 }`, 8},
		{`try { 1 } finally { 99 }`, 1},
		{`try { int("x") } catch { 3 } finally { 99 }`, 3},
		{`try { throw("a") } catch e { errtype(e) }`, "custom"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			out, err := expr.Eval(tt.input, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.want, out)
		})
	}
}

func TestErrorHandling_finallyOverrides(t *testing.T) {
	_, err := expr.Eval(`try { 1 } finally { throw("boom") }`, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")

	_, err = expr.Eval(`try { throw("a") } catch { 1 } finally { throw("b") }`, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "b")
}

func TestErrorHandling_throwArity(t *testing.T) {
	_, err := expr.Compile(`throw()`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected 1")

	_, err = expr.Compile(`throw(1, 2)`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected 1")
}

func TestErrorHandling_retry(t *testing.T) {
	n := 0
	env := map[string]any{
		"failTwice": func() int {
			n++
			if n <= 2 {
				panic("transient")
			}
			return n
		},
	}
	out, err := expr.Eval(`try { failTwice() } catch { retry }`, env)
	require.NoError(t, err)
	assert.Equal(t, 3, out)
}

func TestErrorHandling_retryLimit(t *testing.T) {
	env := map[string]any{
		"always": func() int {
			panic("nope")
		},
	}
	_, err := expr.Eval(`try { always() } catch { retry }`, env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "retry limit exceeded")
}

func TestErrorHandling_retryOutsideCatch(t *testing.T) {
	_, err := expr.Eval(`retry`, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "retry used outside of catch block")

	_, err = expr.Eval(`try { retry } finally { 0 }`, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "retry used outside of catch block")
}

func TestErrorHandling_errtype(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`try { [1, 2][9] } catch e { errtype(e) }`, "index"},
		{`try { int("nope") } catch e { errtype(e) }`, "conversion"},
		{`try { let x = 1; x[0] } catch e { errtype(e) }`, "type"},
		{`try { let x = nil; x.foo } catch e { errtype(e) }`, "nil"},
		{`try { throw("x") } catch e { errtype(e) }`, "custom"},
		{`errtype(nil)`, "none"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			out, err := expr.Eval(tt.input, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.want, out)
		})
	}
}

func TestErrorHandling_errtype_retry(t *testing.T) {
	env := map[string]any{
		"always": func() int { panic("nope") },
	}
	out, err := expr.Eval(`try { try { always() } catch { retry } } catch e { errtype(e) }`, env)
	require.NoError(t, err)
	assert.Equal(t, "retry", out)
}

func TestErrorHandling_errtypeArity(t *testing.T) {
	_, err := expr.Compile(`errtype()`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected 1")
}

func TestErrorHandling_unmatchedFilterRethrows(t *testing.T) {
	_, err := expr.Eval(`try { throw("hello") } catch e is "index" { 0 }`, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hello")
}

func TestErrorHandling_parseRequiresCatchOrFinally(t *testing.T) {
	_, err := expr.Compile(`try { 1 }`)
	require.Error(t, err)
}

func TestErrorHandling_pipeTry(t *testing.T) {
	out, err := expr.Eval(`int("x") | try(0)`, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, out)
}

func TestErrorHandling_wrapPreservesThrowKind(t *testing.T) {
	e := runtime.NewCustomError("index out of range: 9")
	assert.Equal(t, "custom", runtime.Classify(e))
	assert.Equal(t, "index", runtime.Classify(fmt.Errorf("index out of range: 9")))
	assert.Equal(t, "none", runtime.Classify(nil))
}

func TestErrorHandling_stringContains(t *testing.T) {
	out, err := expr.Eval(`try { throw("abc def") } catch e is "def" { true } catch { false }`, nil)
	require.NoError(t, err)
	assert.Equal(t, true, out)

	msg, err := expr.Eval(`try { [0][1] } catch e { string(e) }`, nil)
	require.NoError(t, err)
	require.IsType(t, "", msg)
	assert.True(t, strings.Contains(msg.(string), "index"))
}
