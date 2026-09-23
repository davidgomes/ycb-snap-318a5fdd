// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

//go:build profile

package topdown

import "github.com/open-policy-agent/opa/v1/ast"

// WithRuleProfile sets the callback invoked when a rule definition is entered
// and when that entry succeeds. success is false for an entry and true for a
// success. A nil callback disables profiling for the query.
func (q *Query) WithRuleProfile(fn func(path string, success bool)) *Query {
	q.ruleProfile = fn
	return q
}

func (e *eval) setRuleProfile(fn func(path string, success bool)) {
	e.ruleProfile = fn
}

func (e *eval) profileEnter(n ast.Node) {
	if e.ruleProfile == nil {
		return
	}
	rule, ok := n.(*ast.Rule)
	if !ok {
		return
	}
	path, ok := ruleProfilePath(rule)
	if !ok {
		return
	}
	e.profileRuleSucceeded = false
	e.ruleProfile(path, false)
}

func (e *eval) profileExit(n ast.Node) {
	if e.ruleProfile == nil || e.profileRuleSucceeded {
		return
	}
	rule, ok := n.(*ast.Rule)
	if !ok {
		return
	}
	path, ok := ruleProfilePath(rule)
	if !ok {
		return
	}
	e.profileRuleSucceeded = true
	e.ruleProfile(path, true)
}

func ruleProfilePath(rule *ast.Rule) (string, bool) {
	if rule == nil || rule.Head == nil || rule.Module == nil || rule.Module.Package == nil {
		return "", false
	}
	ref := rule.Module.Package.Path.Extend(rule.Head.Ref().GroundPrefix())
	if len(ref) == 0 {
		return "", false
	}
	return ref.String(), true
}
