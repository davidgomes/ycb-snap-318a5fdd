// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import (
	"strings"
	"testing"

	"github.com/open-policy-agent/opa/v1/ast"
)

func TestPartialTemplateStringRestored(t *testing.T) {
	t.Parallel()

	cases := []struct {
		note    string
		query   string
		module  string
		exp     []string
		support []string
	}{
		{
			note:  "residual interpolation",
			query: `x = $"hello {input.name}"`,
			exp:   []string{`x = $"hello {input.name}"`},
		},
		{
			note:  "nested template string",
			query: `x = $"hello {$"inner {input.name}"}"`,
			exp:   []string{`x = $"hello {$"inner {input.name}"}"`},
		},
		{
			note:  "deeper nesting",
			query: `x = $"a {$"b {$"c {input.c}"}"}"`,
			exp:   []string{`x = $"a {$"b {$"c {input.c}"}"}"`},
		},
		{
			note:  "known scalar stays in the template",
			query: `x = $"a {input.a} b {1 + 2} c {input.c}"`,
			exp:   []string{`x = $"a {input.a} b {3} c {input.c}"`},
		},
		{
			note:  "user variable is not inlined",
			query: `x = input.a; y = $"pre {x} post"`,
			exp:   []string{`x = input.a; y = $"pre {x} post"`},
		},
		{
			note:  "reused user variable",
			query: `x = input.a; y = $"a {x}"; z = $"b {x}"`,
			exp:   []string{`x = input.a; y = $"a {x}"; z = $"b {x}"`},
		},
		{
			note:  "call interpolation",
			query: `x = $"len {count(input.arr)}"`,
			exp:   []string{`x = $"len {count(input.arr)}"`},
		},
		{
			note:  "array interpolation",
			query: `x = $"{[input.a]}"`,
			exp:   []string{`x = $"{[input.a]}"`},
		},
		{
			note:  "set interpolation",
			query: `x = $"{ {input.a} }"`,
			exp:   []string{`x = $"{{input.a}}"`},
		},
		{
			note:  "object interpolation",
			query: `x = $"{ {"a": input.a} }"`,
			exp:   []string{`x = $"{{"a": input.a}}"`},
		},
		{
			note:  "with modifier",
			query: `x = $"{data.f with input as input.x}"`,
			exp:   []string{`x = $"{data.f with input as input.x}"`},
		},
		{
			note:  "comprehension interpolation",
			query: `x = $"{[y | y = input.ys[_]]}"`,
			exp:   []string{`x = $"{[y | y = input.ys[_]]}"`},
		},
		{
			note:  "negated template",
			query: `not $"x {input.a}"`,
			exp:   []string{`not $"x {input.a}"`},
		},
		{
			note:   "rule inlined into the query",
			query:  `data.test.p = x`,
			module: "package test\np := $\"hello {input.name}\"",
			exp:    []string{`x = $"hello {input.name}"`},
		},
		{
			note:   "support module",
			query:  `data.test.p = x`,
			module: "package test\np contains $\"k {input.k}\" if input.ok",
			exp:    []string{`data.partial.test.p = x`},
			support: []string{
				"package partial.test\n\np contains __local1__1 if { input.ok; __local1__1 = $\"k {input.k}\" }",
			},
		},
		{
			note:   "ref head key",
			query:  `data.test.p = x`,
			module: "package test\np[$\"{input.k}\"] := input.v",
			exp:    []string{`data.partial.test.p = x`},
		},
		{
			note:   "every",
			query:  `data.test.p = x`,
			module: "package test\np if { every k in input.ks { $\"{k}\" } }",
			exp:    []string{`every __local0__1, __local1__1 in input.ks { $"{__local1__1}" }; x = true`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.note, func(t *testing.T) {
			t.Parallel()
			opts := []func(*Rego){Query(tc.query)}
			if tc.module != "" {
				opts = append(opts, Module("test.rego", tc.module))
			}
			pq, err := New(opts...).Partial(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			assertPartialBodies(t, pq.Queries, tc.exp)
			if tc.support != nil {
				if len(pq.Support) != len(tc.support) {
					t.Fatalf("support count: got %d want %d\n%v", len(pq.Support), len(tc.support), pq.Support)
				}
				for i, mod := range pq.Support {
					if mod.String() != tc.support[i] {
						t.Fatalf("support:\n got: %s\nwant: %s", mod.String(), tc.support[i])
					}
				}
			}
			for _, q := range pq.Queries {
				if strings.Contains(q.String(), "internal.template_string") {
					t.Fatalf("query still exposes internal.template_string: %s", q)
				}
			}
			for _, mod := range pq.Support {
				if strings.Contains(mod.String(), "internal.template_string") {
					t.Fatalf("support still exposes internal.template_string: %s", mod)
				}
			}
		})
	}
}

func TestPartialResultTemplateStringReused(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	pr, err := New(
		Query(`data.test.p`),
		Module("test.rego", "package test\np := $\"hello {input.name}\" if input.ok"),
	).PartialResult(ctx)
	if err != nil {
		t.Fatal(err)
	}

	rs, err := pr.Rego(Input(map[string]any{"ok": true, "name": "world"})).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustResultString(t, rs); !strings.Contains(got, "hello world") {
		t.Fatalf("partial result eval = %s", got)
	}

	pq, err := pr.Rego().Partial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, q := range pq.Queries {
		if strings.Contains(q.String(), "internal.template_string") {
			t.Fatalf("reused partial result leaked internal.template_string: %s", q)
		}
		if strings.Contains(q.String(), `$"hello {input.name}"`) {
			found = true
		}
	}
	for _, mod := range pq.Support {
		if strings.Contains(mod.String(), "internal.template_string") {
			t.Fatalf("reused partial result support leaked internal.template_string: %s", mod)
		}
		if strings.Contains(mod.String(), `$"hello {input.name}"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("reused partial evaluation did not preserve the template string\nqueries: %v\nsupport: %v", pq.Queries, pq.Support)
	}
}

func assertPartialBodies(t *testing.T, got []ast.Body, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("query count: got %d (%v) want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Fatalf("query %d:\n got: %s\nwant: %s", i, got[i].String(), want[i])
		}
	}
}

func mustResultString(t *testing.T, rs ResultSet) string {
	t.Helper()
	if len(rs) == 0 || len(rs[0].Expressions) == 0 {
		t.Fatalf("empty result: %#v", rs)
	}
	return rs[0].Expressions[0].String()
}
