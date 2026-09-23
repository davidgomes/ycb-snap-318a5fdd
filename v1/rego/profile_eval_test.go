// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

//go:build profile

package rego

import "testing"

func TestEvalRuleProfileRecordsEnteredRules(t *testing.T) {
	module := `package authz

allow if input.role == "guest"

allow if input.role == "admin"

deny if input.blocked

user_is_admin if input.role == "admin"
`
	ctx := t.Context()
	rs, err := New(
		Query("data.authz.allow"),
		Module("authz.rego", module),
		Input(map[string]any{"role": "admin", "blocked": true}),
		EnableRuleProfile(true),
	).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Profile == nil {
		t.Fatalf("expected a profiled result, got %#v", rs)
	}
	prof := rs[0].Profile
	// Rule indexing tries only the definition that can match this input.
	allow := prof.Stat("data.authz.allow")
	if allow == nil || allow.Evals != 1 || allow.Successes != 1 {
		t.Fatalf("allow stat %+v\n%s", allow, prof)
	}
	pq, err := New(
		Query("data.authz.allow"),
		Module("authz.rego", module),
		EnableRuleProfile(true),
	).PrepareForEval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rs, err = pq.Eval(ctx, EvalRuleIndexing(false), EvalInput(map[string]any{"role": "admin", "blocked": true}))
	if err != nil {
		t.Fatal(err)
	}
	unindexed := rs[0].Profile
	allow = unindexed.Stat("data.authz.allow")
	if allow == nil || allow.Evals != 2 || allow.Successes != 1 {
		t.Fatalf("allow without indexing %+v\n%s", allow, unindexed)
	}
	if unindexed.SuccessRate("data.authz.allow") != 0.5 {
		t.Fatalf("rate %v", unindexed.SuccessRate("data.authz.allow"))
	}
	// deny is not referenced by the query, so it is not entered.
	if prof.ContainsRule("data.authz.deny") || unindexed.ContainsRule("data.authz.deny") {
		t.Fatalf("unreferenced rule was recorded\n%s", prof)
	}
	admin := prof.Stat("data.authz.user_is_admin")
	if admin != nil {
		t.Fatalf("user_is_admin is not called by allow, got %+v", admin)
	}
	if !sameStrings(prof.Packages(), []string{"data.authz"}) {
		t.Fatalf("packages %v", prof.Packages())
	}

	// A failing helper referenced by the query is recorded.
	rs, err = New(
		Query("data.authz.allow"),
		Module("authz.rego", `package authz
allow if user_is_admin
user_is_admin if input.admin
`),
		Input(map[string]any{"admin": false}),
		EnableRuleProfile(true),
	).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 0 {
		t.Fatalf("undefined query returned %#v", rs)
	}

	rs, err = New(
		Query("not data.authz.allow"),
		Module("authz.rego", `package authz
allow if user_is_admin
user_is_admin if input.admin
`),
		Input(map[string]any{"admin": false}),
		EnableRuleProfile(true),
	).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 {
		t.Fatalf("expected defined negation, got %#v", rs)
	}
	prof = rs[0].Profile
	if st := prof.Stat("data.authz.allow"); st == nil || st.Evals != 1 || st.Successes != 0 {
		t.Fatalf("allow %+v\n%s", st, prof)
	}
	if st := prof.Stat("data.authz.user_is_admin"); st == nil || st.Evals != 1 || st.Successes != 0 {
		t.Fatalf("helper %+v\n%s", st, prof)
	}
	if failed := prof.FailedRules(); !sameStrings(failed, []string{"data.authz.allow", "data.authz.user_is_admin"}) {
		t.Fatalf("failed %v\n%s", failed, prof)
	}
	if prof.SucceededRules() != nil {
		t.Fatalf("succeeded %v", prof.SucceededRules())
	}

	// Every definition is entered when early exit does not skip it, including
	// a definition that fails.
	rs, err = New(
		Query("data.authz.p"),
		Module("authz.rego", `package authz
p if false
p if true
`),
		EnableRuleProfile(true),
	).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st := rs[0].Profile.Stat("data.authz.p"); st == nil || st.Evals != 2 || st.Successes != 1 {
		t.Fatalf("multi-def %+v\n%s", st, rs[0].Profile)
	}
}

func TestEvalRuleProfileEarlyExitAndOptions(t *testing.T) {
	ctx := t.Context()
	module := `package authz
p if true
p if true
good if true
bad if false
f(x) := x if x > 0
f(x) := 0 if { x <= 0 }
items contains x if { some x in [1, 2, 3] }
`

	rs, err := New(
		Query("data.authz.p"),
		Module("authz.rego", module),
		EnableRuleProfile(true),
	).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st := rs[0].Profile.Stat("data.authz.p"); st == nil || st.Evals != 1 || st.Successes != 1 {
		t.Fatalf("early exit %+v\n%s", st, rs[0].Profile)
	}

	pq, err := New(
		Query("data.authz.p"),
		Module("authz.rego", module),
		EnableRuleProfile(true),
	).PrepareForEval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rs, err = pq.Eval(ctx, EvalEarlyExit(false))
	if err != nil {
		t.Fatal(err)
	}
	if st := rs[0].Profile.Stat("data.authz.p"); st == nil || st.Evals != 2 || st.Successes != 2 {
		t.Fatalf("early exit disabled %+v\n%s", st, rs[0].Profile)
	}
	if rs[0].Profile.Summary() == "profile: disabled" {
		t.Fatal("summary")
	}

	rs, err = pq.Eval(ctx, EvalRuleProfile(false))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Profile != nil {
		t.Fatal("EvalRuleProfile(false) should override the constructor")
	}

	rs, err = New(
		Query("data.authz.p"),
		Module("authz.rego", module),
	).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rs[0].Profile != nil {
		t.Fatal("profiling disabled by default")
	}

	prepared, err := New(
		Query("data.authz.good"),
		Module("authz.rego", module),
	).PrepareForEval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rs, err = prepared.Eval(ctx, EvalRuleProfile(true))
	if err != nil {
		t.Fatal(err)
	}
	prof := rs[0].Profile
	if st := prof.Stat("data.authz.good"); st == nil || st.Evals != 1 || st.Successes != 1 {
		t.Fatalf("good %+v\n%s", st, prof)
	}
	if prof.ContainsRule("data.authz.bad") {
		t.Fatalf("bad should not be entered\n%s", prof)
	}

	rs, err = New(
		Query("data.authz"),
		Module("authz.rego", module),
		EnableRuleProfile(true),
	).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prof = rs[0].Profile
	if st := prof.Stat("data.authz.bad"); st == nil || st.Evals < 1 || st.Successes != 0 {
		t.Fatalf("bad %+v\n%s", st, prof)
	}
	if !prof.ContainsRule("data.authz.good") {
		t.Fatalf("missing good\n%s", prof)
	}

	rs, err = New(
		Query("data.authz.f(-1)"),
		Module("authz.rego", module),
		EnableRuleProfile(true),
	).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 {
		t.Fatalf("function results %#v", rs)
	}
	if st := rs[0].Profile.Stat("data.authz.f"); st == nil || st.Evals != 2 || st.Successes != 1 {
		t.Fatalf("function %+v\n%s", st, rs[0].Profile)
	}

	rs, err = New(
		Query("data.authz.items[x]"),
		Module("authz.rego", module),
		EnableRuleProfile(true),
	).Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) < 2 {
		t.Fatalf("expected multiple results, got %d\n%s", len(rs), rs[0].Profile)
	}
	if rs[0].Profile == nil || rs[0].Profile != rs[1].Profile {
		t.Fatal("results should share one profile")
	}
	if st := rs[0].Profile.Stat("data.authz.items"); st == nil || st.Evals != 1 || st.Successes != 1 {
		t.Fatalf("partial rule %+v\n%s", st, rs[0].Profile)
	}
}

func TestEvalRuleProfileDisabledOption(t *testing.T) {
	rs, err := New(
		Query("data.authz.allow"),
		Module("authz.rego", "package authz\nallow if true\n"),
		EnableRuleProfile(false),
	).Eval(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Profile != nil {
		t.Fatalf("got %#v", rs)
	}
}
