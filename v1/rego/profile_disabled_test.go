// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

//go:build !profile

package rego

import "testing"

func TestRuleProfileIgnoredWithoutBuildTag(t *testing.T) {
	rs, err := New(
		Query("data.authz.allow"),
		Module("authz.rego", "package authz\ndefault allow := false\nallow if input.admin\n"),
		Input(map[string]any{"admin": false}),
		EnableRuleProfile(true),
	).Eval(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Profile != nil {
		t.Fatalf("profile must stay nil without the profile tag, got %#v", rs)
	}

	pq, err := New(
		Query("data.authz.allow"),
		Module("authz.rego", "package authz\nallow if true\n"),
	).PrepareForEval(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rs, err = pq.Eval(t.Context(), EvalRuleProfile(true))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Profile != nil {
		t.Fatalf("per-eval profiling must stay nil without the profile tag, got %#v", rs)
	}
}
