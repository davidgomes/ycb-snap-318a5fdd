// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import "testing"

func TestRuleProfileAPI(t *testing.T) {
	assertEvalOption(EvalRuleProfile)
	assertRegoOption(EnableRuleProfile)

	rs, err := New(
		Query("data.authz.allow"),
		Module("authz.rego", "package authz\nallow { true }\n"),
	).Eval(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Profile != nil {
		t.Fatalf("profiling is disabled unless requested, got %#v", rs)
	}
}

func assertEvalOption(func(bool) EvalOption) {}

func assertRegoOption(func(bool) func(*Rego)) {}
