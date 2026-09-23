// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

//go:build profile

package rego

import (
	"slices"
	"testing"
)

func TestEvalProfileNilReceivers(t *testing.T) {
	t.Parallel()

	var p *EvalProfile
	if p.Stat("data.authz.allow") != nil {
		t.Fatal("nil Stat")
	}
	if p.RulePaths() != nil {
		t.Fatal("nil RulePaths")
	}
	if p.SuccessRate("data.authz.allow") != 0 {
		t.Fatal("nil SuccessRate")
	}
	if p.OverallSuccessRate() != 0 {
		t.Fatal("nil OverallSuccessRate")
	}
	if p.HotRules(1) != nil || p.FailedRules() != nil || p.SucceededRules() != nil {
		t.Fatal("nil rule lists")
	}
	if p.Packages() != nil {
		t.Fatal("nil Packages")
	}
	if p.FilterByPackage("data.authz") != nil {
		t.Fatal("nil FilterByPackage")
	}
	if p.PackageStats() != nil {
		t.Fatal("nil PackageStats")
	}
	if p.ContainsRule("data.authz.allow") {
		t.Fatal("nil ContainsRule")
	}
	if p.Summary() != "profile: disabled" {
		t.Fatalf("nil Summary: %q", p.Summary())
	}
	if p.String() != "<nil>" {
		t.Fatalf("nil String: %q", p.String())
	}
	if !p.Equal(nil) {
		t.Fatal("two nil profiles are equal")
	}
	if p.Equal(&EvalProfile{}) {
		t.Fatal("nil profile equals empty profile")
	}
	if (&EvalProfile{}).Equal(nil) {
		t.Fatal("empty profile equals nil")
	}
	if p.Diff(&EvalProfile{}) != nil {
		t.Fatal("nil Diff")
	}
	if p.Merge(nil) != nil {
		t.Fatal("nil Merge nil")
	}

	var d *ProfileDiff
	if d.HasChanges() {
		t.Fatal("nil HasChanges")
	}

	var st *RuleStat
	if st.SuccessRate() != 0 {
		t.Fatal("nil RuleStat SuccessRate")
	}
	if st.String() != "<nil>" {
		t.Fatalf("nil RuleStat String: %q", st.String())
	}
}

func TestEvalProfileMethods(t *testing.T) {
	t.Parallel()

	allow := &RuleStat{Evals: 4, Successes: 2}
	deny := &RuleStat{Evals: 3, Successes: 0}
	other := &RuleStat{Evals: 1, Successes: 1}
	idle := &RuleStat{Evals: 0, Successes: 0}
	p := &EvalProfile{Rules: map[string]*RuleStat{
		"data.authz.allow": allow,
		"data.authz.deny":  deny,
		"data.other.ok":    other,
		"data.authz.idle":  idle,
	}}

	if p.Stat("data.authz.allow") != allow {
		t.Fatal("Stat pointer")
	}
	if p.Stat("missing") != nil {
		t.Fatal("missing Stat")
	}
	if !p.ContainsRule("data.authz.deny") || p.ContainsRule("missing") {
		t.Fatal("ContainsRule")
	}

	paths := p.RulePaths()
	wantPaths := []string{"data.authz.allow", "data.authz.deny", "data.authz.idle", "data.other.ok"}
	if !slices.Equal(paths, wantPaths) {
		t.Fatalf("RulePaths: %v", paths)
	}

	if p.SuccessRate("data.authz.allow") != 0.5 {
		t.Fatalf("allow rate: %v", p.SuccessRate("data.authz.allow"))
	}
	if p.SuccessRate("missing") != 0 || p.SuccessRate("data.authz.idle") != 0 {
		t.Fatal("zero rates")
	}
	if p.OverallSuccessRate() != 3.0/8.0 {
		t.Fatalf("overall: %v", p.OverallSuccessRate())
	}
	if allow.SuccessRate() != 0.5 || idle.SuccessRate() != 0 {
		t.Fatal("RuleStat rate")
	}
	if allow.String() != "evals=4 successes=2" {
		t.Fatalf("RuleStat string: %q", allow.String())
	}

	if got := p.HotRules(3); !slices.Equal(got, []string{"data.authz.allow", "data.authz.deny"}) {
		t.Fatalf("HotRules: %v", got)
	}
	if p.HotRules(100) != nil {
		t.Fatal("HotRules none")
	}
	if got := p.FailedRules(); !slices.Equal(got, []string{"data.authz.deny"}) {
		t.Fatalf("FailedRules: %v", got)
	}
	if got := p.SucceededRules(); !slices.Equal(got, []string{"data.authz.allow", "data.other.ok"}) {
		t.Fatalf("SucceededRules: %v", got)
	}
	if got := p.Packages(); !slices.Equal(got, []string{"data.authz", "data.other"}) {
		t.Fatalf("Packages: %v", got)
	}

	filtered := p.FilterByPackage("data.authz")
	if filtered == nil || filtered == p {
		t.Fatal("FilterByPackage identity")
	}
	if !slices.Equal(filtered.RulePaths(), []string{"data.authz.allow", "data.authz.deny", "data.authz.idle"}) {
		t.Fatalf("filtered paths: %v", filtered.RulePaths())
	}
	filtered.Rules["data.authz.allow"].Evals = 99
	if allow.Evals != 4 {
		t.Fatal("FilterByPackage did not deep copy")
	}
	if p.FilterByPackage("data.missing") == nil {
		t.Fatal("empty filter is non-nil")
	}
	if p.FilterByPackage("data.missing").RulePaths() != nil {
		t.Fatal("empty filter paths")
	}
	if p.FilterByPackage("data.auth").ContainsRule("data.authz.allow") {
		t.Fatal("package prefix must not match")
	}

	pkgStats := p.PackageStats()
	authz := pkgStats["data.authz"]
	if authz.Evals != 7 || authz.Successes != 2 {
		t.Fatalf("package stats: %+v", authz)
	}
	if pkgStats["data.other"].Evals != 1 || pkgStats["data.other"].Successes != 1 {
		t.Fatal("other package stats")
	}
	authz.Evals = 0
	if p.Stat("data.authz.allow").Evals != 4 {
		t.Fatal("PackageStats aliased a rule stat")
	}

	if p.Summary() != "profile: 4 rules, 8 evals, 3 successes" {
		t.Fatalf("Summary: %q", p.Summary())
	}
	wantStr := "Profile:\n" +
		"  data.authz.allow: evals=4 successes=2\n" +
		"  data.authz.deny: evals=3 successes=0\n" +
		"  data.authz.idle: evals=0 successes=0\n" +
		"  data.other.ok: evals=1 successes=1\n"
	if p.String() != wantStr {
		t.Fatalf("String:\n%s", p.String())
	}
	if (&EvalProfile{}).String() != "Profile:\n" {
		t.Fatalf("empty String: %q", (&EvalProfile{}).String())
	}
	if (&EvalProfile{}).Summary() != "profile: 0 rules, 0 evals, 0 successes" {
		t.Fatalf("empty Summary: %q", (&EvalProfile{}).Summary())
	}
	if (&EvalProfile{}).RulePaths() != nil || (&EvalProfile{}).Packages() != nil || (&EvalProfile{}).PackageStats() != nil {
		t.Fatal("empty profile lists")
	}

	same := &EvalProfile{Rules: map[string]*RuleStat{
		"data.authz.allow": {Evals: 4, Successes: 2},
		"data.authz.deny":  {Evals: 3, Successes: 0},
		"data.other.ok":    {Evals: 1, Successes: 1},
		"data.authz.idle":  {Evals: 0, Successes: 0},
	}}
	if !p.Equal(same) || !same.Equal(p) {
		t.Fatal("Equal")
	}
	same.Rules["data.authz.allow"].Successes = 1
	if p.Equal(same) {
		t.Fatal("Equal ignored count change")
	}
}

func TestEvalProfileMergeAndDiff(t *testing.T) {
	t.Parallel()

	left := &EvalProfile{Rules: map[string]*RuleStat{
		"data.authz.allow": {Evals: 2, Successes: 1},
		"data.authz.deny":  {Evals: 1, Successes: 0},
	}}
	right := &EvalProfile{Rules: map[string]*RuleStat{
		"data.authz.allow": {Evals: 3, Successes: 1},
		"data.other.ok":    {Evals: 4, Successes: 4},
	}}

	if left.Merge(nil) != left || right.Merge(nil) != right {
		t.Fatal("Merge keeps the non-nil receiver")
	}
	if (*EvalProfile)(nil).Merge(right) != right {
		t.Fatal("Merge returns the other side")
	}

	merged := left.Merge(right)
	if merged == left || merged == right {
		t.Fatal("Merge allocated")
	}
	if merged.Stat("data.authz.allow").Evals != 5 || merged.Stat("data.authz.allow").Successes != 2 {
		t.Fatalf("merged allow: %+v", merged.Stat("data.authz.allow"))
	}
	if merged.Stat("data.authz.deny").Evals != 1 || merged.Stat("data.other.ok").Successes != 4 {
		t.Fatal("merged sides")
	}
	merged.Rules["data.authz.allow"].Evals = 0
	if left.Stat("data.authz.allow").Evals != 2 || right.Stat("data.authz.allow").Evals != 3 {
		t.Fatal("Merge aliased inputs")
	}

	diff := left.Diff(right)
	if !diff.HasChanges() {
		t.Fatal("expected changes")
	}
	if len(diff.Added) != 1 || diff.Added["data.other.ok"].Evals != 4 {
		t.Fatalf("Added: %+v", diff.Added)
	}
	if len(diff.Removed) != 1 || diff.Removed["data.authz.deny"].Successes != 0 {
		t.Fatalf("Removed: %+v", diff.Removed)
	}
	delta := diff.Changed["data.authz.allow"]
	if delta == nil || delta.EvalsDelta != 1 || delta.SuccessesDelta != 0 {
		t.Fatalf("Changed: %+v", delta)
	}
	if diff.Added["data.other.ok"] != right.Rules["data.other.ok"] {
		t.Fatal("Added should reference the other profile")
	}
	if diff.Removed["data.authz.deny"] != left.Rules["data.authz.deny"] {
		t.Fatal("Removed should reference the receiver")
	}

	same := left.Diff(left)
	if same.HasChanges() || same.Added != nil || same.Removed != nil || same.Changed != nil {
		t.Fatalf("identical diff: %+v", same)
	}

	onlyRemoved := left.Diff(nil)
	if !onlyRemoved.HasChanges() || len(onlyRemoved.Removed) != 2 || onlyRemoved.Added != nil || onlyRemoved.Changed != nil {
		t.Fatalf("diff nil other: %+v", onlyRemoved)
	}

	empty := (&EvalProfile{}).Diff(&EvalProfile{})
	if empty == nil || empty.HasChanges() {
		t.Fatal("empty diff")
	}
}

func TestRuleProfileEvaluation(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	module := `package authz

default allow := false

allow if { false }

allow if { input.admin }

deny if { input.bad }

helper if { false }
`

	rs, err := New(
		Query("data.authz.allow"),
		Module("authz.rego", module),
		Input(map[string]any{"admin": true, "bad": false}),
		EnableRuleProfile(true),
	).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Profile == nil {
		t.Fatalf("expected one profiled result, got %#v", rs)
	}
	prof := rs[0].Profile
	allow := prof.Stat("data.authz.allow")
	if allow == nil || allow.Evals < 2 || allow.Successes < 1 {
		t.Fatalf("allow stat: %+v profile:\n%s", allow, prof)
	}
	// The failing definition is entered, and the succeeding definition is entered.
	// The default is not entered once another definition succeeds.
	if allow.Successes > allow.Evals {
		t.Fatalf("allow successes exceed evals: %+v", allow)
	}
	if !prof.ContainsRule("data.authz.allow") {
		t.Fatal("allow missing")
	}
	// deny and helper are not referenced, so they are not entered.
	if prof.ContainsRule("data.authz.deny") || prof.ContainsRule("data.authz.helper") {
		t.Fatalf("unreferenced rules were entered:\n%s", prof)
	}

	// Failed helper is entered because allow references it, and the query still succeeds.
	nested, err := New(
		Query("data.authz.allow"),
		Module("authz.rego", `package authz
allow if { not helper }
helper if { false }
`),
		EnableRuleProfile(true),
	).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nested) != 1 {
		t.Fatalf("nested results: %d", len(nested))
	}
	helper := nested[0].Profile.Stat("data.authz.helper")
	if helper == nil || helper.Evals == 0 || helper.Successes != 0 {
		t.Fatalf("failed helper: %+v\n%s", helper, nested[0].Profile)
	}
	if nested[0].Profile.Stat("data.authz.allow").Successes == 0 {
		t.Fatalf("allow should succeed:\n%s", nested[0].Profile)
	}
	failed := nested[0].Profile.FailedRules()
	if !slices.Contains(failed, "data.authz.helper") {
		t.Fatalf("FailedRules: %v", failed)
	}

	// Every definition of a failing rule is visible even when the query is undefined.
	undefined, err := New(
		Query("data.authz.allow"),
		Module("authz.rego", `package authz
allow if { false }
allow if { input.admin }
`),
		Input(map[string]any{"admin": false}),
		EnableRuleProfile(true),
	).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(undefined) != 1 || undefined[0].Profile == nil {
		t.Fatalf("undefined profiled result: %#v", undefined)
	}
	failedAllow := undefined[0].Profile.Stat("data.authz.allow")
	if failedAllow == nil || failedAllow.Evals != 2 || failedAllow.Successes != 0 {
		t.Fatalf("two failing definitions: %+v\n%s", failedAllow, undefined[0].Profile)
	}
	if len(undefined[0].Expressions) != 0 {
		t.Fatalf("undefined query should not invent expression values: %+v", undefined[0].Expressions)
	}

	// Profiling stays off unless requested, including for undefined queries.
	off, err := New(
		Query("data.authz.allow"),
		Module("authz.rego", "package authz\nallow if { false }\n"),
	).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if off != nil {
		t.Fatalf("profiling disabled should keep an undefined query nil, got %#v", off)
	}

	plain, err := New(
		Query("data.authz.allow"),
		Module("authz.rego", "package authz\nallow if { true }\n"),
	).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != 1 || plain[0].Profile != nil {
		t.Fatal("Profile must be nil when profiling is disabled")
	}
}

func TestRuleProfileEvalOption(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	r := New(
		Query("data.authz.allow"),
		Module("authz.rego", `package authz
allow if { false }
allow if { true }
`),
		EnableRuleProfile(true),
	)
	pq, err := r.PrepareForEval(ctx)
	if err != nil {
		t.Fatal(err)
	}

	first, err := pq.Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := pq.Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first[0].Profile == second[0].Profile {
		t.Fatal("profiles from separate evals must not be shared")
	}
	a := first[0].Profile.Stat("data.authz.allow")
	b := second[0].Profile.Stat("data.authz.allow")
	if a == nil || b == nil || a.Evals != b.Evals || a.Successes != b.Successes {
		t.Fatalf("per-eval counts drifted: %+v vs %+v", a, b)
	}
	if a.Evals != 2 || a.Successes != 1 {
		t.Fatalf("expected both definitions, one success: %+v\n%s", a, first[0].Profile)
	}

	disabled, err := pq.Eval(ctx, EvalRuleProfile(false))
	if err != nil {
		t.Fatal(err)
	}
	if len(disabled) != 1 || disabled[0].Profile != nil {
		t.Fatal("EvalRuleProfile(false) must override EnableRuleProfile")
	}

	optIn, err := New(
		Query("data.authz.allow"),
		Module("authz.rego", "package authz\nallow if { true }\n"),
	).PrepareForEval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	off, err := optIn.Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if off[0].Profile != nil {
		t.Fatal("prepared query should not profile by default")
	}
	on, err := optIn.Eval(ctx, EvalRuleProfile(true))
	if err != nil {
		t.Fatal(err)
	}
	if on[0].Profile == nil || on[0].Profile.Stat("data.authz.allow").Successes != 1 {
		t.Fatalf("EvalRuleProfile(true): %s", on[0].Profile)
	}
}

func TestRuleProfileFunctionDefinitions(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	pq, err := New(
		Query("data.authz.allow"),
		Module("authz.rego", `package authz
allow if { permitted("read") }
permitted(action) if { action == "write" }
permitted(action) if { action == "read" }
`),
		EnableRuleProfile(true),
	).PrepareForEval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := pq.Eval(ctx, EvalRuleIndexing(false))
	if err != nil {
		t.Fatal(err)
	}
	allow := rs[0].Profile.Stat("data.authz.allow")
	permitted := rs[0].Profile.Stat("data.authz.permitted")
	if allow == nil || allow.Evals != 1 || allow.Successes != 1 {
		t.Fatalf("allow: %+v\n%s", allow, rs[0].Profile)
	}
	// The non-matching definition is entered and fails; the matching one succeeds.
	if permitted == nil || permitted.Evals != 2 || permitted.Successes != 1 {
		t.Fatalf("permitted definitions: %+v\n%s", permitted, rs[0].Profile)
	}
}

func TestRuleProfileMultipleResultsAndPackages(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rs, err := New(
		Query("data.other.ok"),
		Module("authz.rego", `package authz
item contains x if { x := [1, 2][_] }
ok if { true }
`),
		Module("other.rego", `package other
ok if { data.authz.ok }
`),
		EnableRuleProfile(true),
	).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Profile == nil {
		t.Fatalf("results: %#v", rs)
	}
	prof := rs[0].Profile
	if prof.Stat("data.other.ok").Successes != 1 || prof.Stat("data.authz.ok").Successes != 1 {
		t.Fatalf("nested packages:\n%s", prof)
	}
	if !slices.Equal(prof.Packages(), []string{"data.authz", "data.other"}) {
		t.Fatalf("packages: %v", prof.Packages())
	}
	authz := prof.FilterByPackage("data.authz")
	if authz.ContainsRule("data.other.ok") || !authz.ContainsRule("data.authz.ok") {
		t.Fatalf("filter:\n%s", authz)
	}

	multi, err := New(
		Query("data.authz.item[x]"),
		Module("authz.rego", `package authz
item contains x if { x := [1, 2][_] }
`),
		EnableRuleProfile(true),
	).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(multi) != 2 {
		t.Fatalf("expected two results, got %d\n%s", len(multi), multi[0].Profile)
	}
	if multi[0].Profile != multi[1].Profile {
		t.Fatal("results from one evaluation should share a profile")
	}
	item := multi[0].Profile.Stat("data.authz.item")
	if item == nil || item.Evals != 1 || item.Successes != 2 {
		t.Fatalf("partial rule solutions: %+v\n%s", item, multi[0].Profile)
	}
}
