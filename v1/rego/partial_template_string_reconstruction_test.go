package rego

import (
	"context"
	"strings"
	"testing"

	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/format"
)

func TestPartialReconstructsTemplateStringsInResidualQueries(t *testing.T) {
	t.Parallel()

	pq, err := New(
		Query(`$"hello {input.name}" == input.expected`),
		Unknowns([]string{"input.name", "input.expected"}),
	).Partial(context.Background())
	if err != nil {
		t.Fatalf("unexpected partial evaluation error: %v", err)
	}

	if len(pq.Queries) == 0 {
		t.Fatal("expected at least one residual query")
	}

	foundTemplateString := false
	for i := range pq.Queries {
		got := mustFormatBody(t, pq.Queries[i])
		if strings.Contains(got, "internal.template_string") {
			t.Fatalf("expected residual query to hide internal builtin but got %s", got)
		}
		if strings.Contains(got, `$"hello {input.name}"`) && strings.Contains(got, "input.expected") {
			foundTemplateString = true
		}
	}

	if !foundTemplateString {
		t.Fatalf("expected reconstructed template string in residual queries but got %#v", pq.Queries)
	}
}

func TestPartialReconstructsTemplateStringsInSupportModules(t *testing.T) {
	t.Parallel()

	mod := `package test
import rego.v1

msg := $"hello {input.name}"

p if {
	data.test.msg == input.expected
}`

	pq, err := New(
		Query("data.test.p"),
		Module("test.rego", mod),
		Unknowns([]string{"input.name", "input.expected"}),
		DisableInlining([]string{"data.test.msg"}),
	).Partial(context.Background())
	if err != nil {
		t.Fatalf("unexpected partial evaluation error: %v", err)
	}

	if len(pq.Support) == 0 {
		t.Fatal("expected at least one support module")
	}

	foundTemplateString := false
	for i := range pq.Support {
		got := mustFormatModule(t, pq.Support[i], pq.Support[i].RegoVersion())
		if strings.Contains(got, "internal.template_string") {
			t.Fatalf("expected support module to hide internal builtin but got %s", got)
		}
		if strings.Contains(got, `$"hello {input.name}"`) {
			foundTemplateString = true
		}
	}

	if !foundTemplateString {
		t.Fatalf("expected support modules to contain reconstructed template strings but got %#v", pq.Support)
	}
}

func TestPartialResultReconstructsTemplateStringsInGeneratedModules(t *testing.T) {
	t.Parallel()

	mod := `package test
import rego.v1

msg := $"hello {input.name}"

p if {
	data.test.msg == input.expected
}`

	pr, err := New(
		Query("data.test.p"),
		Module("test.rego", mod),
		Unknowns([]string{"input.name", "input.expected"}),
		DisableInlining([]string{"data.test.msg"}),
	).PartialResult(context.Background())
	if err != nil {
		t.Fatalf("unexpected partial result error: %v", err)
	}

	pq, err := pr.Rego(
		Unknowns([]string{"input.name", "input.expected"}),
	).Partial(context.Background())
	if err != nil {
		t.Fatalf("unexpected partial evaluation error from partial result: %v", err)
	}

	foundTemplateString := false

	for i := range pq.Queries {
		formatted := mustFormatBody(t, pq.Queries[i])
		if strings.Contains(formatted, "internal.template_string") {
			t.Fatalf("expected partial result residual query to hide internal builtin but got %s", formatted)
		}
		if strings.Contains(formatted, `$"hello {input.name}"`) {
			foundTemplateString = true
		}
	}

	for i := range pq.Support {
		formatted := mustFormatModule(t, pq.Support[i], pq.Support[i].RegoVersion())
		if strings.Contains(formatted, "internal.template_string") {
			t.Fatalf("expected partial result support module to hide internal builtin but got %s", formatted)
		}
		if strings.Contains(formatted, `$"hello {input.name}"`) {
			foundTemplateString = true
		}
	}

	if !foundTemplateString {
		t.Fatal("expected partial result to preserve reconstructed template strings through public APIs")
	}
}

func TestPartialReconstructsNestedTemplateStrings(t *testing.T) {
	t.Parallel()

	mod := "package test\nimport rego.v1\np if {\n  x := $`<foo>\n  <bar>{$\"a {input.name} b\"}</bar>\n</foo>`\n  x == input.expected\n}"

	pq, err := New(
		Query("data.test.p"),
		Module("test.rego", mod),
		Unknowns([]string{"input.name", "input.expected"}),
	).Partial(context.Background())
	if err != nil {
		t.Fatalf("unexpected partial evaluation error: %v", err)
	}

	if len(pq.Queries) == 0 {
		t.Fatal("expected at least one residual query")
	}

	foundNestedTemplateString := false
	for i := range pq.Queries {
		got := mustFormatBody(t, pq.Queries[i])
		if strings.Contains(got, "internal.template_string") {
			t.Fatalf("expected nested residual query to hide internal builtin but got %s", got)
		}
		if strings.Contains(got, `{$"a {`) && strings.Contains(got, ` b"}`) {
			foundNestedTemplateString = true
		}
	}

	if !foundNestedTemplateString {
		t.Fatalf("expected nested residual query to contain reconstructed nested template string but got %#v", pq.Queries)
	}
}

func mustFormatBody(t *testing.T, body ast.Body) string {
	t.Helper()

	bs, err := format.AstWithOpts(body, format.Opts{IgnoreLocations: true, RegoVersion: ast.RegoV1})
	if err != nil {
		t.Fatalf("unexpected format error: %v", err)
	}

	return strings.TrimSpace(string(bs))
}

func mustFormatModule(t *testing.T, mod *ast.Module, regoVersion ast.RegoVersion) string {
	t.Helper()

	bs, err := format.AstWithOpts(mod, format.Opts{IgnoreLocations: true, RegoVersion: regoVersion})
	if err != nil {
		t.Fatalf("unexpected format error: %v", err)
	}

	return string(bs)
}
