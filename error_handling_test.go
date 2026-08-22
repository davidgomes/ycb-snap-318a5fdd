package expr_test

import (
	"errors"
	"testing"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/internal/testify/require"
)

func TestErrorHandlingFunctionForm(t *testing.T) {
	got, err := expr.Eval(`try(1 % 0, 42)`, nil)
	require.NoError(t, err)
	require.Equal(t, 42, got)

	program, err := expr.Compile(`try(1 % 0, 42)`)
	require.NoError(t, err)
	got, err = expr.Run(program, nil)
	require.NoError(t, err)
	require.Equal(t, 42, got)

	got, err = expr.Eval(`try(1, 1 % 0)`, nil)
	require.NoError(t, err)
	require.Equal(t, 1, got)
}

func TestErrorHandlingBuiltinArity(t *testing.T) {
	for _, code := range []string{`throw()`, `throw(1, 2)`, `errtype()`, `errtype(1, 2)`} {
		_, err := expr.Eval(code, nil)
		require.Error(t, err)
	}
}

func TestErrorHandlingBlockForm(t *testing.T) {
	tests := []struct {
		name string
		code string
		want any
	}{
		{"fallback", `try { 1 % 0 } catch { 42 }`, 42},
		{"bound custom error", `try { throw("boom") } catch err { errtype(err) }`, "custom"},
		{"filtered catch", `try { throw("boom") } catch err is "boom" { 42 }`, 42},
		{"finally preserves result", `try { 1 } catch { 2 } finally { 3 }`, 1},
		{"index classification", `try { [1][2] } catch err { errtype(err) }`, "index"},
		{"conversion classification", `try { int("bad") } catch err { errtype(err) }`, "conversion"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := expr.Eval(tt.code, nil)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}

	program, err := expr.Compile(`try { 1 % 0 } catch { 42 }`)
	require.NoError(t, err)
	got, err := expr.Run(program, nil)
	require.NoError(t, err)
	require.Equal(t, 42, got)
}

func TestErrorHandlingNilClassification(t *testing.T) {
	type env struct {
		Value *struct {
			Name string
		}
	}

	got, err := expr.Eval(`try { Value.Name } catch err { errtype(err) }`, env{})
	require.NoError(t, err)
	require.Equal(t, "nil", got)
}

func TestErrorHandlingFinallyErrorOverridesResult(t *testing.T) {
	_, err := expr.Eval(`try { 1 } catch { 2 } finally { 1 % 0 }`, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "integer divide by zero")

	_, err = expr.Eval(`try { throw("body") } catch { throw("handler") } finally { 3 }`, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "handler")
}

func TestErrorHandlingRetry(t *testing.T) {
	attempts := 0
	env := map[string]any{
		"work": func() (int, error) {
			attempts++
			if attempts < 3 {
				return 0, errors.New("not yet")
			}
			return 42, nil
		},
	}

	got, err := expr.Eval(`try { work() } catch { retry }`, env)
	require.NoError(t, err)
	require.Equal(t, 42, got)
	require.Equal(t, 3, attempts)
}

func TestErrorHandlingRetryExhaustion(t *testing.T) {
	got, err := expr.Eval(
		`try { try { throw("boom") } catch { retry } } catch err { errtype(err) }`,
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, "retry", got)
}

func TestErrorHandlingRetryOutsideCatch(t *testing.T) {
	got, err := expr.Eval(`try { retry } catch err { errtype(err) }`, nil)
	require.NoError(t, err)
	require.Equal(t, "custom", got)
}
