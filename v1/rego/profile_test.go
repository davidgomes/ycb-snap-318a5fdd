// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import (
	"reflect"
	"testing"
)

func testProfile(entries map[string][2]int) *EvalProfile {
	p := &EvalProfile{}
	for path, counts := range entries {
		for range counts[0] {
			p.observe(path, false)
		}
		for range counts[1] {
			p.observe(path, true)
		}
	}
	return p
}

func TestEvalProfileNilReceivers(t *testing.T) {
	var p *EvalProfile
	var stat *RuleStat
	var diff *ProfileDiff

	if p.Stat("data.authz.allow") != nil {
		t.Fatal("Stat")
	}
	if p.RulePaths() != nil {
		t.Fatal("RulePaths")
	}
	if p.SuccessRate("data.authz.allow") != 0 {
		t.Fatal("SuccessRate")
	}
	if p.OverallSuccessRate() != 0 {
		t.Fatal("OverallSuccessRate")
	}
	if p.HotRules(1) != nil || p.FailedRules() != nil || p.SucceededRules() != nil {
		t.Fatal("rule lists")
	}
	if p.Packages() != nil || p.FilterByPackage("data.authz") != nil || p.PackageStats() != nil {
		t.Fatal("package helpers")
	}
	if p.Merge(nil) != nil {
		t.Fatal("Merge both nil")
	}
	if p.ContainsRule("data.authz.allow") {
		t.Fatal("ContainsRule")
	}
	if p.Summary() != "profile: disabled" {
		t.Fatalf("Summary: %q", p.Summary())
	}
	if !p.Equal(nil) || p.Equal(&EvalProfile{}) {
		t.Fatal("Equal nil")
	}
	if p.String() != "<nil>" {
		t.Fatalf("String: %q", p.String())
	}
	if p.Diff(&EvalProfile{}) != nil {
		t.Fatal("Diff nil receiver")
	}
	if diff.HasChanges() {
		t.Fatal("HasChanges nil")
	}
	if stat.SuccessRate() != 0 {
		t.Fatal("RuleStat.SuccessRate")
	}
	if stat.String() != "<nil>" {
		t.Fatalf("RuleStat.String: %q", stat.String())
	}
}

func TestEvalProfileMethods(t *testing.T) {
	p := testProfile(map[string][2]int{
		"data.authz.allow": {4, 2},
		"data.authz.deny":  {3, 0},
		"data.other.p":     {1, 1},
		"data.authz.audit": {0, 0},
	})
	// data.authz.audit was observed zero times; drop it so the zero-eval case is explicit.
	delete(p.rules, "data.authz.audit")
	p.rules["data.z.empty"] = &RuleStat{}

	if st := p.Stat("data.authz.allow"); st == nil || st.Evals != 4 || st.Successes != 2 {
		t.Fatalf("Stat: %+v", st)
	}
	if p.Stat("missing") != nil {
		t.Fatal("missing stat")
	}
	if !p.ContainsRule("data.authz.deny") || p.ContainsRule("missing") {
		t.Fatal("ContainsRule")
	}

	wantPaths := []string{"data.authz.allow", "data.authz.deny", "data.other.p", "data.z.empty"}
	if got := p.RulePaths(); !reflect.DeepEqual(got, wantPaths) {
		t.Fatalf("RulePaths: %v", got)
	}
	if p.SuccessRate("data.authz.allow") != 0.5 {
		t.Fatalf("SuccessRate: %v", p.SuccessRate("data.authz.allow"))
	}
	if p.SuccessRate("missing") != 0 || p.SuccessRate("data.z.empty") != 0 {
		t.Fatal("SuccessRate zero")
	}
	// 4+3+1+0 evals, 2+0+1+0 successes => 3/8
	if p.OverallSuccessRate() != 3.0/8.0 {
		t.Fatalf("OverallSuccessRate: %v", p.OverallSuccessRate())
	}

	if got := p.HotRules(3); !reflect.DeepEqual(got, []string{"data.authz.allow", "data.authz.deny"}) {
		t.Fatalf("HotRules: %v", got)
	}
	if p.HotRules(100) != nil {
		t.Fatal("HotRules none")
	}
	if got := p.FailedRules(); !reflect.DeepEqual(got, []string{"data.authz.deny"}) {
		t.Fatalf("FailedRules: %v", got)
	}
	if got := p.SucceededRules(); !reflect.DeepEqual(got, []string{"data.authz.allow", "data.other.p"}) {
		t.Fatalf("SucceededRules: %v", got)
	}
	if got := p.Packages(); !reflect.DeepEqual(got, []string{"data.authz", "data.other", "data.z"}) {
		t.Fatalf("Packages: %v", got)
	}

	filtered := p.FilterByPackage("data.authz")
	if got := filtered.RulePaths(); !reflect.DeepEqual(got, []string{"data.authz.allow", "data.authz.deny"}) {
		t.Fatalf("FilterByPackage: %v", got)
	}
	filtered.Stat("data.authz.allow").Evals = 99
	if p.Stat("data.authz.allow").Evals != 4 {
		t.Fatal("FilterByPackage aliased stats")
	}
	if p.FilterByPackage("data.missing").RulePaths() != nil {
		t.Fatal("FilterByPackage miss")
	}

	pkgs := p.PackageStats()
	if pkgs["data.authz"].Evals != 7 || pkgs["data.authz"].Successes != 2 {
		t.Fatalf("PackageStats authz: %+v", pkgs["data.authz"])
	}
	if pkgs["data.other"].Evals != 1 || pkgs["data.other"].Successes != 1 {
		t.Fatalf("PackageStats other: %+v", pkgs["data.other"])
	}

	if p.Summary() != "profile: 4 rules, 8 evals, 3 successes" {
		t.Fatalf("Summary: %q", p.Summary())
	}
	wantStr := "Profile:\n" +
		"  data.authz.allow: evals=4 successes=2\n" +
		"  data.authz.deny: evals=3 successes=0\n" +
		"  data.other.p: evals=1 successes=1\n" +
		"  data.z.empty: evals=0 successes=0\n"
	if p.String() != wantStr {
		t.Fatalf("String:\n%s", p.String())
	}
	if (&RuleStat{Evals: 2, Successes: 1}).String() != "evals=2 successes=1" {
		t.Fatal("RuleStat.String")
	}
	if (&RuleStat{}).SuccessRate() != 0 || (&RuleStat{Evals: 4, Successes: 1}).SuccessRate() != 0.25 {
		t.Fatal("RuleStat.SuccessRate")
	}

	clone := testProfile(map[string][2]int{
		"data.authz.allow": {4, 2},
		"data.authz.deny":  {3, 0},
		"data.other.p":     {1, 1},
	})
	clone.rules["data.z.empty"] = &RuleStat{}
	if !p.Equal(clone) || p.Equal(nil) || !(&EvalProfile{}).Equal(&EvalProfile{}) {
		t.Fatal("Equal")
	}
	clone.Stat("data.authz.allow").Successes = 0
	if p.Equal(clone) {
		t.Fatal("Equal should see count change")
	}
}

func TestEvalProfileMergeAndDiff(t *testing.T) {
	left := testProfile(map[string][2]int{
		"data.authz.allow": {2, 1},
		"data.authz.deny":  {1, 0},
	})
	right := testProfile(map[string][2]int{
		"data.authz.allow": {3, 3},
		"data.other.p":     {4, 1},
	})

	if left.Merge(nil) != left || right.Merge(nil) != right {
		t.Fatal("Merge keeps the non-nil side")
	}
	var none *EvalProfile
	if none.Merge(right) != right || none.Merge(none) != nil {
		t.Fatal("Merge nil receiver")
	}

	merged := left.Merge(right)
	if merged == left || merged == right {
		t.Fatal("Merge should allocate")
	}
	if st := merged.Stat("data.authz.allow"); st.Evals != 5 || st.Successes != 4 {
		t.Fatalf("merged allow: %+v", st)
	}
	if st := merged.Stat("data.authz.deny"); st.Evals != 1 || st.Successes != 0 {
		t.Fatalf("merged deny: %+v", st)
	}
	if st := merged.Stat("data.other.p"); st.Evals != 4 || st.Successes != 1 {
		t.Fatalf("merged other: %+v", st)
	}
	// inputs are unchanged
	if left.Stat("data.authz.allow").Evals != 2 || right.Stat("data.authz.allow").Evals != 3 {
		t.Fatal("Merge mutated inputs")
	}

	diff := left.Diff(right)
	if !diff.HasChanges() {
		t.Fatal("expected changes")
	}
	if _, ok := diff.Removed["data.authz.deny"]; !ok || len(diff.Removed) != 1 {
		t.Fatalf("Removed: %+v", diff.Removed)
	}
	if _, ok := diff.Added["data.other.p"]; !ok || len(diff.Added) != 1 {
		t.Fatalf("Added: %+v", diff.Added)
	}
	delta := diff.Changed["data.authz.allow"]
	if delta == nil || delta.EvalsDelta != 1 || delta.SuccessesDelta != 2 || len(diff.Changed) != 1 {
		t.Fatalf("Changed: %+v", diff.Changed["data.authz.allow"])
	}

	same := left.Diff(testProfile(map[string][2]int{
		"data.authz.allow": {2, 1},
		"data.authz.deny":  {1, 0},
	}))
	if same.HasChanges() || same.Added != nil || same.Removed != nil || same.Changed != nil {
		t.Fatalf("unchanged diff: %+v", same)
	}

	onlyRemoved := left.Diff(nil)
	if len(onlyRemoved.Removed) != 2 || onlyRemoved.Added != nil || onlyRemoved.Changed != nil {
		t.Fatalf("diff nil other: %+v", onlyRemoved)
	}
	if (&EvalProfile{}).Diff(&EvalProfile{}).HasChanges() {
		t.Fatal("empty diff")
	}
}

func TestEvalProfileEmptyCollections(t *testing.T) {
	p := &EvalProfile{}
	if p.RulePaths() != nil || p.Packages() != nil || p.PackageStats() != nil {
		t.Fatal("empty profile collections")
	}
	if p.Summary() != "profile: 0 rules, 0 evals, 0 successes" {
		t.Fatalf("Summary: %q", p.Summary())
	}
	if p.String() != "Profile:\n" {
		t.Fatalf("String: %q", p.String())
	}
	if p.OverallSuccessRate() != 0 {
		t.Fatal("OverallSuccessRate")
	}
}
