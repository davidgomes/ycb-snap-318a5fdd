// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/format"
	"github.com/open-policy-agent/opa/v1/util"
)

func TestPartialTemplateStrings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		note            string
		module          string
		query           string
		disableInlining []string
		queries         []string
		support         []string
		inputs          []string
	}{
		{
			note:    "interpolated input",
			module:  `p := $"hello {input.x}!"`,
			query:   "x = data.test.p",
			queries: []string{`$"hello {input.x}!" = x`},
			inputs:  []string{`{"x": "world"}`, `{"x": 1}`, `{}`},
		},
		{
			note: "known values folded into string parts",
			module: `q := 7
p := $"n={input.x} m={q} s={"{s}"}"`,
			query:   "x = data.test.p",
			queries: []string{`$"n={input.x} m=7 s=\{s}" = x`},
			inputs:  []string{`{"x": [1]}`, `{}`},
		},
		{
			note:    "interpolated call",
			module:  `p := $"{count(input.arr)} items"`,
			query:   "x = data.test.p",
			queries: []string{`$"{count(input.arr)} items" = x`},
			inputs:  []string{`{"arr": [1, 2]}`, `{"arr": 1}`, `{}`},
		},
		{
			note:    "nested template-strings",
			module:  `p := $"outer {$"inner {input.z}"} {input.y}"`,
			query:   "x = data.test.p",
			queries: []string{`$"outer {$"inner {input.z}"} {input.y}" = x`},
			inputs:  []string{`{"z": "z", "y": "y"}`, `{}`},
		},
		{
			note:    "interpolated var bound to residual value",
			module:  `p if { y := input.y; $"v: {y}" == "v: 1" }`,
			query:   "data.test.p = x",
			queries: []string{`__localts0__ = input.y; $"v: {__localts0__}" = "v: 1"; x = true`},
			inputs:  []string{`{"y": 1}`, `{"y": 2}`, `{}`},
		},
		{
			note:    "interpolated var bound to residual value, not compared",
			module:  `p := $"v: {y}" if y := input.y`,
			query:   "x = data.test.p",
			queries: []string{`__localts0__ = input.y; $"v: {__localts0__}" = x`},
			inputs:  []string{`{"y": 1}`, `{}`},
		},
		{
			note:    "negated",
			module:  `p if not $"a{input.x}" == "ab"`,
			query:   "data.test.p = x",
			queries: []string{`not $"a{input.x}" = "ab"; x = true`},
			inputs:  []string{`{"x": "b"}`, `{"x": "c"}`, `{}`},
		},
		{
			note:    "with modifier",
			module:  `p := x if x := $"a{input.x}" with input.x as input.y`,
			query:   "x = data.test.p",
			queries: []string{`x = $"a{input.x}" with input.x as input.y`},
			inputs:  []string{`{"x": "x", "y": "y"}`, `{"x": "x"}`, `{}`},
		},
		{
			note:    "comprehension head",
			module:  `p := [$"#{i}={v}" | some i, v in input.vs]`,
			query:   "x = data.test.p",
			queries: []string{`x = [$"#{__local0__1}={__local1__1}" | __local1__1 = input.vs[__local0__1]]`},
			inputs:  []string{`{"vs": ["a", "b"]}`, `{}`},
		},
		{
			note:    "every",
			module:  `p if every v in input.vs { $"<{v}>" != "<bad>" }`,
			query:   "data.test.p = x",
			queries: []string{`every __local0__1, __local1__1 in input.vs { neq($"<{__local1__1}>", "<bad>") }; x = true`},
			inputs:  []string{`{"vs": ["a", "b"]}`, `{"vs": ["bad"]}`, `{}`},
		},
		{
			note:            "support module",
			module:          `p := $"hello {input.x}!"`,
			query:           "x = data.test.p",
			disableInlining: []string{"data.test.p"},
			queries:         []string{`data.partial.test.p = x`},
			support: []string{`package partial.test

p := $"hello {input.x}!"
`},
			inputs: []string{`{"x": "world"}`, `{}`},
		},
		{
			note:            "support module, nested template-strings and calls",
			module:          `p := $"outer {$"inner {count(input.arr)}"}"`,
			query:           "x = data.test.p",
			disableInlining: []string{"data.test.p"},
			queries:         []string{`data.partial.test.p = x`},
			support: []string{`package partial.test

p := $"outer {$"inner {count(input.arr)}"}"
`},
			inputs: []string{`{"arr": [1]}`, `{"arr": 1}`, `{}`},
		},
		{
			note:            "support module, ref head",
			module:          `o[$"k{input.k}"] := $"v{input.v}" if input.ok`,
			query:           "x = data.test.o",
			disableInlining: []string{"data.test.o"},
			queries:         []string{`data.partial.test.o = x`},
			support: []string{`package partial.test

o[$"k{input.k}"] := $"v{input.v}" if {
	input.ok
}
`},
			inputs: []string{`{"ok": true, "k": 1, "v": 2}`, `{"ok": true}`, `{}`},
		},
		{
			note:            "support module, multi-value rule",
			module:          `s contains $"item {x}" if some x in input.xs`,
			query:           "x = data.test.s",
			disableInlining: []string{"data.test.s"},
			queries:         []string{`data.partial.test.s = x`},
			support: []string{`package partial.test

s contains $"item {__localts0__}" if __localts0__ = input.xs[__local1__1]
`},
			inputs: []string{`{"xs": [1, "a"]}`, `{}`},
		},
		{
			note:            "support module, function",
			module:          `f(x) := $"f {x} {input.q}"`,
			query:           "x = data.test.f(input.w)",
			disableInlining: []string{"data.test.f"},
			queries:         []string{`data.partial.test.f(input.w, x)`},
			support: []string{`package partial.test

f(__local0__1) := $"f {__local0__1} {input.q}"
`},
			inputs: []string{`{"w": 1, "q": 2}`, `{"w": 1}`, `{}`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.note, func(t *testing.T) {
			t.Parallel()

			module := "package test\n\n" + tc.module
			pq, err := New(
				Query(tc.query),
				Module("test.rego", module),
				DisableInlining(tc.disableInlining),
			).Partial(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			queries := make([]string, len(pq.Queries))
			for i, q := range pq.Queries {
				queries[i] = q.String()
			}
			if !slices.Equal(queries, tc.queries) {
				t.Errorf("expected queries:\n\n%v\n\ngot:\n\n%v", strings.Join(tc.queries, "\n"), strings.Join(queries, "\n"))
			}

			support := make([]string, len(pq.Support))
			for i, mod := range pq.Support {
				support[i] = string(format.MustAst(mod))
			}
			if !slices.Equal(support, tc.support) {
				t.Errorf("expected support:\n\n%v\n\ngot:\n\n%v", strings.Join(tc.support, "\n"), strings.Join(support, "\n"))
			}

			assertNoLoweredTemplateStrings(t, pq)

			for _, input := range tc.inputs {
				exp := evalValues(t, []string{tc.query}, []string{module}, input)
				act := evalValues(t, queries, append(support, module), input)
				if !slices.Equal(exp, act) {
					t.Errorf("input %v: expected %v from residual, got %v", input, exp, act)
				}
			}
		})
	}
}

func TestPartialResultTemplateStrings(t *testing.T) {
	t.Parallel()

	module := `package test

p := $"outer {$"inner {input.z}"} {count(input.arr)}"`

	pr, err := New(Query("data.test.p"), Module("test.rego", module)).PartialResult(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	rs, err := pr.Rego(Input(map[string]any{"z": "z", "arr": []int{1, 2}})).Eval(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if exp, act := "outer inner z 2", rs[0].Expressions[0].Value; exp != act {
		t.Errorf("expected %v, got %v", exp, act)
	}

	pq, err := pr.Rego().Partial(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if exp, act := `$"outer {$"inner {input.z}"} {count(input.arr)}"`, pq.Queries[0].String(); len(pq.Queries) != 1 || exp != act {
		t.Errorf("expected query %v, got %v", exp, pq.Queries)
	}

	assertNoLoweredTemplateStrings(t, pq)
}

func TestReconstructTemplateStringsUnrepresentable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		note     string
		query    string
		expected string
	}{
		{
			note:     "conditional comprehension",
			query:    `internal.template_string(["a", {__local0__ | input.b; __local0__ = input.a}], x)`,
			expected: `internal.template_string(["a", {__local0__ | input.b; __local0__ = input.a}], x)`,
		},
		{
			note:     "relation call",
			query:    `internal.template_string(["a", {__local0__ | walk(input, __local1__); __local0__ = __local1__}], x)`,
			expected: `internal.template_string(["a", {__local0__ | walk(input, __local1__); __local0__ = __local1__}], x)`,
		},
		{
			note:     "comprehension local var",
			query:    `internal.template_string(["a", {__local0__ | __local0__ = input.a[__local1__]}], x)`,
			expected: `internal.template_string(["a", {__local0__ | __local0__ = input.a[__local1__]}], x)`,
		},
		{
			note:     "binding shared with other exprs",
			query:    `__local1__ = {__local0__ | __local0__ = input.a}; internal.template_string([__local1__], x); y = __local1__`,
			expected: `__local1__ = {__local0__ | __local0__ = input.a}; internal.template_string([__local1__], x); y = __local1__`,
		},
		{
			note:     "multiple values",
			query:    `internal.template_string(["a", {1, 2}], x)`,
			expected: `internal.template_string(["a", {1, 2}], x)`,
		},
		{
			note:     "hoisting out of negation",
			query:    `not internal.template_string(["a", {input.a}], "ab")`,
			expected: `not internal.template_string(["a", {input.a}], "ab")`,
		},
		{
			note:     "undefined value",
			query:    `internal.template_string(["a", set()], x)`,
			expected: `"a<undefined>" = x`,
		},
		{
			note:     "binding under other with modifiers",
			query:    `__local1__ = {__local0__ | __local0__ = input.a}; internal.template_string([__local1__], x) with input.a as 1`,
			expected: `__local1__ = {__local0__ | __local0__ = input.a}; internal.template_string([__local1__], x) with input.a as 1`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.note, func(t *testing.T) {
			t.Parallel()

			pq := &PartialQueries{Queries: []ast.Body{ast.MustParseBody(tc.query)}}
			reconstructTemplateStrings(pq, New().functionArity(nil))

			if act := pq.Queries[0].String(); act != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, act)
			}
		})
	}
}

func assertNoLoweredTemplateStrings(t *testing.T, pq *PartialQueries) {
	t.Helper()

	for _, q := range pq.Queries {
		if containsTemplateStringCall(q) {
			t.Errorf("unexpected %v in query: %v", ast.InternalTemplateString.Name, q)
		}
	}
	for _, mod := range pq.Support {
		if containsTemplateStringCall(mod) {
			t.Errorf("unexpected %v in support module: %v", ast.InternalTemplateString.Name, mod)
		}
	}
}

// evalValues evaluates each query against the modules and returns the sorted
// values bound to x, or produced by the query's last expression if x is unbound.
func evalValues(t *testing.T, queries []string, modules []string, input string) []string {
	t.Helper()

	var values []string
	for _, q := range queries {
		opts := make([]func(*Rego), 0, 2+len(modules))
		opts = append(opts, Query(q), Input(util.MustUnmarshalJSON([]byte(input))))
		for i, m := range modules {
			opts = append(opts, Module(fmt.Sprintf("module%d.rego", i), m))
		}

		rs, err := New(opts...).Eval(t.Context())
		if err != nil {
			t.Fatalf("query %v: %v", q, err)
		}

		for _, r := range rs {
			if x, ok := r.Bindings["x"]; ok {
				values = append(values, fmt.Sprint(x))
			} else {
				values = append(values, fmt.Sprint(r.Expressions[len(r.Expressions)-1].Value))
			}
		}
	}

	slices.Sort(values)
	return values
}
