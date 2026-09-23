// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

//go:build profile

package rego

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const profileTestModule = `package test

default allow := false

allow if input.x > 10

allow if input.x > 0

s contains v if {
	some v in input.xs
	v > 1
}

f(a) := a * 2 if a > 0

g := f(input.x)

never if false
`

func TestRuleProfileEval(t *testing.T) {
	tests := []struct {
		note  string
		query string
		input map[string]any
		exp   map[string]*RuleStat
	}{
		{
			note:  "multiple definitions",
			query: "data.test.allow",
			input: map[string]any{"x": 5},
			exp: map[string]*RuleStat{
				"data.test.allow": {Evals: 2, Successes: 1},
			},
		},
		{
			note:  "default rule",
			query: "data.test.allow",
			input: map[string]any{"x": -1},
			exp: map[string]*RuleStat{
				"data.test.allow": {Evals: 3, Successes: 1},
			},
		},
		{
			note:  "multiple results count as one success",
			query: "data.test.s",
			input: map[string]any{"xs": []int{1, 2, 3}},
			exp: map[string]*RuleStat{
				"data.test.s": {Evals: 1, Successes: 1},
			},
		},
		{
			note:  "functions",
			query: "data.test.g",
			input: map[string]any{"x": 5},
			exp: map[string]*RuleStat{
				"data.test.f": {Evals: 1, Successes: 1},
				"data.test.g": {Evals: 1, Successes: 1},
			},
		},
		{
			note:  "failed rules",
			query: "data.test",
			input: map[string]any{"x": -1, "xs": []int{}},
			exp: map[string]*RuleStat{
				"data.test.allow": {Evals: 3, Successes: 1},
				"data.test.f":     {Evals: 1, Successes: 0},
				"data.test.g":     {Evals: 1, Successes: 0},
				"data.test.never": {Evals: 1, Successes: 0},
				"data.test.s":     {Evals: 1, Successes: 0},
			},
		},
		{
			note:  "no rules",
			query: "x := 1",
			exp:   map[string]*RuleStat{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.note, func(t *testing.T) {
			rs, err := New(
				Query(tc.query),
				Module("test.rego", profileTestModule),
				Input(tc.input),
				EnableRuleProfile(true),
			).Eval(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(rs) != 1 {
				t.Fatalf("expected 1 result, got %d", len(rs))
			}
			if rs[0].Profile == nil {
				t.Fatal("expected profile")
			}
			if exp := (&EvalProfile{Rules: tc.exp}); !rs[0].Profile.Equal(exp) {
				t.Fatalf("expected:\n%v\ngot:\n%v", exp, rs[0].Profile)
			}
		})
	}
}

func TestRuleProfileSharedByResults(t *testing.T) {
	rs, err := New(
		Query("x := data.test.s[_]"),
		Module("test.rego", profileTestModule),
		Input(map[string]any{"xs": []int{1, 2, 3}}),
		EnableRuleProfile(true),
	).Eval(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 2 {
		t.Fatalf("expected 2 results, got %d", len(rs))
	}
	if rs[0].Profile == nil || rs[0].Profile != rs[1].Profile {
		t.Fatalf("expected results to share a profile, got %v and %v", rs[0].Profile, rs[1].Profile)
	}
}

func TestRuleProfileOptions(t *testing.T) {
	tests := []struct {
		note     string
		regoOpts []func(*Rego)
		evalOpts []EvalOption
		enabled  bool
	}{
		{
			note: "disabled by default",
		},
		{
			note:     "enabled on construction",
			regoOpts: []func(*Rego){EnableRuleProfile(true)},
			enabled:  true,
		},
		{
			note:     "enabled per evaluation",
			evalOpts: []EvalOption{EvalRuleProfile(true)},
			enabled:  true,
		},
		{
			note:     "disabled per evaluation",
			regoOpts: []func(*Rego){EnableRuleProfile(true)},
			evalOpts: []EvalOption{EvalRuleProfile(false)},
		},
		{
			note:     "disabled on construction",
			regoOpts: []func(*Rego){EnableRuleProfile(false)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.note, func(t *testing.T) {
			opts := append([]func(*Rego){
				Query("data.test.allow"),
				Module("test.rego", profileTestModule),
			}, tc.regoOpts...)
			pq, err := New(opts...).PrepareForEval(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			evalOpts := append([]EvalOption{EvalInput(map[string]any{"x": 5})}, tc.evalOpts...)

			var prev *EvalProfile
			for range 2 {
				rs, err := pq.Eval(t.Context(), evalOpts...)
				if err != nil {
					t.Fatal(err)
				}
				profile := rs[0].Profile
				if !tc.enabled {
					if profile != nil {
						t.Fatalf("expected no profile, got %v", profile)
					}
					continue
				}
				if exp := "profile: 1 rules, 2 evals, 1 successes"; profile.Summary() != exp {
					t.Fatalf("expected %q, got %q", exp, profile.Summary())
				}
				if prev == profile {
					t.Fatal("expected a new profile for every evaluation")
				}
				prev = profile
			}
		})
	}
}

func TestRuleProfileJSON(t *testing.T) {
	rs, err := New(
		Query("data.test.allow"),
		Module("test.rego", profileTestModule),
		Input(map[string]any{"x": 5}),
		EnableRuleProfile(true),
	).Eval(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	bs, err := json.Marshal(rs[0])
	if err != nil {
		t.Fatal(err)
	}
	if exp := `"profile":{"rules":{"data.test.allow":{"evals":2,"successes":1}}}`; !strings.Contains(string(bs), exp) {
		t.Fatalf("expected %s to contain %s", bs, exp)
	}

	var result Result
	if err := json.Unmarshal(bs, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Profile.Equal(rs[0].Profile) {
		t.Fatalf("expected %v, got %v", rs[0].Profile, result.Profile)
	}

	bs, err = json.Marshal(Result{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bs), "profile") {
		t.Fatalf("expected no profile in %s", bs)
	}
}

func testProfile() *EvalProfile {
	return &EvalProfile{Rules: map[string]*RuleStat{
		"data.authz.allow":    {Evals: 4, Successes: 1},
		"data.authz.deny":     {Evals: 2, Successes: 0},
		"data.authz.is_admin": {Evals: 1, Successes: 1},
		"data.lib.helper":     {Evals: 10, Successes: 10},
		`data.lib["x.y"]`:     {Evals: 0, Successes: 0},
	}}
}

func TestEvalProfileQueries(t *testing.T) {
	p := testProfile()

	if s := p.Stat("data.authz.allow"); s == nil || *s != (RuleStat{Evals: 4, Successes: 1}) {
		t.Errorf("unexpected stat: %v", s)
	}
	if s := p.Stat("data.authz.missing"); s != nil {
		t.Errorf("expected nil stat, got %v", s)
	}
	if !p.ContainsRule("data.lib.helper") || p.ContainsRule("data.lib") {
		t.Error("unexpected ContainsRule result")
	}

	rates := map[string]float64{
		"data.authz.allow":   0.25,
		"data.lib.helper":    1,
		`data.lib["x.y"]`:    0,
		"data.authz.missing": 0,
	}
	for rule, exp := range rates {
		if act := p.SuccessRate(rule); act != exp {
			t.Errorf("SuccessRate(%q): expected %v, got %v", rule, exp, act)
		}
	}
	if exp, act := 12.0/17.0, p.OverallSuccessRate(); act != exp {
		t.Errorf("OverallSuccessRate: expected %v, got %v", exp, act)
	}
	if act := (&EvalProfile{}).OverallSuccessRate(); act != 0 {
		t.Errorf("OverallSuccessRate of empty profile: expected 0, got %v", act)
	}

	paths := []struct {
		note string
		act  []string
		exp  []string
	}{
		{"RulePaths", p.RulePaths(), []string{"data.authz.allow", "data.authz.deny", "data.authz.is_admin", "data.lib.helper", `data.lib["x.y"]`}},
		{"RulePaths empty", (&EvalProfile{}).RulePaths(), nil},
		{"HotRules", p.HotRules(2), []string{"data.authz.allow", "data.authz.deny", "data.lib.helper"}},
		{"HotRules none", p.HotRules(11), nil},
		{"FailedRules", p.FailedRules(), []string{"data.authz.deny"}},
		{"SucceededRules", p.SucceededRules(), []string{"data.authz.allow", "data.authz.is_admin", "data.lib.helper"}},
		{"Packages", p.Packages(), []string{"data.authz", "data.lib"}},
		{"Packages empty", (&EvalProfile{}).Packages(), nil},
	}
	for _, tc := range paths {
		if !reflect.DeepEqual(tc.act, tc.exp) {
			t.Errorf("%s: expected %q, got %q", tc.note, tc.exp, tc.act)
		}
	}

	expStats := map[string]*RuleStat{
		"data.authz": {Evals: 7, Successes: 2},
		"data.lib":   {Evals: 10, Successes: 10},
	}
	if act := p.PackageStats(); !reflect.DeepEqual(act, expStats) {
		t.Errorf("PackageStats: expected %v, got %v", expStats, act)
	}

	if exp, act := "profile: 5 rules, 17 evals, 12 successes", p.Summary(); act != exp {
		t.Errorf("Summary: expected %q, got %q", exp, act)
	}

	exp := `Profile:
  data.authz.allow: evals=4 successes=1
  data.authz.deny: evals=2 successes=0
  data.authz.is_admin: evals=1 successes=1
  data.lib.helper: evals=10 successes=10
  data.lib["x.y"]: evals=0 successes=0
`
	if act := p.String(); act != exp {
		t.Errorf("String: expected:\n%s\ngot:\n%s", exp, act)
	}
	if exp, act := "Profile:\n", (&EvalProfile{}).String(); act != exp {
		t.Errorf("String of empty profile: expected %q, got %q", exp, act)
	}
}

func TestEvalProfileFilterByPackage(t *testing.T) {
	p := testProfile()

	filtered := p.FilterByPackage("data.lib")
	exp := &EvalProfile{Rules: map[string]*RuleStat{
		"data.lib.helper": {Evals: 10, Successes: 10},
		`data.lib["x.y"]`: {Evals: 0, Successes: 0},
	}}
	if !filtered.Equal(exp) {
		t.Fatalf("expected:\n%v\ngot:\n%v", exp, filtered)
	}

	filtered.Stat("data.lib.helper").Evals = 100
	if p.Stat("data.lib.helper").Evals != 10 {
		t.Fatal("expected filtered profile to hold copies of the stats")
	}

	if empty := p.FilterByPackage("data.missing"); empty == nil || empty.RulePaths() != nil {
		t.Fatalf("expected empty profile, got %v", empty)
	}
}

func TestEvalProfileMerge(t *testing.T) {
	a := &EvalProfile{Rules: map[string]*RuleStat{
		"data.x.p": {Evals: 1, Successes: 1},
		"data.x.q": {Evals: 2, Successes: 0},
	}}
	b := &EvalProfile{Rules: map[string]*RuleStat{
		"data.x.q": {Evals: 3, Successes: 1},
		"data.x.r": {Evals: 1, Successes: 0},
	}}

	merged := a.Merge(b)
	exp := &EvalProfile{Rules: map[string]*RuleStat{
		"data.x.p": {Evals: 1, Successes: 1},
		"data.x.q": {Evals: 5, Successes: 1},
		"data.x.r": {Evals: 1, Successes: 0},
	}}
	if !merged.Equal(exp) {
		t.Fatalf("expected:\n%v\ngot:\n%v", exp, merged)
	}

	merged.Stat("data.x.p").Evals = 100
	if a.Stat("data.x.p").Evals != 1 || b.Stat("data.x.q").Evals != 3 {
		t.Fatal("expected merge to leave its inputs unchanged")
	}

	var nilProfile *EvalProfile
	if nilProfile.Merge(nil) != nil {
		t.Error("expected merging nil profiles to return nil")
	}
	if nilProfile.Merge(b) != b {
		t.Error("expected merge with nil receiver to return other")
	}
	if a.Merge(nil) != a {
		t.Error("expected merge with nil to return receiver")
	}
}

func TestEvalProfileDiff(t *testing.T) {
	a := &EvalProfile{Rules: map[string]*RuleStat{
		"data.x.same":    {Evals: 1, Successes: 1},
		"data.x.changed": {Evals: 2, Successes: 1},
		"data.x.removed": {Evals: 1, Successes: 0},
	}}
	b := &EvalProfile{Rules: map[string]*RuleStat{
		"data.x.same":    {Evals: 1, Successes: 1},
		"data.x.changed": {Evals: 5, Successes: 0},
		"data.x.added":   {Evals: 3, Successes: 3},
	}}

	exp := &ProfileDiff{
		Added:   map[string]*RuleStat{"data.x.added": {Evals: 3, Successes: 3}},
		Removed: map[string]*RuleStat{"data.x.removed": {Evals: 1, Successes: 0}},
		Changed: map[string]*RuleStatDelta{"data.x.changed": {EvalsDelta: 3, SuccessesDelta: -1}},
	}
	diff := a.Diff(b)
	if !reflect.DeepEqual(diff, exp) {
		t.Fatalf("expected %+v, got %+v", exp, diff)
	}
	if !diff.HasChanges() {
		t.Fatal("expected changes")
	}

	diff = a.Diff(a.Merge(&EvalProfile{}))
	if diff == nil || diff.Added != nil || diff.Removed != nil || diff.Changed != nil || diff.HasChanges() {
		t.Fatalf("expected empty diff, got %+v", diff)
	}

	exp = &ProfileDiff{Removed: a.Rules}
	if diff := a.Diff(nil); !reflect.DeepEqual(diff, exp) {
		t.Fatalf("expected %+v, got %+v", exp, diff)
	}

	var nilProfile *EvalProfile
	if diff := nilProfile.Diff(b); diff != nil {
		t.Fatalf("expected nil diff, got %+v", diff)
	}
	var nilDiff *ProfileDiff
	if nilDiff.HasChanges() {
		t.Fatal("expected nil diff to have no changes")
	}
}

func TestEvalProfileEqual(t *testing.T) {
	var nilProfile *EvalProfile

	tests := []struct {
		note string
		a, b *EvalProfile
		exp  bool
	}{
		{"same", testProfile(), testProfile(), true},
		{"different counts", testProfile(), testProfile().Merge(&EvalProfile{Rules: map[string]*RuleStat{"data.authz.allow": {Evals: 1}}}), false},
		{"different rules", testProfile(), testProfile().FilterByPackage("data.authz"), false},
		{"empty", &EvalProfile{}, &EvalProfile{Rules: map[string]*RuleStat{}}, true},
		{"both nil", nilProfile, nilProfile, true},
		{"nil receiver", nilProfile, &EvalProfile{}, false},
		{"nil other", &EvalProfile{}, nilProfile, false},
	}

	for _, tc := range tests {
		if act := tc.a.Equal(tc.b); act != tc.exp {
			t.Errorf("%s: expected %v, got %v", tc.note, tc.exp, act)
		}
	}
}

func TestEvalProfileNilReceiver(t *testing.T) {
	var p *EvalProfile

	if p.Stat("data.x.p") != nil {
		t.Error("Stat: expected nil")
	}
	if p.ContainsRule("data.x.p") {
		t.Error("ContainsRule: expected false")
	}
	if p.SuccessRate("data.x.p") != 0 || p.OverallSuccessRate() != 0 {
		t.Error("expected zero success rates")
	}
	for note, act := range map[string][]string{
		"RulePaths":      p.RulePaths(),
		"HotRules":       p.HotRules(0),
		"FailedRules":    p.FailedRules(),
		"SucceededRules": p.SucceededRules(),
		"Packages":       p.Packages(),
	} {
		if act != nil {
			t.Errorf("%s: expected nil, got %q", note, act)
		}
	}
	if p.PackageStats() != nil {
		t.Error("PackageStats: expected nil")
	}
	if p.FilterByPackage("data.x") != nil {
		t.Error("FilterByPackage: expected nil")
	}
	if exp, act := "profile: disabled", p.Summary(); act != exp {
		t.Errorf("Summary: expected %q, got %q", exp, act)
	}
	if exp, act := "<nil>", p.String(); act != exp {
		t.Errorf("String: expected %q, got %q", exp, act)
	}
}

func TestRuleStat(t *testing.T) {
	var nilStat *RuleStat

	tests := []struct {
		stat *RuleStat
		rate float64
		str  string
	}{
		{&RuleStat{Evals: 4, Successes: 1}, 0.25, "evals=4 successes=1"},
		{&RuleStat{}, 0, "evals=0 successes=0"},
		{nilStat, 0, "<nil>"},
	}

	for _, tc := range tests {
		if act := tc.stat.SuccessRate(); act != tc.rate {
			t.Errorf("SuccessRate: expected %v, got %v", tc.rate, act)
		}
		if act := tc.stat.String(); act != tc.str {
			t.Errorf("String: expected %q, got %q", tc.str, act)
		}
	}
}
