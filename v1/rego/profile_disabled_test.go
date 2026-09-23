// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

//go:build !profile

package rego

import (
	"context"
	"testing"
)

func TestRuleProfileIgnoredWithoutBuildTag(t *testing.T) {
	rs, err := New(
		Query("data.authz.allow"),
		Module("authz.rego", "package authz\n\nallow if true\n"),
		EnableRuleProfile(true),
	).Eval(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Profile != nil {
		t.Fatalf("profile must stay nil without the profile build tag: %+v", rs)
	}
}
