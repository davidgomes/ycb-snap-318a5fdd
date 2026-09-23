// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import (
	"fmt"
	"strings"
	"testing"

	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/format"
)

const templateStringsModule = `package test

greeting := $"hello {input.name}"

summary := $"{input.name} has {count(input.items)} items" if input.active

nested := $"outer {$"inner {input.name}"}"

k := 1

known := $"{k} {data.test.missing} {input.name}"

f(x) := $"f {x}"

called := f(input.name)

msgs contains $"item {x}" if some x in input.items

modified := $"{count(input.items) with input.items as [1, 2]}"

every_check if every v in input.items { $"{v}" == "1" }

names := [$"{u.name}!" | some u in input.users]
`

func TestPartialTemplateStrings(t *testing.T) {
	tests := []struct {
		note            string
		query           string
		disableInlining []string
		expQueries      []string
		expSupport      []string
	}{
		{
			note:       "residual interpolation",
			query:      "x = data.test.greeting",
			expQueries: []string{`x = $"hello {input.name}"`},
		},
		{
			note:       "intermediate bindings",
			query:      "x = data.test.summary",
			expQueries: []string{`input.active; x = $"{input.name} has {count(input.items)} items"`},
		},
		{
			note:       "nested template strings",
			query:      "x = data.test.nested",
			expQueries: []string{`x = $"outer {$"inner {input.name}"}"`},
		},
		{
			note:       "known and undefined values",
			query:      "x = data.test.known",
			expQueries: []string{`x = $"{1} <undefined> {input.name}"`},
		},
		{
			note:       "inlined function argument must be defined",
			query:      "x = data.test.called",
			expQueries: []string{`_ = input.name; x = $"f {input.name}"`},
		},
		{
			note:       "with modifier",
			query:      "x = data.test.modified",
			expQueries: []string{`x = $"{count(input.items) with input.items as [1, 2]}"`},
		},
		{
			note:       "every body",
			query:      "data.test.every_check",
			expQueries: []string{`every __local4__1, __local5__1 in input.items { $"{__local5__1}" = "1" }`},
		},
		{
			note:       "comprehension body",
			query:      "x = data.test.names",
			expQueries: []string{`x = [__local31__1 | __local8__1 = input.users[__local7__1]; __local31__1 = $"{__local8__1.name}!"]`},
		},
		{
			note:       "query template string",
			query:      `x = $"{input.a}-{input.b}"`,
			expQueries: []string{`x = $"{input.a}-{input.b}"`},
		},
		{
			note:       "support module",
			query:      "x = data.test.msgs",
			expQueries: []string{`data.partial.test.msgs = x`},
			expSupport: []string{`package partial.test

msgs contains __local26__1 if {
	_ = input.items[__local2__1]
	__local26__1 = $"item {input.items[__local2__1]}"
}`},
		},
		{
			note:            "support module with inlining disabled",
			query:           "x = data.test.greeting; y = data.test.called",
			disableInlining: []string{"data.test.greeting", "data.test.f"},
			expQueries:      []string{`data.partial.test.greeting = x; data.partial.test.f(input.name, y)`},
			expSupport: []string{`package partial.test

greeting = __local18__1 if __local18__1 = $"hello {input.name}"

f(__local0__3) = __local24__3 if __local24__3 = $"f {__local0__3}"`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.note, func(t *testing.T) {
			pq, err := New(
				Query(tc.query),
				Module("test.rego", templateStringsModule),
				DisableInlining(tc.disableInlining),
			).Partial(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			if len(pq.Queries) != len(tc.expQueries) {
				t.Fatalf("expected %d queries but got %d: %v", len(tc.expQueries), len(pq.Queries), pq.Queries)
			}
			for i, exp := range tc.expQueries {
				if exp, act := ast.MustParseBody(exp).String(), pq.Queries[i].String(); exp != act {
					t.Errorf("expected query:\n\n%s\n\ngot:\n\n%s", exp, act)
				}
			}

			if len(pq.Support) != len(tc.expSupport) {
				t.Fatalf("expected %d support modules but got %d: %v", len(tc.expSupport), len(pq.Support), pq.Support)
			}
			for i, exp := range tc.expSupport {
				if exp, act := ast.MustParseModule(exp).String(), pq.Support[i].String(); exp != act {
					t.Errorf("expected support module:\n\n%s\n\ngot:\n\n%s", exp, act)
				}
			}
		})
	}
}

// TestPartialTemplateStringsEval verifies that the reconstructed output, once
// formatted and parsed again, evaluates like the original policy.
func TestPartialTemplateStringsEval(t *testing.T) {
	inputs := []string{
		`{}`,
		`{"name": "alice", "active": true, "items": ["1", "b"], "users": [{"name": "bob"}, {}]}`,
		`{"name": 7, "active": false, "items": ["1"], "users": []}`,
	}
	rules := []string{"greeting", "summary", "nested", "known", "called", "msgs", "modified", "every_check", "names"}

	for _, rule := range rules {
		for _, input := range inputs {
			t.Run(fmt.Sprintf("%s/%s", rule, input), func(t *testing.T) {
				ctx := t.Context()
				query := "x = data.test." + rule
				in := ast.MustParseTerm(input).Value
				exp := templateStringsResults(t, New(Query(query), Module("test.rego", templateStringsModule), ParsedInput(in)))

				pq, err := New(Query(query), Module("test.rego", templateStringsModule)).Partial(ctx)
				if err != nil {
					t.Fatal(err)
				}
				assertNoTemplateStringCalls(t, pq)

				opts := []func(*Rego){ParsedInput(in)}
				for i, mod := range pq.Support {
					opts = append(opts, Module(fmt.Sprintf("support%d.rego", i), string(format.MustAst(mod))))
				}
				act := map[string]bool{}
				for _, body := range pq.Queries {
					for k := range templateStringsResults(t, New(append(opts, Query(string(format.MustAst(body))))...)) {
						act[k] = true
					}
				}
				if fmt.Sprint(exp) != fmt.Sprint(act) {
					t.Errorf("expected %v but got %v from partial queries %v and support %v", exp, act, pq.Queries, pq.Support)
				}

				ref := "data.test." + rule
				pr, err := New(Query(ref), Module("test.rego", templateStringsModule)).PartialResult(ctx)
				if err != nil {
					t.Fatal(err)
				}
				exp = templateStringsValues(t, New(Query(ref), Module("test.rego", templateStringsModule), ParsedInput(in)))
				act = templateStringsValues(t, pr.Rego(ParsedInput(in)))
				if fmt.Sprint(exp) != fmt.Sprint(act) {
					t.Errorf("expected %v but got %v from partial result", exp, act)
				}
			})
		}
	}
}

func TestPartialResultTemplateStrings(t *testing.T) {
	ctx := t.Context()

	pr, err := New(
		Query("data.test.p"),
		Module("test.rego", `package test

p := $"{input.greeting}, {input.name}!" if input.enabled`),
	).PartialResult(ctx)
	if err != nil {
		t.Fatal(err)
	}

	pq, err := pr.Rego(
		Unknowns([]string{"input.name"}),
		Input(map[string]any{"greeting": "hi", "enabled": true}),
	).Partial(ctx)
	if err != nil {
		t.Fatal(err)
	}

	exp := ast.MustParseBody(`$"hi, {input.name}!"`).String()
	if len(pq.Queries) != 1 || pq.Queries[0].String() != exp {
		t.Fatalf("expected query %s but got: %v", exp, pq.Queries)
	}

	rs, err := pr.Rego(Input(map[string]any{"greeting": "hi", "name": "alice", "enabled": true})).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Expressions[0].Value != "hi, alice!" {
		t.Fatalf("unexpected result: %v", rs)
	}
}

func TestReconstructTemplateStringsUnrepresentable(t *testing.T) {
	tests := []struct {
		note  string
		query string
		exp   string
	}{
		{
			note:  "multiple outputs",
			query: `internal.template_string(["a", {1, 2}], x)`,
		},
		{
			note:  "definedness can't be required outside of negation",
			query: `not internal.template_string(["a", {input.x}], "a1")`,
		},
		{
			note:  "condition in interpolated expression",
			query: `__local0__ = {__local1__ | input.z; __local1__ = input.x}; internal.template_string([__local0__], x)`,
		},
		{
			note:  "var local to interpolated expression",
			query: `__local0__ = {__local1__ | __local1__ = input.xs[__local2__]}; internal.template_string([__local0__], x)`,
		},
		{
			note:  "negation",
			query: `__local0__ = {__local1__ | __local1__ = input.x}; not internal.template_string(["a", __local0__], "ab")`,
			exp:   `not "ab" = $"a{input.x}"`,
		},
		{
			note:  "binding used elsewhere",
			query: `__local0__ = {__local1__ | __local1__ = input.x}; internal.template_string([__local0__], x); y = __local0__`,
			exp:   `__local0__ = {__local1__ | __local1__ = input.x}; x = $"{input.x}"; y = __local0__`,
		},
	}

	c := ast.NewCompiler()
	for _, tc := range tests {
		t.Run(tc.note, func(t *testing.T) {
			query := ast.MustParseBody(tc.query)
			queries, _ := reconstructTemplateStrings(c, []ast.Body{query}, nil)

			exp := tc.query
			if tc.exp != "" {
				exp = tc.exp
			}
			if exp, act := ast.MustParseBody(exp).String(), queries[0].String(); exp != act {
				t.Errorf("expected:\n\n%s\n\ngot:\n\n%s", exp, act)
			}
		})
	}
}

func templateStringsResults(t *testing.T, r *Rego) map[string]bool {
	t.Helper()
	rs, err := r.Eval(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, result := range rs {
		out[fmt.Sprint(result.Bindings)] = true
	}
	return out
}

func templateStringsValues(t *testing.T, r *Rego) map[string]bool {
	t.Helper()
	rs, err := r.Eval(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, result := range rs {
		out[fmt.Sprint(result.Expressions[0].Value)] = true
	}
	return out
}

func assertNoTemplateStringCalls(t *testing.T, pq *PartialQueries) {
	t.Helper()
	for _, body := range pq.Queries {
		if strings.Contains(body.String(), ast.InternalTemplateString.Name) {
			t.Errorf("unexpected %s in query: %v", ast.InternalTemplateString.Name, body)
		}
	}
	for _, mod := range pq.Support {
		if strings.Contains(mod.String(), ast.InternalTemplateString.Name) {
			t.Errorf("unexpected %s in support module: %v", ast.InternalTemplateString.Name, mod)
		}
	}
}
