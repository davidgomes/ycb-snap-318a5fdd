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

const profileModule = `package authz

default allow := false

allow if startswith(input.user, "adm")

allow if startswith(input.user, "roo")

deny if input.user == "bob"

role := "admin" if input.user == "admin"
`

func TestRuleProfileEval(t *testing.T) {
	ctx := context.Background()

	r := New(
		Query("x = data.authz.allow; data.authz.deny"),
		Module("authz.rego", profileModule),
		Input(map[string]any{"user": "alice"}),
	)
	pq, err := r.PrepareForEval(ctx)
	if err != nil {
		t.Fatal(err)
	}

	rs, err := New(
		Query("data.authz.allow"),
		Module("authz.rego", profileModule),
		Input(map[string]any{"user": "admin"}),
	).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rs[0].Profile != nil {
		t.Fatalf("expected nil profile when disabled, got %v", rs[0].Profile)
	}

	rs, err = pq.Eval(ctx, EvalInput(map[string]any{"user": "bob"}), EvalRuleProfile(true))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 {
		t.Fatalf("expected one result, got %v", rs)
	}
	p := rs[0].Profile
	if p == nil {
		t.Fatal("expected profile")
	}

	// Both allow definitions are entered and fail, then the default succeeds.
	if s := p.Stat("data.authz.allow"); s == nil || s.Evals != 3 || s.Successes != 1 {
		t.Fatalf("unexpected allow stat: %v", s)
	}
	if s := p.Stat("data.authz.deny"); s == nil || s.Evals != 1 || s.Successes != 1 {
		t.Fatalf("unexpected deny stat: %v", s)
	}
	if p.ContainsRule("data.authz.role") {
		t.Fatal("role was never referenced")
	}
}

func TestRuleProfileEnableAtConstruction(t *testing.T) {
	ctx := context.Background()
	rs, err := New(
		Query("data.authz.allow"),
		Module("authz.rego", profileModule),
		Input(map[string]any{"user": "root"}),
		EnableRuleProfile(true),
	).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s := rs[0].Profile.Stat("data.authz.allow"); s == nil || s.Evals != 2 || s.Successes != 1 {
		t.Fatalf("unexpected allow stat: %v", s)
	}

	pq, err := New(
		Query("data.authz.allow"),
		Module("authz.rego", profileModule),
		EnableRuleProfile(true),
	).PrepareForEval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rs, err = pq.Eval(ctx, EvalInput(map[string]any{"user": "root"}), EvalRuleProfile(false))
	if err != nil {
		t.Fatal(err)
	}
	if rs[0].Profile != nil {
		t.Fatal("expected per-eval option to disable profiling")
	}
}

func testProfile() *EvalProfile {
	return &EvalProfile{Rules: map[string]*RuleStat{
		"data.authz.allow": {Evals: 4, Successes: 1},
		"data.authz.deny":  {Evals: 2, Successes: 0},
		"data.util.f":      {Evals: 1, Successes: 1},
	}}
}

func TestEvalProfileMethods(t *testing.T) {
	p := testProfile()

	if got := p.RulePaths(); !reflect.DeepEqual(got, []string{"data.authz.allow", "data.authz.deny", "data.util.f"}) {
		t.Fatal(got)
	}
	if got := p.SuccessRate("data.authz.allow"); got != 0.25 {
		t.Fatal(got)
	}
	if got := p.SuccessRate("missing"); got != 0 {
		t.Fatal(got)
	}
	if got := p.OverallSuccessRate(); got != 2.0/7.0 {
		t.Fatal(got)
	}
	if got := p.HotRules(2); !reflect.DeepEqual(got, []string{"data.authz.allow", "data.authz.deny"}) {
		t.Fatal(got)
	}
	if got := p.HotRules(10); got != nil {
		t.Fatal(got)
	}
	if got := p.FailedRules(); !reflect.DeepEqual(got, []string{"data.authz.deny"}) {
		t.Fatal(got)
	}
	if got := p.SucceededRules(); !reflect.DeepEqual(got, []string{"data.authz.allow", "data.util.f"}) {
		t.Fatal(got)
	}
	if got := p.Packages(); !reflect.DeepEqual(got, []string{"data.authz", "data.util"}) {
		t.Fatal(got)
	}

	f := p.FilterByPackage("data.authz")
	if got := f.RulePaths(); !reflect.DeepEqual(got, []string{"data.authz.allow", "data.authz.deny"}) {
		t.Fatal(got)
	}
	f.Rules["data.authz.allow"].Evals = 100
	if p.Rules["data.authz.allow"].Evals != 4 {
		t.Fatal("FilterByPackage must deep copy")
	}

	ps := p.PackageStats()
	if !reflect.DeepEqual(ps["data.authz"], &RuleStat{Evals: 6, Successes: 1}) {
		t.Fatal(ps["data.authz"])
	}

	m := p.Merge(testProfile())
	if !reflect.DeepEqual(m.Stat("data.util.f"), &RuleStat{Evals: 2, Successes: 2}) {
		t.Fatal(m)
	}
	if p.Stat("data.util.f").Evals != 1 {
		t.Fatal("Merge must not mutate inputs")
	}
	var nilP *EvalProfile
	if nilP.Merge(nil) != nil || nilP.Merge(p) != p || p.Merge(nil) != p {
		t.Fatal("unexpected nil merge behaviour")
	}

	if got := p.Summary(); got != "profile: 3 rules, 7 evals, 2 successes" {
		t.Fatal(got)
	}
	exp := "Profile:\n  data.authz.allow: evals=4 successes=1\n  data.authz.deny: evals=2 successes=0\n  data.util.f: evals=1 successes=1\n"
	if got := p.String(); got != exp {
		t.Fatal(got)
	}
	if !p.Equal(testProfile()) || p.Equal(m) || p.Equal(nil) || !nilP.Equal(nil) {
		t.Fatal("unexpected Equal result")
	}
	if got := p.Stat("data.authz.allow").String(); got != "evals=4 successes=1" {
		t.Fatal(got)
	}
}

func TestEvalProfileNilReceiver(t *testing.T) {
	var p *EvalProfile
	var s *RuleStat
	var d *ProfileDiff

	if p.Stat("x") != nil || p.RulePaths() != nil || p.SuccessRate("x") != 0 ||
		p.OverallSuccessRate() != 0 || p.HotRules(0) != nil || p.FailedRules() != nil ||
		p.SucceededRules() != nil || p.Packages() != nil || p.FilterByPackage("x") != nil ||
		p.PackageStats() != nil || p.ContainsRule("x") || p.Diff(testProfile()) != nil {
		t.Fatal("unexpected nil receiver result")
	}
	if p.Summary() != "profile: disabled" || p.String() != "<nil>" {
		t.Fatal("unexpected nil receiver string")
	}
	if s.SuccessRate() != 0 || s.String() != "<nil>" {
		t.Fatal("unexpected nil RuleStat result")
	}
	if d.HasChanges() {
		t.Fatal("nil diff has no changes")
	}
}

func TestEvalProfileDiff(t *testing.T) {
	a := testProfile()
	b := testProfile()

	d := a.Diff(b)
	if d.HasChanges() || d.Added != nil || d.Removed != nil || d.Changed != nil {
		t.Fatalf("expected empty diff, got %+v", d)
	}

	delete(b.Rules, "data.util.f")
	b.Rules["data.authz.allow"] = &RuleStat{Evals: 6, Successes: 3}
	b.Rules["data.new.r"] = &RuleStat{Evals: 1}

	d = a.Diff(b)
	if !d.HasChanges() {
		t.Fatal("expected changes")
	}
	if !reflect.DeepEqual(d.Added, map[string]*RuleStat{"data.new.r": {Evals: 1}}) {
		t.Fatal(d.Added)
	}
	if !reflect.DeepEqual(d.Removed, map[string]*RuleStat{"data.util.f": {Evals: 1, Successes: 1}}) {
		t.Fatal(d.Removed)
	}
	if !reflect.DeepEqual(d.Changed, map[string]*RuleStatDelta{"data.authz.allow": {EvalsDelta: 2, SuccessesDelta: 2}}) {
		t.Fatal(d.Changed)
	}
}
