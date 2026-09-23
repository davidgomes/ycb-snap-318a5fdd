//go:build profile

package rego

import (
	"context"
	"reflect"
	"testing"
)

const profileTestModule = `package authz

default allow := false

allow if { input.user == "admin" }
allow if { is_editor }

is_editor if { input.role == "editor" }

deny contains msg if { some x in input.items; x > 1; msg := x }

inc(x) := x + 1

v := inc(1)
`

func TestRuleProfileEval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		note  string
		input map[string]any
		exp   map[string]*RuleStat
	}{
		{
			note:  "failing definition and multi-valued rule",
			input: map[string]any{"user": "admin", "items": []any{0}},
			exp: map[string]*RuleStat{
				"data.authz.allow": {Evals: 2, Successes: 1},
				"data.authz.deny":  {Evals: 1, Successes: 0},
				"data.authz.inc":   {Evals: 1, Successes: 1},
				"data.authz.v":     {Evals: 1, Successes: 1},
			},
		},
		{
			note:  "default rule and success counted once per entry",
			input: map[string]any{"user": "bob", "items": []any{1, 2, 3}},
			exp: map[string]*RuleStat{
				"data.authz.allow": {Evals: 2, Successes: 1},
				"data.authz.deny":  {Evals: 1, Successes: 1},
				"data.authz.inc":   {Evals: 1, Successes: 1},
				"data.authz.v":     {Evals: 1, Successes: 1},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.note, func(t *testing.T) {
			t.Parallel()

			rs, err := New(
				Query("x = data.authz"),
				Module("authz.rego", profileTestModule),
				Input(tc.input),
				EnableRuleProfile(true),
			).Eval(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(rs) != 1 {
				t.Fatalf("expected 1 result, got %d", len(rs))
			}
			exp := &EvalProfile{Rules: tc.exp}
			if !rs[0].Profile.Equal(exp) {
				t.Fatalf("expected:\n%v\ngot:\n%v", exp, rs[0].Profile)
			}
		})
	}
}

func TestRuleProfileOptions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	newRego := func(opts ...func(*Rego)) *Rego {
		opts = append(opts, Query("data.authz.v"), Module("authz.rego", profileTestModule))
		return New(opts...)
	}

	rs, err := newRego().Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rs[0].Profile != nil {
		t.Fatalf("expected nil profile by default, got %v", rs[0].Profile)
	}

	pq, err := newRego().PrepareForEval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rs, err = pq.Eval(ctx, EvalRuleProfile(true))
	if err != nil {
		t.Fatal(err)
	}
	if !rs[0].Profile.ContainsRule("data.authz.v") {
		t.Fatalf("expected profile with data.authz.v, got %v", rs[0].Profile)
	}

	pq, err = newRego(EnableRuleProfile(true)).PrepareForEval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rs, err = pq.Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rs[0].Profile == nil {
		t.Fatal("expected profile when enabled at construction")
	}
	rs, err = pq.Eval(ctx, EvalRuleProfile(false))
	if err != nil {
		t.Fatal(err)
	}
	if rs[0].Profile != nil {
		t.Fatalf("expected nil profile when disabled per eval, got %v", rs[0].Profile)
	}
}

func TestRuleProfileRefHeadPaths(t *testing.T) {
	t.Parallel()

	module := `package x

a.b.c if true

p[k] := 1 if { some k in ["y", "z"] }
`
	rs, err := New(
		Query("data.x = v"),
		Module("x.rego", module),
		EnableRuleProfile(true),
	).Eval(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertStrings(t, "RulePaths", rs[0].Profile.RulePaths(), []string{"data.x.a.b.c", "data.x.p"})
}

func TestRuleProfileSharedAcrossResults(t *testing.T) {
	t.Parallel()

	rs, err := New(
		Query("data.authz.deny[x]"),
		Module("authz.rego", profileTestModule),
		Input(map[string]any{"items": []any{2, 3}}),
		EnableRuleProfile(true),
	).Eval(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 2 {
		t.Fatalf("expected 2 results, got %d", len(rs))
	}
	if rs[0].Profile == nil || rs[0].Profile != rs[1].Profile {
		t.Fatalf("expected all results to share one profile, got %p and %p", rs[0].Profile, rs[1].Profile)
	}
}

func TestEvalProfileNilReceiver(t *testing.T) {
	t.Parallel()

	var p *EvalProfile
	var s *RuleStat
	var d *ProfileDiff

	if p.Stat("data.a.b") != nil || p.RulePaths() != nil || p.HotRules(0) != nil ||
		p.FailedRules() != nil || p.SucceededRules() != nil || p.Packages() != nil ||
		p.FilterByPackage("data.a") != nil || p.PackageStats() != nil || p.Diff(&EvalProfile{}) != nil {
		t.Fatal("expected nil results from nil profile")
	}
	if p.SuccessRate("data.a.b") != 0 || p.OverallSuccessRate() != 0 || s.SuccessRate() != 0 {
		t.Fatal("expected zero rates from nil receivers")
	}
	if p.ContainsRule("data.a.b") || d.HasChanges() {
		t.Fatal("expected false from nil receivers")
	}
	if p.Summary() != "profile: disabled" || p.String() != "<nil>" || s.String() != "<nil>" {
		t.Fatal("unexpected string from nil receivers")
	}
	if !p.Equal(nil) || p.Equal(&EvalProfile{}) || (&EvalProfile{}).Equal(nil) {
		t.Fatal("unexpected nil equality")
	}
	if p.Merge(nil) != nil {
		t.Fatal("expected nil merge of two nil profiles")
	}
	other := &EvalProfile{}
	if p.Merge(other) != other || other.Merge(p) != other {
		t.Fatal("expected merge with nil to return the non-nil profile")
	}
}

func TestEvalProfileMethods(t *testing.T) {
	t.Parallel()

	p := &EvalProfile{Rules: map[string]*RuleStat{
		"data.authz.allow": {Evals: 4, Successes: 1},
		"data.authz.deny":  {Evals: 2, Successes: 0},
		"data.util.f":      {Evals: 2, Successes: 2},
	}}

	if got := p.Stat("data.authz.allow"); got == nil || got.Evals != 4 || got.String() != "evals=4 successes=1" {
		t.Fatalf("unexpected stat: %v", got)
	}
	if p.Stat("data.x.y") != nil || p.ContainsRule("data.x.y") || !p.ContainsRule("data.util.f") {
		t.Fatal("unexpected membership")
	}
	assertStrings(t, "RulePaths", p.RulePaths(), []string{"data.authz.allow", "data.authz.deny", "data.util.f"})
	assertStrings(t, "HotRules", p.HotRules(3), []string{"data.authz.allow"})
	assertStrings(t, "HotRules", p.HotRules(5), nil)
	assertStrings(t, "FailedRules", p.FailedRules(), []string{"data.authz.deny"})
	assertStrings(t, "SucceededRules", p.SucceededRules(), []string{"data.authz.allow", "data.util.f"})
	assertStrings(t, "Packages", p.Packages(), []string{"data.authz", "data.util"})
	assertStrings(t, "RulePaths", (&EvalProfile{}).RulePaths(), nil)

	if got := p.SuccessRate("data.authz.allow"); got != 0.25 {
		t.Fatalf("expected 0.25, got %v", got)
	}
	if got := p.SuccessRate("data.x.y"); got != 0 {
		t.Fatalf("expected 0 for untracked rule, got %v", got)
	}
	if got := p.OverallSuccessRate(); got != 0.375 {
		t.Fatalf("expected 0.375, got %v", got)
	}
	if got := (&RuleStat{}).SuccessRate(); got != 0 {
		t.Fatalf("expected 0 for zero evals, got %v", got)
	}

	filtered := p.FilterByPackage("data.authz")
	assertStrings(t, "FilterByPackage", filtered.RulePaths(), []string{"data.authz.allow", "data.authz.deny"})
	filtered.Rules["data.authz.allow"].Evals = 100
	if p.Rules["data.authz.allow"].Evals != 4 {
		t.Fatal("expected FilterByPackage to copy stats")
	}

	expPkgStats := map[string]*RuleStat{
		"data.authz": {Evals: 6, Successes: 1},
		"data.util":  {Evals: 2, Successes: 2},
	}
	if got := p.PackageStats(); !reflect.DeepEqual(got, expPkgStats) {
		t.Fatalf("expected %v, got %v", expPkgStats, got)
	}

	if got, exp := p.Summary(), "profile: 3 rules, 8 evals, 3 successes"; got != exp {
		t.Fatalf("expected %q, got %q", exp, got)
	}
	exp := "Profile:\n" +
		"  data.authz.allow: evals=4 successes=1\n" +
		"  data.authz.deny: evals=2 successes=0\n" +
		"  data.util.f: evals=2 successes=2\n"
	if got := p.String(); got != exp {
		t.Fatalf("expected %q, got %q", exp, got)
	}
}

func TestEvalProfileMerge(t *testing.T) {
	t.Parallel()

	a := &EvalProfile{Rules: map[string]*RuleStat{
		"data.a.p": {Evals: 1, Successes: 1},
		"data.a.q": {Evals: 2, Successes: 0},
	}}
	b := &EvalProfile{Rules: map[string]*RuleStat{
		"data.a.q": {Evals: 3, Successes: 1},
		"data.b.r": {Evals: 1, Successes: 0},
	}}

	merged := a.Merge(b)
	exp := &EvalProfile{Rules: map[string]*RuleStat{
		"data.a.p": {Evals: 1, Successes: 1},
		"data.a.q": {Evals: 5, Successes: 1},
		"data.b.r": {Evals: 1, Successes: 0},
	}}
	if !merged.Equal(exp) {
		t.Fatalf("expected:\n%v\ngot:\n%v", exp, merged)
	}
	if a.Rules["data.a.q"].Evals != 2 || b.Rules["data.a.q"].Evals != 3 {
		t.Fatal("expected Merge not to modify its inputs")
	}
}

func TestEvalProfileDiff(t *testing.T) {
	t.Parallel()

	before := &EvalProfile{Rules: map[string]*RuleStat{
		"data.a.same":    {Evals: 1, Successes: 1},
		"data.a.changed": {Evals: 2, Successes: 1},
		"data.a.removed": {Evals: 1, Successes: 0},
	}}
	after := &EvalProfile{Rules: map[string]*RuleStat{
		"data.a.same":    {Evals: 1, Successes: 1},
		"data.a.changed": {Evals: 5, Successes: 0},
		"data.a.added":   {Evals: 3, Successes: 3},
	}}

	d := before.Diff(after)
	exp := &ProfileDiff{
		Added:   map[string]*RuleStat{"data.a.added": {Evals: 3, Successes: 3}},
		Removed: map[string]*RuleStat{"data.a.removed": {Evals: 1, Successes: 0}},
		Changed: map[string]*RuleStatDelta{"data.a.changed": {EvalsDelta: 3, SuccessesDelta: -1}},
	}
	if !reflect.DeepEqual(d, exp) || !d.HasChanges() {
		t.Fatalf("expected %+v, got %+v", exp, d)
	}

	same := before.Diff(before.Merge(&EvalProfile{}))
	if same.Added != nil || same.Removed != nil || same.Changed != nil || same.HasChanges() {
		t.Fatalf("expected empty diff, got %+v", same)
	}
}

func assertStrings(t *testing.T, name string, got, exp []string) {
	t.Helper()
	if !reflect.DeepEqual(got, exp) {
		t.Fatalf("%s: expected %v, got %v", name, exp, got)
	}
}
