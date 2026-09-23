// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import "testing"

func TestEvalProfileNilReceivers(t *testing.T) {
	var p *EvalProfile
	var other *EvalProfile
	if p.Stat("data.authz.allow") != nil {
		t.Fatal("Stat")
	}
	if p.RulePaths() != nil || p.HotRules(1) != nil || p.FailedRules() != nil || p.SucceededRules() != nil {
		t.Fatal("nil profile returned lists")
	}
	if p.SuccessRate("data.authz.allow") != 0 || p.OverallSuccessRate() != 0 {
		t.Fatal("rates")
	}
	if p.Packages() != nil || p.FilterByPackage("data.authz") != nil || p.PackageStats() != nil {
		t.Fatal("package helpers")
	}
	if p.Merge(nil) != nil {
		t.Fatal("merge nil,nil")
	}
	if p.ContainsRule("data.authz.allow") {
		t.Fatal("contains")
	}
	if p.Summary() != "profile: disabled" || p.String() != "<nil>" {
		t.Fatalf("summary %q string %q", p.Summary(), p.String())
	}
	if !p.Equal(nil) || p.Equal(newEvalProfile()) {
		t.Fatal("equal")
	}
	if p.Diff(other) != nil || p.Diff(newEvalProfile()) != nil {
		t.Fatal("diff nil receiver")
	}
	var diff *ProfileDiff
	if diff.HasChanges() {
		t.Fatal("nil diff has changes")
	}
	var st *RuleStat
	if st.SuccessRate() != 0 || st.String() != "<nil>" {
		t.Fatal("nil rule stat")
	}
}

func TestEvalProfileMethods(t *testing.T) {
	p := newEvalProfile()
	if p.Summary() != "profile: 0 rules, 0 evals, 0 successes" {
		t.Fatalf("empty summary: %s", p.Summary())
	}
	if p.String() != "Profile:\n" {
		t.Fatalf("empty string: %q", p.String())
	}
	if p.RulePaths() != nil || p.Packages() != nil || p.PackageStats() != nil {
		t.Fatal("empty profile should not report rules")
	}
	if p.SuccessRate("missing") != 0 || p.Stat("missing") != nil || p.ContainsRule("missing") {
		t.Fatal("missing rule")
	}
	if (&RuleStat{}).SuccessRate() != 0 {
		t.Fatal("zero evals")
	}

	record(p, "data.authz.allow", true)
	record(p, "data.authz.allow", false)
	record(p, "data.authz.deny", false)
	record(p, "data.lib.helper", true)
	record(p, "data.lib.helper", true)
	// A second solution from one entry must not increment successes again.
	p.enter("data.authz.allow")
	p.succeed("data.authz.allow")
	p.succeed("data.authz.allow")
	p.leave("data.authz.allow")

	allow := p.Stat("data.authz.allow")
	if allow == nil || allow.Evals != 3 || allow.Successes != 2 || allow.String() != "evals=3 successes=2" {
		t.Fatalf("allow stat: %+v %s", allow, allow)
	}
	if p.SuccessRate("data.authz.allow") != float64(2)/float64(3) {
		t.Fatalf("allow rate %v", p.SuccessRate("data.authz.allow"))
	}
	if p.OverallSuccessRate() != float64(4)/float64(6) {
		t.Fatalf("overall %v", p.OverallSuccessRate())
	}
	if p.Summary() != "profile: 3 rules, 6 evals, 4 successes" {
		t.Fatalf("summary: %s", p.Summary())
	}
	if got, want := p.String(), "Profile:\n  data.authz.allow: evals=3 successes=2\n  data.authz.deny: evals=1 successes=0\n  data.lib.helper: evals=2 successes=2\n"; got != want {
		t.Fatalf("string:\n%s\nwant:\n%s", got, want)
	}

	if paths := p.RulePaths(); !sameStrings(paths, []string{"data.authz.allow", "data.authz.deny", "data.lib.helper"}) {
		t.Fatalf("paths %v", paths)
	}
	if hot := p.HotRules(2); !sameStrings(hot, []string{"data.authz.allow", "data.lib.helper"}) {
		t.Fatalf("hot %v", hot)
	}
	if p.HotRules(10) != nil {
		t.Fatal("hot none")
	}
	if failed := p.FailedRules(); !sameStrings(failed, []string{"data.authz.deny"}) {
		t.Fatalf("failed %v", failed)
	}
	if ok := p.SucceededRules(); !sameStrings(ok, []string{"data.authz.allow", "data.lib.helper"}) {
		t.Fatalf("succeeded %v", ok)
	}
	if pkgs := p.Packages(); !sameStrings(pkgs, []string{"data.authz", "data.lib"}) {
		t.Fatalf("packages %v", pkgs)
	}
	if !p.ContainsRule("data.lib.helper") || p.ContainsRule("data.lib.other") {
		t.Fatal("contains")
	}

	stats := p.PackageStats()
	if stats["data.authz"].Evals != 4 || stats["data.authz"].Successes != 2 {
		t.Fatalf("authz package stats %+v", stats["data.authz"])
	}
	if stats["data.lib"].Evals != 2 || stats["data.lib"].Successes != 2 {
		t.Fatalf("lib package stats %+v", stats["data.lib"])
	}

	filtered := p.FilterByPackage("data.authz")
	if filtered == nil || filtered == p {
		t.Fatal("filter identity")
	}
	filtered.Stat("data.authz.allow").Evals = 100
	if p.Stat("data.authz.allow").Evals != 3 {
		t.Fatal("filter did not deep copy")
	}
	if filtered.ContainsRule("data.lib.helper") || !filtered.ContainsRule("data.authz.deny") {
		t.Fatalf("filter contents %s", filtered)
	}
	if empty := p.FilterByPackage("data.missing"); empty == nil || empty.RulePaths() != nil {
		t.Fatal("filter miss")
	}
	if p.FilterByPackage("data.authz").PackageStats()["data.authz"].Successes != 2 {
		t.Fatal("filtered package stats")
	}

	other := newEvalProfile()
	record(other, "data.authz.allow", true)
	record(other, "data.other.rule", false)

	merged := p.Merge(other)
	if merged.Stat("data.authz.allow").Evals != 4 || merged.Stat("data.authz.allow").Successes != 3 {
		t.Fatalf("merged allow %+v", merged.Stat("data.authz.allow"))
	}
	if merged.Stat("data.other.rule").Evals != 1 || merged.Stat("data.lib.helper").Evals != 2 {
		t.Fatalf("merged profile %s", merged)
	}
	merged.Stat("data.authz.allow").Successes = 0
	if p.Stat("data.authz.allow").Successes != 2 || other.Stat("data.authz.allow").Successes != 1 {
		t.Fatal("merge aliased stats")
	}
	if p.Merge(nil) != p || other.Merge(nil) != other {
		t.Fatal("merge with nil should return the receiver")
	}
	if (*EvalProfile)(nil).Merge(other) != other {
		t.Fatal("merge nil receiver")
	}

	if !p.Equal(cloneProfile(p)) || p.Equal(other) || p.Equal(nil) {
		t.Fatal("equal")
	}
	same := newEvalProfile()
	record(same, "data.authz.allow", true)
	record(same, "data.authz.allow", false)
	record(same, "data.authz.allow", true)
	record(same, "data.authz.deny", false)
	record(same, "data.lib.helper", true)
	record(same, "data.lib.helper", true)
	if !p.Equal(same) {
		t.Fatalf("reconstructed profile not equal\n%s\n%s", p, same)
	}

	diff := p.Diff(other)
	if !diff.HasChanges() {
		t.Fatal("expected changes")
	}
	if diff.Added["data.other.rule"].Evals != 1 || diff.Added["data.authz.allow"] != nil {
		t.Fatalf("added %#v", diff.Added)
	}
	if diff.Removed["data.authz.deny"] == nil || diff.Removed["data.lib.helper"] == nil || diff.Removed["data.authz.allow"] != nil {
		t.Fatalf("removed %#v", diff.Removed)
	}
	delta := diff.Changed["data.authz.allow"]
	if delta == nil || delta.EvalsDelta != -2 || delta.SuccessesDelta != -1 {
		t.Fatalf("delta %+v", delta)
	}
	diff.Added["data.other.rule"].Evals = 9
	if other.Stat("data.other.rule").Evals != 1 {
		t.Fatal("diff aliased added stat")
	}

	noChange := p.Diff(cloneProfile(p))
	if noChange == nil || noChange.HasChanges() || noChange.Added != nil || noChange.Removed != nil || noChange.Changed != nil {
		t.Fatalf("unchanged diff %#v", noChange)
	}
	onlyRemoved := p.Diff(nil)
	if !onlyRemoved.HasChanges() || onlyRemoved.Added != nil || onlyRemoved.Changed != nil || onlyRemoved.Removed["data.lib.helper"] == nil {
		t.Fatalf("diff against nil %#v", onlyRemoved)
	}

	// Nested same-path rules: the outer entry still succeeds once.
	nested := newEvalProfile()
	nested.enter("data.rec.p")
	nested.enter("data.rec.p")
	nested.succeed("data.rec.p")
	nested.succeed("data.rec.p")
	nested.leave("data.rec.p")
	nested.succeed("data.rec.p")
	nested.leave("data.rec.p")
	if st := nested.Stat("data.rec.p"); st.Evals != 2 || st.Successes != 2 {
		t.Fatalf("nested %+v", st)
	}
}

func record(p *EvalProfile, path string, success bool) {
	p.enter(path)
	if success {
		p.succeed(path)
	}
	p.leave(path)
}

func cloneProfile(p *EvalProfile) *EvalProfile {
	return newEvalProfile().Merge(p)
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
