// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import (
	"context"
	"strings"
	"testing"

	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/format"
)

func TestPartialReconstructsTemplateStrings(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	module := `package test
p := $"hello {input.name}"
q := $"user_{input.id}"
r := $"a {input.x} b {sprintf("%s", [input.y])} c"
s := $"nested {$"inner {input.z}"} end"
u contains $"set {input.a}"
v[x] := $"key {x}" if { x := input.k }
w if { $"hi {input.name}" with input as {"name": "x"} }
brace := $"keep \{brace {input.name}"
num := $"{42} {input.x}"
`

	cases := []struct {
		query string
		want  string
	}{
		{query: "data.test.p", want: `$"hello {input.name}"`},
		{query: "data.test.q", want: `$"user_{input.id}"`},
		{query: "data.test.r", want: `$"a {input.x} b {sprintf("%s", [input.y])} c"`},
		{query: "data.test.s", want: `$"nested {$"inner {input.z}"} end"`},
		{query: `data.test.p = $"hello {input.name}"`, want: `$"hello {input.name}" = $"hello {input.name}"`},
		{query: "data.test.num", want: `$"{42} {input.x}"`},
		{query: "data.test.brace", want: `$"keep \{brace {input.name}"`},
	}

	compiler := ast.MustCompileModules(map[string]string{"t.rego": module})
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			t.Parallel()
			pq, err := New(
				Query(tc.query),
				Compiler(compiler),
				Unknowns([]string{"input"}),
			).Partial(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(pq.Queries) != 1 {
				t.Fatalf("queries: %v", pq.Queries)
			}
			got := formatBody(t, pq.Queries[0])
			if got != tc.want {
				t.Fatalf("got %s\nwant %s", got, tc.want)
			}
			if strings.Contains(got, "internal.template_string") {
				t.Fatalf("residual still exposes internal.template_string: %s", got)
			}
		})
	}

	t.Run("support modules", func(t *testing.T) {
		t.Parallel()
		pq, err := New(
			Query("data.test.u"),
			Compiler(compiler),
			Unknowns([]string{"input"}),
		).Partial(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(pq.Support) != 1 {
			t.Fatalf("support: %d", len(pq.Support))
		}
		src := formatModule(t, pq.Support[0])
		if strings.Contains(src, "internal.template_string") {
			t.Fatalf("support leaked internal call:\n%s", src)
		}
		if !strings.Contains(src, `$"set {input.a}"`) {
			t.Fatalf("support missing template string:\n%s", src)
		}
	})

	t.Run("residual var in template", func(t *testing.T) {
		t.Parallel()
		pq, err := New(
			Query("data.test.v"),
			Compiler(compiler),
			Unknowns([]string{"input"}),
		).Partial(ctx)
		if err != nil {
			t.Fatal(err)
		}
		src := formatModule(t, pq.Support[0])
		if strings.Contains(src, "internal.template_string") {
			t.Fatalf("support leaked internal call:\n%s", src)
		}
		if !strings.Contains(src, `$"key {input.k}"`) && !strings.Contains(src, `$"key {__local`) {
			t.Fatalf("support missing key template:\n%s", src)
		}
	})

	t.Run("with modifier", func(t *testing.T) {
		t.Parallel()
		pq, err := New(
			Query("data.test.w"),
			Compiler(compiler),
			Unknowns([]string{"input"}),
		).Partial(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got := formatBody(t, pq.Queries[0])
		if strings.Contains(got, "internal.template_string") {
			t.Fatalf("with query leaked internal call: %s", got)
		}
		if !strings.Contains(got, `$"hi {input.name}"`) {
			t.Fatalf("with query: %s", got)
		}
	})

	t.Run("partial result reused", func(t *testing.T) {
		t.Parallel()
		pr, err := New(
			Query("data.test.p"),
			Compiler(compiler),
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
			t.Fatalf("queries: %v", pq.Queries)
		}
		got := formatBody(t, pq.Queries[0])
		if got != `$"hello {input.name}"` {
			t.Fatalf("reused partial result: %s", got)
		}
	})
}

func TestPartialTemplateStringSemantics(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	module := `package test
p := $"hello {input.name}"
r := $"a {input.x} b {sprintf("%s", [input.y])} c"
s := $"nested {$"inner {input.z}"} end"
`
	input := map[string]any{"name": "ada", "x": "X", "y": "Y", "z": "Z"}
	compiler := ast.MustCompileModules(map[string]string{"t.rego": module})

	for _, query := range []string{"data.test.p", "data.test.r", "data.test.s"} {
		t.Run(query, func(t *testing.T) {
			t.Parallel()
			full, err := New(Query(query), Compiler(compiler), Input(input)).Eval(ctx)
			if err != nil {
				t.Fatal(err)
			}
			pq, err := New(Query(query), Compiler(compiler), Unknowns([]string{"input"})).Partial(ctx)
			if err != nil {
				t.Fatal(err)
			}
			mods := map[string]string{}
			for i, mod := range pq.Support {
				bs, err := format.AstWithOpts(mod, format.Opts{RegoVersion: mod.RegoVersion()})
				if err != nil {
					t.Fatal(err)
				}
				mods[string(rune('a'+i))+".rego"] = string(bs)
			}
			bs, err := format.AstWithOpts(pq.Queries[0], format.Opts{})
			if err != nil {
				t.Fatal(err)
			}
			partial, err := New(Query(string(bs)), Module("support.rego", mods["a.rego"]), Input(input)).Eval(ctx)
			if err != nil {
				// No support module.
				partial, err = New(Query(string(bs)), Input(input)).Eval(ctx)
				if err != nil {
					t.Fatal(err)
				}
			}
			if full[0].Expressions[0].Value != partial[0].Expressions[0].Value {
				t.Fatalf("full %v partial %v query %s", full[0].Expressions[0].Value, partial[0].Expressions[0].Value, bs)
			}
		})
	}
}

func formatBody(t *testing.T, body ast.Body) string {
	t.Helper()
	bs, err := format.AstWithOpts(body, format.Opts{IgnoreLocations: true})
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(bs))
}

func formatModule(t *testing.T, mod *ast.Module) string {
	t.Helper()
	bs, err := format.AstWithOpts(mod, format.Opts{IgnoreLocations: true, RegoVersion: mod.RegoVersion()})
	if err != nil {
		t.Fatal(err)
	}
	return string(bs)
}
