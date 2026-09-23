// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/format"
)

func TestPartialReconstructsTemplateStrings(t *testing.T) {
	t.Parallel()

	cases := []struct {
		note    string
		module  string
		query   string
		want    string
		support string
	}{
		{
			note: "residual interpolation",
			module: `package test
p := $"hello {input.name}"`,
			query: "data.test.p",
			want:  `$"hello {input.name}"`,
		},
		{
			note: "nested template strings",
			module: `package test
p := $"outer {$"inner {input.x}"}"`,
			query: "data.test.p",
			want:  `$"outer {$"inner {input.x}"}"`,
		},
		{
			note: "nested residual values",
			module: `package test
p := $"a {$"b {input.x} c {input.y}"} d"`,
			query: "data.test.p",
			want:  `$"a {$"b {input.x} c {input.y}"} d"`,
		},
		{
			note: "known value and residual interpolation",
			module: `package test
p if {
	x := 1
	y := $"n={input.n} x={x}"
	y == input.want
}`,
			query: "data.test.p",
			want:  `$"n={input.n} x={1}" = input.want`,
		},
		{
			note: "variable interpolation",
			module: `package test
p if {
	x := input.x
	$"val {x}" == input.want
}`,
			query: "data.test.p",
			want:  `$"val {input.x}" = input.want`,
		},
		{
			note: "ref interpolation",
			module: `package test
p := $"hello {input.user.name}"`,
			query: "data.test.p",
			want:  `$"hello {input.user.name}"`,
		},
		{
			note: "function call interpolation",
			module: `package test
p := $"len {count(input.xs)}"`,
			query: "data.test.p",
			want:  `$"len {count(input.xs)}"`,
		},
		{
			note: "comprehension interpolation",
			module: `package test
p := $"items {[x | x := input.xs[_]]}"`,
			query: "data.test.p",
			want:  `$"items {[__local0__1 | __local0__1 = input.xs[_]]}"`,
		},
		{
			note: "inlined function argument",
			module: `package test
p(x) := $"v {x}"
q := p(input.x)`,
			query: "data.test.q",
			want:  `$"v {input.x}"`,
		},
		{
			note: "query template string",
			module: `package test
p := input.x`,
			query: `$"hello {input.name}"`,
			want:  `$"hello {input.name}"`,
		},
		{
			note: "support module keeps template syntax",
			module: `package test
p := $"hello {input.name}"`,
			query:   `data.test.p with input.name as "alice"`,
			support: `$"hello {input.name}"`,
		},
		{
			note: "object key",
			module: `package test
p[$"k {input.k}"] := input.v`,
			query:   "data.test.p",
			support: `$"k {input.k}"`,
		},
		{
			note: "set membership",
			module: `package test
p contains $"user {input.user}" if input.ok`,
			query:   "data.test.p",
			support: `$"user {input.user}"`,
		},
		{
			note: "object value",
			module: `package test
p := {"msg": $"hello {input.name}"}`,
			query: "data.test.p",
			want:  `_ = {"msg": $"hello {input.name}"}`,
		},
		{
			note: "array of template strings",
			module: `package test
p := [$"a {input.x}", $"b {input.y}"]`,
			query: "data.test.p",
			want:  `_ = [$"a {input.x}", $"b {input.y}"]`,
		},
		{
			note: "negated comparison",
			module: `package test
p if {
	not $"x {input.x}" == input.y
}`,
			query: "data.test.p",
			want:  `not $"x {input.x}" = input.y`,
		},
		{
			note: "with modifier on comparison",
			module: `package test
p if {
	input.ok
	$"user {input.user}" == input.want with data.extra as 1
}`,
			query: "data.test.p",
			want:  `input.ok; __local1__1 = $"user {input.user with data.extra as 1}" with data.extra as 1; __local1__1 = input.want with data.extra as 1`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.note, func(t *testing.T) {
			t.Parallel()

			pq := partialTemplateStrings(t, tc.module, tc.query)
			assertNoInternalTemplateString(t, pq)

			if tc.want != "" {
				if len(pq.Queries) != 1 {
					t.Fatalf("expected 1 query, got %d: %v", len(pq.Queries), pq.Queries)
				}
				if got := pq.Queries[0].String(); got != tc.want {
					t.Fatalf("query = %s, want %s", got, tc.want)
				}
				roundTripBody(t, pq.Queries[0])
			}
			if tc.support != "" {
				found := false
				for _, mod := range pq.Support {
					if strings.Contains(mod.String(), tc.support) {
						found = true
						roundTripModule(t, mod)
					}
				}
				if !found {
					t.Fatalf("support missing %s:\n%v", tc.support, pq.Support)
				}
			}
		})
	}
}

func TestPartialTemplateStringSemantics(t *testing.T) {
	t.Parallel()

	cases := []struct {
		note   string
		module string
		query  string
		input  map[string]any
	}{
		{
			note: "residual interpolation",
			module: `package test
p := $"hello {input.name}"`,
			query: "data.test.p",
			input: map[string]any{"name": "alice"},
		},
		{
			note: "nested template strings",
			module: `package test
p := $"outer {$"inner {input.x}"}"`,
			query: "data.test.p",
			input: map[string]any{"x": "z"},
		},
		{
			note: "nested residual values",
			module: `package test
p := $"a {$"b {input.x} c {input.y}"} d"`,
			query: "data.test.p",
			input: map[string]any{"x": "X", "y": "Y"},
		},
		{
			note: "comparison",
			module: `package test
p if {
	x := 1
	y := $"n={input.n} x={x}"
	y == input.want
}`,
			query: "data.test.p",
			input: map[string]any{"n": "N", "want": "n=N x=1"},
		},
		{
			note: "function call",
			module: `package test
p := $"len {count(input.xs)}"`,
			query: "data.test.p",
			input: map[string]any{"xs": []any{"a", "b", "c"}},
		},
		{
			note: "comprehension",
			module: `package test
p := $"items {[x | x := input.xs[_]]}"`,
			query: "data.test.p",
			input: map[string]any{"xs": []any{"a", "b"}},
		},
		{
			note: "ref",
			module: `package test
p := $"hello {input.user.name}"`,
			query: "data.test.p",
			input: map[string]any{"user": map[string]any{"name": "ada"}},
		},
		{
			note: "object key",
			module: `package test
p[$"k {input.k}"] := input.v`,
			query: "data.test.p",
			input: map[string]any{"k": "id", "v": 7},
		},
		{
			note: "set membership",
			module: `package test
p contains $"user {input.user}" if input.ok`,
			query: "data.test.p",
			input: map[string]any{"ok": true, "user": "ada"},
		},
		{
			note: "object value",
			module: `package test
p := {"msg": $"hello {input.name}"}`,
			query: "data.test.p",
			input: map[string]any{"name": "ada"},
		},
		{
			note: "array of template strings",
			module: `package test
p := [$"a {input.x}", $"b {input.y}"]`,
			query: "data.test.p",
			input: map[string]any{"x": "X", "y": "Y"},
		},
		{
			note: "negated comparison",
			module: `package test
p if {
	not $"x {input.x}" == input.y
}`,
			query: "data.test.p",
			input: map[string]any{"x": "1", "y": "nope"},
		},
		{
			note: "with on interpolation",
			module: `package test
p if {
	input.ok
	$"user {input.user}" == input.want with data.extra as 1
}`,
			query: "data.test.p",
			input: map[string]any{"ok": true, "user": "ada", "want": "user ada"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.note, func(t *testing.T) {
			t.Parallel()

			full := evalValue(t, New(Query(tc.query), Module("m.rego", tc.module), Input(tc.input)))

			pr, err := New(Query(tc.query), Module("m.rego", tc.module)).PartialResult(t.Context())
			if err != nil {
				t.Fatalf("partial result: %v", err)
			}
			got := evalValue(t, pr.Rego(Input(tc.input)))
			if fullJSON, gotJSON := mustJSON(t, full), mustJSON(t, got); fullJSON != gotJSON {
				t.Fatalf("partial result eval = %s, full eval = %s", gotJSON, fullJSON)
			}

			pq, err := pr.Rego().Partial(t.Context())
			if err != nil {
				t.Fatalf("partial after partial result: %v", err)
			}
			assertNoInternalTemplateString(t, pq)
		})
	}
}

func TestPartialTemplateStringWithModifierSemantics(t *testing.T) {
	t.Parallel()

	module := `package test
p := $"hello {input.name}"`
	query := `data.test.p with input.name as "alice"`
	full := evalValue(t, New(Query(query), Module("m.rego", module)))
	got := evalPartialQueries(t, partialTemplateStrings(t, module, query), nil)
	if mustJSON(t, full) != mustJSON(t, got) {
		t.Fatalf("partial query eval = %s, full eval = %s", mustJSON(t, got), mustJSON(t, full))
	}
}

func TestPartialTemplateStringSourceRoundTrip(t *testing.T) {
	t.Parallel()

	module := `package test
p := $"hello {input.name}"
q if {
	input.ok
	$"user {input.user}" == input.want
}`
	pq := partialTemplateStrings(t, module, "data.test.q")
	assertNoInternalTemplateString(t, pq)
	if len(pq.Queries) != 1 {
		t.Fatalf("expected 1 query, got %v", pq.Queries)
	}
	roundTripBody(t, pq.Queries[0])

	src, err := format.AstWithOpts(pq.Queries[0], format.Opts{IgnoreLocations: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `$"user {input.user}"`) {
		t.Fatalf("source form lost the template string:\n%s", src)
	}
}

func evalPartialQueries(t *testing.T, pq *PartialQueries, input any) any {
	t.Helper()

	if len(pq.Queries) != 1 {
		t.Fatalf("expected 1 query, got %d: %v", len(pq.Queries), pq.Queries)
	}
	opts := make([]func(*Rego), 0, 2+len(pq.Support))
	opts = append(opts, ParsedQuery(pq.Queries[0]), Input(input))
	for _, mod := range pq.Support {
		opts = append(opts, ParsedModule(mod))
	}
	rs, err := New(opts...).Eval(t.Context())
	if err != nil {
		t.Fatalf("eval partial: %v", err)
	}
	if len(rs) == 0 || len(rs[0].Expressions) == 0 {
		return nil
	}
	// Partial evaluation often rewrites a reference into an equality plus the
	// captured value. The captured value is the last expression.
	return rs[0].Expressions[len(rs[0].Expressions)-1].Value
}

func partialTemplateStrings(t *testing.T, module, query string) *PartialQueries {
	t.Helper()

	pq, err := New(
		Query(query),
		Module("m.rego", module),
		Unknowns([]string{"input"}),
	).Partial(t.Context())
	if err != nil {
		t.Fatalf("partial: %v", err)
	}
	return pq
}

func assertNoInternalTemplateString(t *testing.T, pq *PartialQueries) {
	t.Helper()

	for i, query := range pq.Queries {
		if strings.Contains(query.String(), ast.InternalTemplateString.Name) {
			t.Fatalf("query %d still contains %s: %s", i, ast.InternalTemplateString.Name, query)
		}
	}
	for i, mod := range pq.Support {
		if strings.Contains(mod.String(), ast.InternalTemplateString.Name) {
			t.Fatalf("support module %d still contains %s:\n%s", i, ast.InternalTemplateString.Name, mod)
		}
	}
}

func roundTripBody(t *testing.T, body ast.Body) {
	t.Helper()

	src, err := format.AstWithOpts(body, format.Opts{IgnoreLocations: true})
	if err != nil {
		t.Fatalf("format query: %v", err)
	}
	if strings.Contains(string(src), ast.InternalTemplateString.Name) {
		t.Fatalf("formatted query still contains %s:\n%s", ast.InternalTemplateString.Name, src)
	}
	if _, err := ast.ParseBody(string(src)); err != nil {
		t.Fatalf("parse formatted query %s: %v", src, err)
	}
}

func roundTripModule(t *testing.T, mod *ast.Module) {
	t.Helper()

	src, err := format.AstWithOpts(mod, format.Opts{IgnoreLocations: true, RegoVersion: mod.RegoVersion()})
	if err != nil {
		t.Fatalf("format module: %v", err)
	}
	if strings.Contains(string(src), ast.InternalTemplateString.Name) {
		t.Fatalf("formatted module still contains %s:\n%s", ast.InternalTemplateString.Name, src)
	}
	if _, err := ast.ParseModuleWithOpts("partial.rego", string(src), ast.ParserOptions{RegoVersion: mod.RegoVersion()}); err != nil {
		t.Fatalf("parse formatted module:\n%s\n%v", src, err)
	}
}

func evalValue(t *testing.T, r *Rego) any {
	t.Helper()

	rs, err := r.Eval(t.Context())
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if len(rs) == 0 || len(rs[0].Expressions) == 0 {
		return nil
	}
	return rs[0].Expressions[0].Value
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
