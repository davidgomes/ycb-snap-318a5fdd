// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import (
	"slices"
	"strings"
	"testing"

	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/format"
)

func TestPartialReconstructsTemplateStringsInResidualQueries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		note    string
		query   string
		module  string
		unknown string
		want    string
		wantAlt string
	}{
		{
			note:    "query template with residual interpolation",
			query:   `$"{input.x}"`,
			unknown: "input",
			want:    `$"{input.x}"`,
		},
		{
			note:    "query template with residual interpolation and literal text",
			query:   `$"user: {input.name}"`,
			unknown: "input",
			want:    `$"user: {input.name}"`,
		},
		{
			note:    "query template with mixed known and residual parts",
			query:   `$"n={1 + 1} x={input.x}"`,
			unknown: "input",
			want:    `$"n={2} x={input.x}"`,
			wantAlt: `$"n=2 x={input.x}"`,
		},
		{
			note:    "nested template string remains representable",
			query:   `$"outer {$"inner {input.x}"}"`,
			unknown: "input",
			want:    `$"outer {$"inner {input.x}"}"`,
		},
		{
			note:  "rule head template with residual interpolation",
			query: "data.test.p",
			module: `package test
p := $"{input.x}"
`,
			unknown: "input",
			want:    `$"{input.x}"`,
		},
		{
			note:  "rule body assignment of residual template",
			query: "data.test.p",
			module: `package test
p := x if {
	x := $"id:{input.x}"
}
`,
			unknown: "input",
			want:    `$"id:{input.x}"`,
		},
		{
			note:  "comprehension containing residual template",
			query: "data.test.p",
			module: `package test
p := [x | x := $"{input.x}"]
`,
			unknown: "input",
			want:    `[__local0__1 | __local0__1 = $"{input.x}"]`,
			wantAlt: `_ = [__local0__1 | __local0__1 = $"{input.x}"]; _`,
		},
		{
			note:  "nested template string in rule head",
			query: "data.test.p",
			module: `package test
p := $"foo {$"bar {input.x}"}"
`,
			unknown: "input",
			want:    `$"foo {$"bar {input.x}"}"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.note, func(t *testing.T) {
			t.Parallel()

			opts := []func(*Rego){
				Query(tc.query),
				Unknowns([]string{tc.unknown}),
			}
			if tc.module != "" {
				opts = append(opts, Module("test.rego", tc.module))
			}

			pq, err := New(opts...).Partial(t.Context())
			if err != nil {
				t.Fatalf("Partial(): %v", err)
			}

			assertNoInternalTemplateString(t, pq)
			if len(pq.Queries) != 1 {
				t.Fatalf("expected 1 residual query, got %d: %v", len(pq.Queries), pq.Queries)
			}

			got := strings.TrimSpace(pq.Queries[0].String())
			accepted := []string{strings.TrimSpace(tc.want)}
			if tc.wantAlt != "" {
				accepted = append(accepted, strings.TrimSpace(tc.wantAlt))
			}
			if parsed := strings.TrimSpace(ast.MustParseBody(tc.want).String()); parsed != "" {
				accepted = append(accepted, parsed)
			}
			if !slices.Contains(accepted, got) {
				t.Fatalf("unexpected residual query:\nwant: %s\n got: %s", tc.want, got)
			}
		})
	}
}

func TestPartialReconstructsTemplateStringsInSupportModules(t *testing.T) {
	t.Parallel()

	mod := `package test
p := $"{input.x}"
`

	pq, err := New(
		Query("data.test.p"),
		Module("test.rego", mod),
		Unknowns([]string{"input"}),
		DisableInlining([]string{"data.test.p"}),
	).Partial(t.Context())
	if err != nil {
		t.Fatalf("Partial(): %v", err)
	}

	assertNoInternalTemplateString(t, pq)
	if len(pq.Support) == 0 {
		t.Fatal("expected at least one support module")
	}

	found := false
	for _, module := range pq.Support {
		src := string(mustFormatModule(t, module))
		if strings.Contains(src, "$\"{input.x}\"") || strings.Contains(src, `$"{input.x}"`) {
			found = true
		}
		if strings.Contains(src, ast.InternalTemplateString.Name) {
			t.Fatalf("support module still exposes %s:\n%s", ast.InternalTemplateString.Name, src)
		}
	}
	if !found {
		srcs := make([]string, 0, len(pq.Support))
		for _, module := range pq.Support {
			srcs = append(srcs, string(mustFormatModule(t, module)))
		}
		t.Fatalf("support modules did not preserve template-string syntax:\n%s", strings.Join(srcs, "\n"))
	}
}

func TestPartialResultReconstructsTemplateStringsForFurtherPartialEval(t *testing.T) {
	t.Parallel()

	mod := `package test
p := $"hello {input.x}"
`

	r := New(
		Query("data.test.p"),
		Module("test.rego", mod),
		Unknowns([]string{"input"}),
	)

	pr, err := r.PartialResult(t.Context())
	if err != nil {
		t.Fatalf("PartialResult(): %v", err)
	}

	rs, err := pr.Rego(Input(map[string]any{"x": "opa"})).Eval(t.Context())
	if err != nil {
		t.Fatalf("PartialResult eval: %v", err)
	}
	if len(rs) == 0 || len(rs[0].Expressions) == 0 {
		t.Fatalf("expected a result, got %v", rs)
	}
	if got, want := rs[0].Expressions[0].Value, "hello opa"; got != want {
		t.Fatalf("expected %q, got %#v", want, got)
	}

	pq, err := pr.Rego(Unknowns([]string{"input"})).Partial(t.Context())
	if err != nil {
		t.Fatalf("PartialResult reused Partial(): %v", err)
	}

	assertNoInternalTemplateString(t, pq)
	if len(pq.Queries) != 1 {
		t.Fatalf("expected 1 residual query, got %d: %v", len(pq.Queries), pq.Queries)
	}

	got := strings.TrimSpace(pq.Queries[0].String())
	if !strings.Contains(got, `$"hello {input.x}"`) && !strings.Contains(got, "hello {input.x}") {
		t.Fatalf("reused partial evaluation did not preserve template-string syntax: %s", got)
	}
}

func TestPartialTemplateStringSourceIsOrdinaryRego(t *testing.T) {
	t.Parallel()

	pq, err := New(
		Query(`$"hi {input.x}"`),
		Unknowns([]string{"input"}),
	).Partial(t.Context())
	if err != nil {
		t.Fatalf("Partial(): %v", err)
	}

	assertNoInternalTemplateString(t, pq)

	bs, err := format.AstWithOpts(pq.Queries[0], format.Opts{IgnoreLocations: true})
	if err != nil {
		t.Fatalf("format residual query: %v", err)
	}
	src := string(bs)
	if strings.Contains(src, ast.InternalTemplateString.Name) {
		t.Fatalf("formatted source still contains %s:\n%s", ast.InternalTemplateString.Name, src)
	}
	if !strings.Contains(src, `$"hi {input.x}"`) && !strings.Contains(src, "hi {input.x}") {
		t.Fatalf("formatted source did not reconstruct the template string:\n%s", src)
	}
}

func assertNoInternalTemplateString(t *testing.T, pq *PartialQueries) {
	t.Helper()

	var found bool
	vis := ast.NewGenericVisitor(func(x any) bool {
		switch node := x.(type) {
		case ast.Ref:
			if node.Equal(ast.InternalTemplateString.Ref()) {
				found = true
			}
		case *ast.Term:
			if isInternalTemplateStringTerm(node) {
				found = true
			}
		}
		return false
	})

	for _, query := range pq.Queries {
		vis.Walk(query)
	}
	for _, module := range pq.Support {
		vis.Walk(module)
	}

	if found {
		t.Fatalf("partial evaluation leaked %s: queries=%v support=%v", ast.InternalTemplateString.Name, pq.Queries, pq.Support)
	}
}

func mustFormatModule(t *testing.T, module *ast.Module) []byte {
	t.Helper()
	bs, err := format.AstWithOpts(module, format.Opts{IgnoreLocations: true, RegoVersion: module.RegoVersion()})
	if err != nil {
		t.Fatalf("format module: %v", err)
	}
	return bs
}
