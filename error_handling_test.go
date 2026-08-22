package expr_test

import (
	"strings"
	"testing"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/internal/testify/require"
)

func TestErrorHandling(t *testing.T) {
	tests := []struct {
		input string
		want  any
	}{
		{`try(throw("boom"), 42)`, 42},
		{`try { throw("boom") } catch { 42 }`, 42},
		{`try { throw("boom") } catch e is "boom" { errtype(e) }`, "custom"},
		{`try { throw("boom") } catch e { errtype(e) }`, "custom"},
		{`try { 1 / 0 } catch { 1 } finally { 2 }`, 2},
	}
	for _, test := range tests {
		got, err := expr.Eval(test.input, nil)
		require.NoError(t, err, test.input)
		require.Equal(t, test.want, got, test.input)
	}
}

func TestRetryOutsideCatch(t *testing.T) {
	_, err := expr.Eval(`retry`, nil)
	require.Error(t, err)
}

func TestRetryExhaustion(t *testing.T) {
	_, err := expr.Eval(`try { throw("boom") } catch { retry }`, nil)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "retry limit exhausted"))
}
