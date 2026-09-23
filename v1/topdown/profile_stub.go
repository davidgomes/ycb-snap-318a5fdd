// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

//go:build !profile

package topdown

import "github.com/open-policy-agent/opa/v1/ast"

// WithRuleProfile sets the callback invoked when a rule definition is entered
// and when that entry succeeds. Rule profiling is compiled in with the
// "profile" build tag; without that tag the callback is ignored.
func (q *Query) WithRuleProfile(func(string, bool)) *Query {
	return q
}

func (e *eval) setRuleProfile(func(string, bool)) {
	e.ruleProfile = nil
	e.profileRuleSucceeded = false
}

func (*eval) profileEnter(ast.Node) {}

func (*eval) profileExit(ast.Node) {}
