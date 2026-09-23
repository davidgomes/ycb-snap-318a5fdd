//go:build profile

package rego

// EvalRuleProfile enables or disables rule evaluation profiling for a
// Prepared Query's evaluation. When enabled, each Result carries a Profile.
func EvalRuleProfile(enabled bool) EvalOption {
	return func(e *EvalContext) {
		e.ruleProfile = &enabled
	}
}

// EnableRuleProfile enables or disables rule evaluation profiling for all
// evaluations performed by r.
func EnableRuleProfile(enabled bool) func(r *Rego) {
	return func(r *Rego) {
		r.ruleProfile = enabled
	}
}
