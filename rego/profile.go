// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import v1 "github.com/open-policy-agent/opa/v1/rego"

// EvalProfile maps fully qualified rule paths to evaluation statistics.
type EvalProfile = v1.EvalProfile

// RuleStat counts how many times a rule was entered and how many of those
// entries succeeded.
type RuleStat = v1.RuleStat

// ProfileDiff is the result of comparing two evaluation profiles.
type ProfileDiff = v1.ProfileDiff

// RuleStatDelta is the change in counts between two profiles.
type RuleStatDelta = v1.RuleStatDelta

// EvalRuleProfile enables or disables rule evaluation profiling for a single
// prepared-query evaluation.
func EvalRuleProfile(enabled bool) EvalOption {
	return v1.EvalRuleProfile(enabled)
}

// EnableRuleProfile enables or disables rule evaluation profiling for
// evaluations created from the Rego object.
func EnableRuleProfile(enabled bool) func(*Rego) {
	return v1.EnableRuleProfile(enabled)
}
