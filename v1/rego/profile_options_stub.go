// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

//go:build !profile

package rego

// EvalRuleProfile enables or disables rule evaluation profiling for a single
// prepared-query evaluation. The collected counts are returned on Result.Profile.
// This option is effective when OPA is built with the "profile" build tag.
func EvalRuleProfile(bool) EvalOption {
	return func(*EvalContext) {}
}

// EnableRuleProfile enables or disables rule evaluation profiling for
// evaluations created from the Rego object. Prepared evaluations inherit this
// setting unless EvalRuleProfile overrides it.
// This option is effective when OPA is built with the "profile" build tag.
func EnableRuleProfile(bool) func(*Rego) {
	return func(*Rego) {}
}
