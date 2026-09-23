// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

//go:build !profile

package rego

import "github.com/open-policy-agent/opa/v1/topdown"

// EvalProfile holds rule evaluation statistics. Rule profiling is only
// available in builds using the "profile" build tag; in other builds a
// Result's Profile is always nil.
type EvalProfile struct{}

func withRuleProfile(q *topdown.Query, _ bool) (*topdown.Query, func(ResultSet)) {
	return q, func(ResultSet) {}
}
