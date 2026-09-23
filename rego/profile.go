// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

//go:build profile

package rego

import v1 "github.com/open-policy-agent/opa/v1/rego"

// EvalProfile maps each fully qualified rule path to evaluation counts.
type EvalProfile = v1.EvalProfile

// RuleStat counts how many times a rule was entered and how many of those
// entries produced a value.
type RuleStat = v1.RuleStat

// ProfileDiff is the result of comparing two evaluation profiles.
type ProfileDiff = v1.ProfileDiff

// RuleStatDelta is the change in counts from one profile to another.
type RuleStatDelta = v1.RuleStatDelta

// EvalRuleProfile turns rule-evaluation profiling on or off for one evaluation.
func EvalRuleProfile(yes bool) EvalOption {
	return v1.EvalRuleProfile(yes)
}

// EnableRuleProfile turns rule-evaluation profiling on or off for queries
// prepared from this Rego object.
func EnableRuleProfile(yes bool) func(*Rego) {
	return v1.EnableRuleProfile(yes)
}
