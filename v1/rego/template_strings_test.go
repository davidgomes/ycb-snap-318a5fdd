// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import (
	"strings"
	"testing"

	"github.com/open-policy-agent/opa/v1/ast"
)

const templateStringsModule = `package p

x := $"hello {input.name}!"

y := $"a {input.a} b {$"n {input.b}"} c {1+2}"

z := $"v {count(input.arr)}" if input.q

g(a) := $"g {a} {input.k}"

h := g(input.z) if input.t

allow if h == "x"
`

func TestPartialReconstructsTemplateStrings(t *testing.T) {
	tests := []struct {
		note     string
		query    string
		unknowns []string
		inlining []string
		queries  []string
		support  []string
	}{
		{
			note:    "residual interpolation",
			query:   "data.p.x = a",
			queries: []string{`a = $"hello {input.name}!"`},
		},
		{
			note:    "nested template string",
			query:   "data.p.y = b",
			queries: []string{`b = $"a {input.a} b {$"n {input.b}"} c {3}"`},
		},
		{
			note:    "call inlined from intermediate bindings",
			query:   "data.p.z = c",
			queries: []string{"input.q\nc = $\"v {count(input.arr)}\""},
		},
		{
			note:     "support module",
			query:    "data.p.allow",
			inlining: []string{"data.p.h"},
			queries:  []string{`data.partial.p.h = "x"`},
			support:  []string{`$"g {input.z} {input.k}"`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.note, func(t *testing.T) {
			pq, err := New(
				Query(tc.query),
				Module("test.rego", templateStringsModule),
				Unknowns([]string{"input"}),
				DisableInlining(tc.inlining),
			).Partial(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			if len(pq.Queries) != len(tc.queries) {
				t.Fatalf("expected %d queries, got %v", len(tc.queries), pq.Queries)
			}
			for i, q := range pq.Queries {
				if exp := strings.ReplaceAll(tc.queries[i], "\n", "; "); q.String() != exp {
					t.Errorf("expected query %v, got %v", exp, q)
				}
			}

			var sb strings.Builder
			for _, m := range pq.Support {
				sb.WriteString(m.String())
			}
			support := sb.String()
			if strings.Contains(support, ast.InternalTemplateString.Name) {
				t.Errorf("unexpected internal built-in in support: %s", support)
			}
			for _, s := range tc.support {
				if !strings.Contains(support, s) {
					t.Errorf("expected support to contain %s, got %s", s, support)
				}
			}
		})
	}
}

func TestPartialResultReusedForPartialReconstructsTemplateStrings(t *testing.T) {
	ctx := t.Context()
	pr, err := New(
		Query("data.p.y"),
		Module("test.rego", templateStringsModule),
		Unknowns([]string{"input"}),
	).PartialResult(ctx)
	if err != nil {
		t.Fatal(err)
	}

	pq, err := pr.Rego(Unknowns([]string{"input"})).Partial(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(pq.Queries) != 1 {
		t.Fatalf("expected 1 query, got %v", pq.Queries)
	}
	if q := pq.Queries[0].String(); !strings.Contains(q, `$"a {input.a} b {$"n {input.b}"} c {3}"`) {
		t.Errorf("expected reconstructed template string, got %v", q)
	}
	if strings.Contains(pq.Queries[0].String(), ast.InternalTemplateString.Name) {
		t.Errorf("unexpected internal built-in in %v", pq.Queries[0])
	}
}
