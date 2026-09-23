// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import (
	"strings"
	"testing"

	"github.com/open-policy-agent/opa/v1/format"
)

const templateStringsModule = `package test

p := $"hello {input.x}!"

q if {
	y := input.y
	s := $"a {y} b {input.z} c {data.test.r}"
	s == "foo"
}

r := 1

n := $"outer {$"inner {input.a}"}"

f := $"{[input.b, 1]} {input.c.d}"

g(a) := $"g {a}"

h := $"{g(input.m)}" if input.t == 1

w contains $"{i}-{input.v[i]}" if some i in [1, 2]
`

func formatPartialQueries(t *testing.T, pq *PartialQueries) string {
	t.Helper()

	var sb strings.Builder
	for _, q := range pq.Queries {
		bs, err := format.AstWithOpts(q, format.Opts{IgnoreLocations: true})
		if err != nil {
			t.Fatal(err)
		}
		sb.Write(bs)
	}
	for _, m := range pq.Support {
		bs, err := format.AstWithOpts(m, format.Opts{IgnoreLocations: true, RegoVersion: m.RegoVersion()})
		if err != nil {
			t.Fatal(err)
		}
		sb.WriteString("\n")
		sb.Write(bs)
	}
	return strings.TrimSpace(sb.String())
}

func TestPartialReconstructsTemplateStrings(t *testing.T) {
	tests := []struct {
		note            string
		query           string
		disableInlining []string
		exp             string
	}{
		{
			note:  "residual interpolation",
			query: "data.test.p = x",
			exp:   `x = $"hello {input.x}!"`,
		},
		{
			note:  "residual set term bound to var",
			query: "data.test.q",
			exp: `__ts0__ = input.y
"foo" = $"a {__ts0__} b {input.z} c {1}"`,
		},
		{
			note:  "nested",
			query: "data.test.n = x",
			exp:   `x = $"outer {$"inner {input.a}"}"`,
		},
		{
			note:  "inlined intermediate bindings",
			query: "data.test.f = x",
			exp:   `x = $"{[input.b, 1]} {input.c.d}"`,
		},
		{
			note:  "function call",
			query: "data.test.h = x",
			exp: `input.t = 1
x = $"{data.test.g(input.m)}"`,
		},
		{
			note:            "support modules",
			query:           "data.test.n = x; data.test.w = y",
			disableInlining: []string{"data.test.n", "data.test.w"},
			exp: `data.partial.test.n = x
data.partial.test.w = y

package partial.test

n := __local17__1 if __local17__1 = $"outer {$"inner {input.a}"}"

w contains __local22__2 if __local22__2 = $"{1}-{input.v[1]}"

w contains __local22__2 if __local22__2 = $"{2}-{input.v[2]}"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.note, func(t *testing.T) {
			r := New(
				Query(tc.query),
				Module("test.rego", templateStringsModule),
				DisableInlining(tc.disableInlining),
			)

			pq, err := r.Partial(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			if act := formatPartialQueries(t, pq); act != tc.exp {
				t.Fatalf("expected:\n\n%s\n\ngot:\n\n%s", tc.exp, act)
			}
		})
	}
}

func TestPartialResultReconstructsTemplateStrings(t *testing.T) {
	r := New(
		Query("data.test.n"),
		Module("test.rego", templateStringsModule),
	)

	pr, err := r.PartialResult(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	pq, err := pr.Rego(Unknowns([]string{"input"})).Partial(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	exp := `$"outer {$"inner {input.a}"}"`
	if act := formatPartialQueries(t, pq); act != exp {
		t.Fatalf("expected:\n\n%s\n\ngot:\n\n%s", exp, act)
	}

	pr, err = New(
		Query("data.test.f"),
		Module("test.rego", templateStringsModule),
	).PartialResult(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	assertEval(t, pr.Rego(Input(map[string]any{"c": map[string]any{"d": 1}})), `[["<undefined> 1"]]`)
	assertEval(t, pr.Rego(Input(map[string]any{"b": "B", "c": map[string]any{"d": 1}})), `[["[\"B\", 1] 1"]]`)
}
