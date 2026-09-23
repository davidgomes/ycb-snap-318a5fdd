//go:build profile

package rego

import (
	"context"
	"strings"
	"testing"
)

func TestEvalProfileMethods(t *testing.T) {
	var nilProfile *EvalProfile
	if nilProfile.Stat("data.authz.allow") != nil || nilProfile.RulePaths() != nil {
		t.Fatal("nil stat/paths")
	}
	if nilProfile.SuccessRate("x") != 0 || nilProfile.OverallSuccessRate() != 0 {
		t.Fatal("nil rates")
	}
	if nilProfile.HotRules(1) != nil || nilProfile.FailedRules() != nil || nilProfile.SucceededRules() != nil {
		t.Fatal("nil lists")
	}
	if nilProfile.Packages() != nil || nilProfile.FilterByPackage("data.authz") != nil {
		t.Fatal("nil package ops")
	}
	if nilProfile.Merge(nil) != nil {
		t.Fatal("nil merge")
	}
	if nilProfile.PackageStats() != nil || nilProfile.ContainsRule("data.authz.allow") {
		t.Fatal("nil contains")
	}
	if nilProfile.Summary() != "profile: disabled" || nilProfile.String() != "<nil>" {
		t.Fatal("nil summary/string")
	}
	if !nilProfile.Equal(nil) || nilProfile.Equal(newEvalProfile()) {
		t.Fatal("nil equal")
	}
	if nilProfile.Diff(newEvalProfile()) != nil {
		t.Fatal("nil diff")
	}
	var nilDiff *ProfileDiff
	if nilDiff.HasChanges() {
		t.Fatal("nil diff changes")
	}
	var nilStat *RuleStat
	if nilStat.SuccessRate() != 0 || nilStat.String() != "<nil>" {
		t.Fatal("nil rule stat")
	}

	p := newEvalProfile()
	rec := profileRecorder{p: p}
	rec.EnterRule("data.authz.allow")
	rec.SucceedRule("data.authz.allow")
	rec.EnterRule("data.authz.allow")
	rec.EnterRule("data.authz.deny")
	rec.EnterRule("data.other.p")
	rec.SucceedRule("data.other.p")

	if p.Stat("missing") != nil {
		t.Fatal("missing stat")
	}
	allow := p.Stat("data.authz.allow")
	if allow.Evals != 2 || allow.Successes != 1 || allow.SuccessRate() != 0.5 || allow.String() != "evals=2 successes=1" {
		t.Fatalf("allow stat: %+v %s", allow, allow)
	}
	if p.SuccessRate("data.authz.deny") != 0 || p.SuccessRate("missing") != 0 {
		t.Fatal("success rate")
	}
	if p.OverallSuccessRate() != 0.5 {
		t.Fatal(p.OverallSuccessRate())
	}
	if strings.Join(p.RulePaths(), ",") != "data.authz.allow,data.authz.deny,data.other.p" {
		t.Fatal(p.RulePaths())
	}
	if strings.Join(p.HotRules(2), ",") != "data.authz.allow" || p.HotRules(10) != nil {
		t.Fatal(p.HotRules(2), p.HotRules(10))
	}
	if strings.Join(p.FailedRules(), ",") != "data.authz.deny" {
		t.Fatal(p.FailedRules())
	}
	if strings.Join(p.SucceededRules(), ",") != "data.authz.allow,data.other.p" {
		t.Fatal(p.SucceededRules())
	}
	if strings.Join(p.Packages(), ",") != "data.authz,data.other" {
		t.Fatal(p.Packages())
	}
	filtered := p.FilterByPackage("data.authz")
	filtered.Stat("data.authz.allow").Evals = 99
	if p.Stat("data.authz.allow").Evals != 2 || filtered.ContainsRule("data.other.p") {
		t.Fatal("filter copy")
	}
	if !p.ContainsRule("data.authz.deny") {
		t.Fatal("contains")
	}
	pkgs := p.PackageStats()
	if pkgs["data.authz"].Evals != 3 || pkgs["data.authz"].Successes != 1 || pkgs["data.other"].Successes != 1 {
		t.Fatalf("%+v %+v", pkgs["data.authz"], pkgs["data.other"])
	}
	if p.Summary() != "profile: 3 rules, 4 evals, 2 successes" {
		t.Fatal(p.Summary())
	}
	wantStr := "Profile:\n  data.authz.allow: evals=2 successes=1\n  data.authz.deny: evals=1 successes=0\n  data.other.p: evals=1 successes=1\n"
	if p.String() != wantStr {
		t.Fatalf("string\n%q\n%q", p.String(), wantStr)
	}

	other := newEvalProfile()
	orec := profileRecorder{p: other}
	orec.EnterRule("data.authz.allow")
	orec.SucceedRule("data.authz.allow")
	orec.EnterRule("data.authz.allow")
	orec.SucceedRule("data.authz.allow")
	orec.EnterRule("data.new.q")
	merged := p.Merge(other)
	if merged.Stat("data.authz.allow").Evals != 4 || merged.Stat("data.authz.allow").Successes != 3 {
		t.Fatal(merged.Stat("data.authz.allow"))
	}
	if merged.Stat("data.new.q").Evals != 1 || p.Merge(nil) != p || nilProfile.Merge(p) != p {
		t.Fatal("merge identity")
	}
	if !p.Equal(p) || p.Equal(other) || !newEvalProfile().Equal(newEvalProfile()) {
		t.Fatal("equal")
	}

	diff := p.Diff(other)
	if !diff.HasChanges() {
		t.Fatal("expected changes")
	}
	if diff.Added["data.new.q"].Evals != 1 || diff.Removed["data.authz.deny"].Evals != 1 || diff.Removed["data.other.p"] == nil {
		t.Fatalf("%+v %+v", diff.Added, diff.Removed)
	}
	if diff.Changed["data.authz.allow"].EvalsDelta != 0 || diff.Changed["data.authz.allow"].SuccessesDelta != 1 {
		t.Fatal(diff.Changed["data.authz.allow"])
	}
	same := p.Diff(p)
	if same.HasChanges() || same.Added != nil || same.Removed != nil || same.Changed != nil {
		t.Fatal("empty diff should use nil maps")
	}
}

func TestEvalRuleProfile(t *testing.T) {
	ctx := context.Background()
	mod := `package authz
import rego.v1

allow if {
	input.x
}

allow if {
	input.y
}

deny if {
	false
}

other if {
	true
}
`
	rs, err := New(
		Query("data.authz.allow"),
		Module("authz.rego", mod),
		Input(map[string]any{"x": true, "y": false}),
		EnableRuleProfile(true),
	).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Profile == nil {
		t.Fatalf("result profile: %+v", rs)
	}
	prof := rs[0].Profile
	allow := prof.Stat("data.authz.allow")
	if allow == nil || allow.Evals != 2 || allow.Successes != 1 {
		t.Fatalf("allow: %+v paths=%v", allow, prof.RulePaths())
	}
	if prof.ContainsRule("data.authz.deny") || prof.ContainsRule("data.authz.other") {
		t.Fatalf("unexpected rules: %v", prof.RulePaths())
	}

	off, err := New(
		Query("data.authz.allow"),
		Module("authz.rego", mod),
		Input(map[string]any{"x": true}),
	).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(off) != 1 || off[0].Profile != nil {
		t.Fatal("profile should be nil when disabled")
	}

	called := `package authz
import rego.v1

allow if {
	not deny
}

deny if {
	false
}
`
	prepared, err := New(
		Query("data.authz.allow"),
		Module("authz.rego", called),
	).PrepareForEval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	frs, err := prepared.Eval(ctx, EvalRuleProfile(true))
	if err != nil {
		t.Fatal(err)
	}
	if len(frs) != 1 || frs[0].Profile == nil {
		t.Fatal(frs)
	}
	deny := frs[0].Profile.Stat("data.authz.deny")
	if deny == nil || deny.Evals != 1 || deny.Successes != 0 {
		t.Fatalf("deny: %+v paths=%v", deny, frs[0].Profile.RulePaths())
	}
	if frs[0].Profile.Stat("data.authz.allow").Successes != 1 {
		t.Fatal(frs[0].Profile.Stat("data.authz.allow"))
	}
}
