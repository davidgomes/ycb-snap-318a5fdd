// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import v1 "github.com/open-policy-agent/opa/v1/rego"

// EvalProfile maps fully qualified rule paths to evaluation statistics.
type EvalProfile = v1.EvalProfile

// RuleStat counts how many times a rule was entered and how many of those
// entries produced a result.
type RuleStat = v1.RuleStat

// ProfileDiff is the difference between two evaluation profiles.
type ProfileDiff = v1.ProfileDiff

// RuleStatDelta is the change in counts from one profile to another.
type RuleStatDelta = v1.RuleStatDelta

// EvalRuleProfile enables or disables per-rule evaluation profiling for one
// evaluation. Profiling is recorded only when OPA is built with the "profile"
// tag.
func EvalRuleProfile(enabled bool) EvalOption {
	return v1.EvalRuleProfile(enabled)
}

// EnableRuleProfile enables per-rule evaluation profiling for evaluations of
// the Rego object. Profiling is recorded only when OPA is built with the
// "profile" tag.
func EnableRuleProfile(enabled bool) func(*Rego) {
	return v1.EnableRuleProfile(enabled)
}
