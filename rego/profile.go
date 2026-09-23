//go:build profile

package rego

import (
	v1 "github.com/open-policy-agent/opa/v1/rego"
)

// EvalProfile records how often each rule was entered and succeeded during
// an evaluation.
type EvalProfile = v1.EvalProfile

// RuleStat holds the evaluation counts for a single rule path.
type RuleStat = v1.RuleStat

// ProfileDiff describes the differences between two profiles.
type ProfileDiff = v1.ProfileDiff

// RuleStatDelta holds the change in counts for a rule present in both
// profiles of a diff.
type RuleStatDelta = v1.RuleStatDelta

// EvalRuleProfile enables or disables rule profiling for a Prepared Query's
// evaluation.
func EvalRuleProfile(enabled bool) EvalOption {
	return v1.EvalRuleProfile(enabled)
}

// EnableRuleProfile enables or disables rule profiling by default for
// evaluations of this Rego object.
func EnableRuleProfile(enabled bool) func(r *Rego) {
	return v1.EnableRuleProfile(enabled)
}
