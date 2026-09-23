// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

//go:build profile

package rego

import (
	v1 "github.com/open-policy-agent/opa/v1/rego"
)

// EvalProfile maps fully qualified rule paths to their evaluation counters.
type EvalProfile = v1.EvalProfile

// RuleStat holds evaluation counters for a single rule path.
type RuleStat = v1.RuleStat

// ProfileDiff describes the differences between two profiles.
type ProfileDiff = v1.ProfileDiff

// RuleStatDelta is the difference between two RuleStats.
type RuleStatDelta = v1.RuleStatDelta

// EnableRuleProfile returns an argument that enables rule evaluation profiling.
func EnableRuleProfile(yes bool) func(r *Rego) {
	return v1.EnableRuleProfile(yes)
}

// EvalRuleProfile enables or disables rule evaluation profiling for a
// prepared query's evaluation.
func EvalRuleProfile(yes bool) EvalOption {
	return v1.EvalRuleProfile(yes)
}
