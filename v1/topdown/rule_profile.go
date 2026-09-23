// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

//go:build profile

package topdown

import "github.com/open-policy-agent/opa/v1/ast"

// ruleProfileHooks receives rule enter, success, and leave events.
// It is embedded at the start of eval and Query so the empty form compiled
// without the profile tag does not change their size.
type ruleProfileHooks struct {
	ruleEnter   func(string)
	ruleSucceed func(string)
	ruleLeave   func(string)
}

// WithRuleProfiler sets callbacks invoked when a rule is entered, succeeds,
// and finished. Nil callbacks are ignored.
func (q *Query) WithRuleProfiler(enter, succeed, leave func(string)) *Query {
	q.ruleEnter = enter
	q.ruleSucceed = succeed
	q.ruleLeave = leave
	return q
}

func (q *Query) applyRuleProfiler(e *eval) {
	e.ruleEnter = q.ruleEnter
	e.ruleSucceed = q.ruleSucceed
	e.ruleLeave = q.ruleLeave
}

func (e *eval) profileEnterRule(rule *ast.Rule) {
	if e.ruleEnter == nil {
		return
	}
	if path := ruleProfilePath(rule); path != "" {
		e.ruleEnter(path)
	}
}

func (e *eval) profileSucceedRule(rule *ast.Rule) {
	if e.ruleSucceed == nil {
		return
	}
	if path := ruleProfilePath(rule); path != "" {
		e.ruleSucceed(path)
	}
}

func (e *eval) profileLeaveRule(rule *ast.Rule) {
	if e.ruleLeave == nil {
		return
	}
	if path := ruleProfilePath(rule); path != "" {
		e.ruleLeave(path)
	}
}

func ruleProfilePath(rule *ast.Rule) string {
	if rule == nil || rule.Head == nil || rule.Module == nil || rule.Module.Package == nil {
		return ""
	}
	// Same document path as (*ast.Rule).Path, without calling the deprecated helper.
	return rule.Module.Package.Path.Extend(rule.Head.Ref().GroundPrefix()).String()
}
