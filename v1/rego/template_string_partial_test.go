// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import (
	"fmt"
	"strings"
	"testing"

	"github.com/open-policy-agent/opa/v1/ast"
)

func TestPartialTemplateStringReconstruction(t *testing.T) {
	t.Parallel()

	cases := []struct {
		note    string
		query   string
		module  string
		input   string
		want    []string
		forbid  string
		wantVal string
	}{
		{
			note:    "residual interpolation",
			query:   `$"hello {input.name}!"`,
			input:   `{"name":"ada"}`,
			want:    []string{`$"hello {input.name}!"`},
			wantVal: `"hello ada!"`,
		},
		{
			note:    "nested template",
			query:   `$"outer {$"inner {input.x}"}"`,
			input:   `{"x":"z"}`,
			want:    []string{`$"outer {$"inner {input.x}"}"`},
			wantVal: `"outer inner z"`,
		},
		{
			note:  "rule head",
			query: "data.test.p = x",
			module: `package test
p := $"hello {input.name}"`,
			input:   `{"name":"ada"}`,
			want:    []string{`x = $"hello {input.name}"`},
			wantVal: `"hello ada"`,
		},
		{
			note:  "support contains",
			query: "data.test.p = x",
			module: `package test
p contains $"id {input.user}" if {
	input.user
}`,
			input:   `{"user":"ada"}`,
			want:    []string{`$"id {input.user}"`, "input.user"},
			wantVal: `["id ada"]`,
		},
		{
			note:  "nested in rule",
			query: "data.test.p = x",
			module: `package test
p := $"a {$"b {input.x}"} {input.y}"`,
			input:   `{"x":"X","y":"Y"}`,
			want:    []string{`$"a {$"b {input.x}"} {input.y}"`},
			wantVal: `"a b X Y"`,
		},
		{
			note:  "call inside",
			query: "data.test.p = x",
			module: `package test
p := $"n {count(input.xs)}"`,
			input:   `{"xs":[1,2,3]}`,
			want:    []string{`$"n {count(input.xs)}"`},
			wantVal: `"n 3"`,
		},
		{
			note:  "user comprehension",
			query: "data.test.p = x",
			module: `package test
p := [ $"v {x}" |
	x := input.xs[_]
]`,
			input:   `{"xs":["a","b"]}`,
			want:    []string{`$"v {`, "input.xs[_]"},
			wantVal: `["v a", "v b"]`,
		},
		{
			note:  "with inside",
			query: `$"{data.foo with data.foo as input.bar}"`,
			input: `{"bar":"BAR"}`,
			want:  []string{`$"{data.foo with data.foo as input.bar}"`},
		},
		{
			note:    "sprintf mix",
			query:   `sprintf("hi %s", [$"u {input.u}"])`,
			input:   `{"u":"ada"}`,
			want:    []string{`sprintf("hi %s", [$"u {input.u}"])`},
			wantVal: `"hi u ada"`,
		},
		{
			note:  "object key",
			query: "data.test.p = x",
			module: `package test
p[$"k {input.k}"] := input.v`,
			input: `{"k":"id","v":7}`,
			want:  []string{`$"k {input.k}"`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.note, func(t *testing.T) {
			t.Parallel()

			opts := []func(*Rego){
				Query(tc.query),
				SetRegoVersion(ast.RegoV1),
			}
			if tc.module != "" {
				opts = append(opts, Module("test.rego", tc.module))
			}
			pq, err := New(opts...).Partial(t.Context())
			if err != nil {
				t.Fatalf("partial: %v", err)
			}
			got := partialText(pq)
			if strings.Contains(got, "internal.template_string") {
				t.Fatalf("lowered builtin leaked into partial result:\n%s", got)
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Fatalf("result missing %q\n%s", w, got)
				}
			}
			if err := parsePartial(pq); err != nil {
				t.Fatalf("reconstructed partial result is not valid Rego: %v\n%s", err, got)
			}
			if tc.input != "" {
				assertReconstructedEval(t, pq, tc.input, tc.wantVal)
			}
		})
	}
}

func TestPartialResultTemplateStringRoundTrip(t *testing.T) {
	t.Parallel()

	module := `package test
p := $"hello {input.name}"`
	pr, err := New(
		Query("data.test.p"),
		Module("test.rego", module),
		SetRegoVersion(ast.RegoV1),
	).PartialResult(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	pq, err := pr.Rego(SetRegoVersion(ast.RegoV1)).Partial(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := partialText(pq)
	if strings.Contains(got, "internal.template_string") {
		t.Fatalf("second partial leaked internal.template_string:\n%s", got)
	}
	if !strings.Contains(got, `$"hello {input.name}"`) {
		t.Fatalf("second partial lost template string:\n%s", got)
	}
}

func TestPartialResultCompilesResidualTemplateRules(t *testing.T) {
	t.Parallel()

	cases := []struct {
		note   string
		query  string
		module string
	}{
		{
			note:  "contains",
			query: "data.test.p",
			module: `package test
p contains $"id {input.user}" if {
	input.user
}`,
		},
		{
			note:  "object key",
			query: "data.test.p",
			module: `package test
p[$"k {input.k}"] := input.v`,
		},
		{
			note:  "negation",
			query: "data.test.p",
			module: `package test
p if {
	not $"a {input.a}" == "nope"
	input.ok
}`,
		},
		{
			note:  "every",
			query: "data.test.p",
			module: `package test
p if {
	every x in input.xs {
		$"ok {x}"
	}
}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.note, func(t *testing.T) {
			t.Parallel()
			_, err := New(
				Query(tc.query),
				Module("test.rego", tc.module),
				SetRegoVersion(ast.RegoV1),
			).PartialResult(t.Context())
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPartialTemplateStringUndefinedPartStaysLowered(t *testing.T) {
	t.Parallel()

	pq, err := New(
		Query("data.test.p = x"),
		Module("test.rego", `package test
p := $"u {data.missing} {input.x}"`),
		SetRegoVersion(ast.RegoV1),
	).Partial(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// An undefined interpolation is saved as an empty set, which stringifies to
	// "<undefined>" and cannot be written back as a template-string expression.
	if !strings.Contains(partialText(pq), "internal.template_string") {
		t.Fatalf("expected undefined interpolation to remain lowered, got:\n%s", partialText(pq))
	}
}

func partialText(pq *PartialQueries) string {
	var b strings.Builder
	for _, q := range pq.Queries {
		fmt.Fprintf(&b, "%s\n", q)
	}
	for _, mod := range pq.Support {
		fmt.Fprintf(&b, "%s\n", mod)
	}
	return b.String()
}

func parsePartial(pq *PartialQueries) error {
	for i, q := range pq.Queries {
		if len(q) == 0 {
			continue
		}
		if _, err := ast.ParseBody(q.String()); err != nil {
			return fmt.Errorf("query %d: %w", i, err)
		}
	}
	for i, mod := range pq.Support {
		if _, err := ast.ParseModule("support.rego", mod.String()); err != nil {
			return fmt.Errorf("support %d: %w", i, err)
		}
	}
	return nil
}

func assertReconstructedEval(t *testing.T, pq *PartialQueries, input, want string) {
	t.Helper()
	if want == "" || len(pq.Queries) != 1 {
		return
	}

	opts := []func(*Rego){
		Query(pq.Queries[0].String()),
		SetRegoVersion(ast.RegoV1),
		ParsedInput(ast.MustParseTerm(input).Value),
	}
	for i, mod := range pq.Support {
		opts = append(opts, Module(fmt.Sprintf("support%d.rego", i), mod.String()))
	}
	rs, err := New(opts...).Eval(t.Context())
	if err != nil {
		t.Fatalf("eval reconstructed partial query: %v\n%s", err, partialText(pq))
	}
	if len(rs) == 0 {
		t.Fatalf("reconstructed partial query undefined, want %s\n%s", want, partialText(pq))
	}

	got := resultValue(rs[0])
	if got != want {
		t.Fatalf("reconstructed eval %s, want %s\n%s", got, want, partialText(pq))
	}
}

func resultValue(r Result) string {
	if v, ok := r.Bindings["x"]; ok {
		return ast.MustInterfaceToValue(v).String()
	}
	if len(r.Expressions) == 1 && r.Expressions[0].Value != nil {
		return ast.MustInterfaceToValue(r.Expressions[0].Value).String()
	}
	return fmt.Sprint(r)
}
