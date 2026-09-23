//go:build profile

package rego

import "github.com/open-policy-agent/opa/v1/topdown"

func applyRuleProfileImpl(q *topdown.Query, ectx *EvalContext) (*topdown.Query, *EvalProfile) {
	if ectx == nil || !ectx.ruleProfile || q == nil {
		return q, nil
	}
	p := newEvalProfile()
	return q.WithRuleProfile(profileRecorder{p: p}), p
}
