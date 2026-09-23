// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

//go:build profile

package rego

import (
	"context"
	"reflect"
	"testing"
)

func TestRuleProfileEval(t *testing.T) {
	ctx := context.Background()
	module := `package authz

p contains "a" if {
	true
}

p contains "b" if {
	false
}

allow if {
	data.authz.p
}
`
	r := New(
		Query("data.authz.allow"),
		Module("authz.rego", module),
		EnableRuleProfile(true),
		Input(map[string]any{}),
	)
	rs, err := r.Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 {
		t.Fatalf("results: %d", len(rs))
	}
	prof := rs[0].Profile
	if prof == nil {
		t.Fatal("expected profile")
	}

	allow := prof.Stat("data.authz.allow")
	if allow == nil || allow.Evals != 1 || allow.Successes != 1 {
		t.Fatalf("allow: %+v", allow)
	}
	// Two definitions of p are entered. One succeeds and one fails, so the shared
	// path records both and is not a fully failed rule.
	partial := prof.Stat("data.authz.p")
	if partial == nil || partial.Evals != 2 || partial.Successes != 1 {
		t.Fatalf("partial: %+v profile=%s", partial, prof)
	}
	if prof.FailedRules() != nil {
		t.Fatalf("FailedRules: %v", prof.FailedRules())
	}
	if got := prof.SucceededRules(); !reflect.DeepEqual(got, []string{"data.authz.allow", "data.authz.p"}) {
		t.Fatalf("SucceededRules: %v", got)
	}
	if got := prof.Packages(); !reflect.DeepEqual(got, []string{"data.authz"}) {
		t.Fatalf("Packages: %v", got)
	}
}

func TestRuleProfileRecordsFailedRule(t *testing.T) {
	ctx := context.Background()
	module := `package authz

allow if {
	not deny
}

deny if {
	false
}
`
	rs, err := New(
		Query("data.authz.allow"),
		Module("authz.rego", module),
		EnableRuleProfile(true),
	).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prof := rs[0].Profile
	deny := prof.Stat("data.authz.deny")
	if deny == nil || deny.Evals == 0 || deny.Successes != 0 {
		t.Fatalf("deny: %+v\n%s", deny, prof)
	}
	if got := prof.FailedRules(); !reflect.DeepEqual(got, []string{"data.authz.deny"}) {
		t.Fatalf("FailedRules: %v", got)
	}
}

func TestRuleProfileDisabledByDefault(t *testing.T) {
	ctx := context.Background()
	rs, err := New(
		Query("data.authz.allow"),
		Module("authz.rego", "package authz\n\nallow if true\n"),
	).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Profile != nil {
		t.Fatalf("profile should be nil when disabled: %+v", rs)
	}
}

func TestRuleProfileEvalOptionOverridesConstruction(t *testing.T) {
	ctx := context.Background()
	r := New(
		Query("data.authz.allow"),
		Module("authz.rego", "package authz\n\nallow if true\n"),
		EnableRuleProfile(true),
	)
	pq, err := r.PrepareForEval(ctx)
	if err != nil {
		t.Fatal(err)
	}

	off, err := pq.Eval(ctx, EvalRuleProfile(false))
	if err != nil {
		t.Fatal(err)
	}
	if len(off) != 1 || off[0].Profile != nil {
		t.Fatal("EvalRuleProfile(false) should disable profiling")
	}

	on, err := pq.Eval(ctx, EvalRuleProfile(true))
	if err != nil {
		t.Fatal(err)
	}
	if len(on) != 1 || on[0].Profile == nil || on[0].Profile.Stat("data.authz.allow") == nil {
		t.Fatalf("expected profile, got %+v", on)
	}

	inherited, err := pq.Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(inherited) != 1 || inherited[0].Profile == nil {
		t.Fatal("prepared eval should inherit EnableRuleProfile")
	}
}

func TestRuleProfileMultipleDefinitionsAndCalls(t *testing.T) {
	ctx := context.Background()
	module := `package authz

f(_) if {
	true
}

allow if {
	f(1)
	f(2)
}
`
	rs, err := New(
		Query("data.authz.allow"),
		Module("authz.rego", module),
		EnableRuleProfile(true),
	).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prof := rs[0].Profile
	f := prof.Stat("data.authz.f")
	if f == nil || f.Evals < 2 || f.Successes < 2 {
		t.Fatalf("function rule: %+v\n%s", f, prof)
	}
}
