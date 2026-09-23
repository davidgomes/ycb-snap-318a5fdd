// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

//go:build !profile

package rego

import "github.com/open-policy-agent/opa/v1/topdown"

// ruleProfileResult contributes no fields unless OPA is built with the
// "profile" tag.
type ruleProfileResult struct{} //nolint:unused

// ruleProfileConfig contributes no fields unless OPA is built with the
// "profile" tag.
type ruleProfileConfig struct{} //nolint:unused

func applyStoredRuleProfile(*EvalContext, *Rego) {}

// ruleProfiler is a placeholder so evaluation can share one code path.
// Profiling is compiled in with the "profile" build tag.
type ruleProfiler struct{}

func beginRuleProfile(*EvalContext) *ruleProfiler { return nil }

func attachRuleProfiler(q *topdown.Query, _ *ruleProfiler) *topdown.Query { return q }

func finishRuleProfile(rs ResultSet, _ *ruleProfiler) ResultSet {
	if len(rs) == 0 {
		return nil
	}
	return rs
}
