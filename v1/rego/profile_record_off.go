//go:build !profile

package rego

import "github.com/open-policy-agent/opa/v1/topdown"

func applyRuleProfileImpl(q *topdown.Query, _ *EvalContext) (*topdown.Query, *EvalProfile) {
	return q, nil
}
