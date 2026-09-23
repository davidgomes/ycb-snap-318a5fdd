//go:build !profile

package rego

import "github.com/open-policy-agent/opa/v1/topdown"

// EvalProfile holds rule evaluation statistics. Rule profiling is only
// available when built with the "profile" build tag; otherwise Result.Profile
// is always nil.
type EvalProfile struct{}

func withRuleProfiler(q *topdown.Query, _ *EvalContext) (*topdown.Query, *EvalProfile) {
	return q, nil
}
