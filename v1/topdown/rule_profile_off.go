// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

//go:build !profile

package topdown

import "github.com/open-policy-agent/opa/v1/ast"

// ruleProfileHooks is empty unless OPA is built with the profile tag.
type ruleProfileHooks struct{}

// WithRuleProfiler is a no-op unless OPA is built with the profile tag.
func (q *Query) WithRuleProfiler(func(string), func(string), func(string)) *Query {
	return q
}

func (q *Query) applyRuleProfiler(e *eval) {
	e.ruleProfileHooks = q.ruleProfileHooks
}

func (*eval) profileEnterRule(*ast.Rule) {}

func (*eval) profileSucceedRule(*ast.Rule) {}

func (*eval) profileLeaveRule(*ast.Rule) {}
