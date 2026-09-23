//go:build profile

package rego

import (
	v1 "github.com/open-policy-agent/opa/v1/rego"
)

// EvalRuleProfile enables or disables rule evaluation profiling for a
// Prepared Query's evaluation.
func EvalRuleProfile(enabled bool) EvalOption {
	return v1.EvalRuleProfile(enabled)
}

// EnableRuleProfile enables or disables rule evaluation profiling for all
// evaluations performed by r.
func EnableRuleProfile(enabled bool) func(r *Rego) {
	return v1.EnableRuleProfile(enabled)
}
