// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import (
	"github.com/open-policy-agent/opa/v1/ast"
)

// reconstructPartialTemplateStrings rewrites residual queries and support
// modules so that compiler-lowered internal.template_string calls are
// presented as ordinary template-string syntax.
func reconstructPartialTemplateStrings(pq *PartialQueries) {
	if pq == nil {
		return
	}

	for i := range pq.Queries {
		pq.Queries[i] = reconstructTemplateStringQuery(pq.Queries[i])
	}

	for i := range pq.Support {
		pq.Support[i] = reconstructTemplateStringModule(pq.Support[i])
	}
}

func reconstructTemplateStringQuery(body ast.Body) ast.Body {
	result, _ := reconstructTemplateStringBody(body, templateStringElideGeneratedBindings)
	return result
}

func reconstructTemplateStringModule(module *ast.Module) *ast.Module {
	if module == nil {
		return nil
	}

	cpy := module.Copy()
	ast.WalkRules(cpy, func(rule *ast.Rule) bool {
		reconstructTemplateStringRuleInPlace(rule)
		return false
	})
	return cpy
}

func reconstructTemplateStringRuleInPlace(rule *ast.Rule) {
	if rule == nil {
		return
	}

	body, env := reconstructTemplateStringBody(rule.Body, templateStringElideGeneratedBindings)
	if len(body) == 0 {
		body = ast.NewBody(ast.NewExpr(ast.BooleanTerm(true)))
	}
	rule.Body = body

	if rule.Head == nil {
		return
	}

	if rule.Head.Key != nil {
		rule.Head.Key = reconstructTemplateStringTerm(rule.Head.Key, env)
	}
	if rule.Head.Value != nil {
		rule.Head.Value = reconstructTemplateStringTerm(rule.Head.Value, env)
	}
	for i := range rule.Head.Args {
		rule.Head.Args[i] = reconstructTemplateStringTerm(rule.Head.Args[i], env)
	}
}

type templateStringRewriteMode int

const (
	templateStringPreserveBindings templateStringRewriteMode = iota
	templateStringElideGeneratedBindings
)

type templateStringEnv map[ast.Var]*ast.Expr

func (e templateStringEnv) copy() templateStringEnv {
	if len(e) == 0 {
		return templateStringEnv{}
	}

	cpy := make(templateStringEnv, len(e))
	for k, v := range e {
		cpy[k] = v.Copy()
	}
	return cpy
}

func (e templateStringEnv) lookup(v ast.Var) (*ast.Expr, bool) {
	if e == nil {
		return nil, false
	}
	expr, ok := e[v]
	return expr, ok
}

func (e templateStringEnv) merge(other templateStringEnv) templateStringEnv {
	switch {
	case len(other) == 0:
		return e
	case len(e) == 0:
		return other
	}

	cpy := e.copy()
	for k, v := range other {
		cpy[k] = v
	}
	return cpy
}

type templateStringBinding struct {
	varName ast.Var
	expr    *ast.Expr
}

func reconstructTemplateStringBody(body ast.Body, mode templateStringRewriteMode) (ast.Body, templateStringEnv) {
	if len(body) == 0 {
		return nil, templateStringEnv{}
	}

	env := templateStringEnv{}
	result := make(ast.Body, 0, len(body))

	for i := range body {
		expr := reconstructTemplateStringExpr(body[i], env, mode)
		if expr == nil {
			continue
		}

		if rewritten, ok := rewriteStandaloneTemplateStringCall(expr, env); ok {
			expr = rewritten
		}

		binding, ok := templateStringGeneratedBinding(expr, body[i+1:], env)
		if ok && mode == templateStringElideGeneratedBindings && canElideTemplateStringBinding(binding, body[i+1:]) {
			env[binding.varName] = binding.expr
			continue
		}

		if replacement, ok := templateStringCallAssignment(expr, env); ok {
			result = append(result, replacement)
			continue
		}

		result = append(result, expr)
	}

	for i, expr := range result {
		expr.Index = i
	}

	return result, env
}

func reconstructTemplateStringExpr(expr *ast.Expr, env templateStringEnv, mode templateStringRewriteMode) *ast.Expr {
	if expr == nil {
		return nil
	}

	cpy := expr.Copy()
	for i := range cpy.With {
		cpy.With[i].Target = reconstructTemplateStringTerm(cpy.With[i].Target, env)
		cpy.With[i].Value = reconstructTemplateStringTerm(cpy.With[i].Value, env)
	}

	switch terms := cpy.Terms.(type) {
	case *ast.Term:
		cpy.Terms = reconstructTemplateStringTerm(terms, env)
	case []*ast.Term:
		for i := range terms {
			terms[i] = reconstructTemplateStringTerm(terms[i], env)
		}
		cpy.Terms = terms
	case *ast.Every:
		if terms.Key != nil {
			terms.Key = reconstructTemplateStringTerm(terms.Key, env)
		}
		terms.Value = reconstructTemplateStringTerm(terms.Value, env)
		terms.Domain = reconstructTemplateStringTerm(terms.Domain, env)
		terms.Body, _ = reconstructTemplateStringBody(terms.Body, mode)
		if len(terms.Body) == 0 {
			terms.Body = ast.NewBody(ast.NewExpr(ast.BooleanTerm(true)))
		}
		cpy.Terms = terms
	case *ast.SomeDecl:
		for i := range terms.Symbols {
			terms.Symbols[i] = reconstructTemplateStringTerm(terms.Symbols[i], env)
		}
		cpy.Terms = terms
	}

	return cpy
}

func reconstructTemplateStringTerm(term *ast.Term, env templateStringEnv) *ast.Term {
	if term == nil {
		return nil
	}

	if v, ok := term.Value.(ast.Var); ok {
		if expr, ok := env.lookup(v); ok {
			if t := exprToTerm(expr); t != nil {
				return reconstructTemplateStringTerm(t, env)
			}
		}
	}

	cpy := term.Copy()
	switch value := cpy.Value.(type) {
	case ast.Ref:
		for i := range value {
			value[i] = reconstructTemplateStringTerm(value[i], env)
		}
		cpy.Value = value
	case ast.Call:
		if isInternalTemplateStringCall(value) && len(value) >= 2 {
			if template, ok := reconstructTemplateStringTermFromArray(value[1], env); ok {
				return template
			}
		}
		for i := range value {
			value[i] = reconstructTemplateStringTerm(value[i], env)
		}
		cpy.Value = value
	case *ast.Array:
		for i := range value.Len() {
			value.Set(i, reconstructTemplateStringTerm(value.Elem(i), env))
		}
	case ast.Set:
		next := ast.NewSet()
		value.Foreach(func(t *ast.Term) {
			next.Add(reconstructTemplateStringTerm(t, env))
		})
		cpy.Value = next
	case ast.Object:
		next := ast.NewObjectWithCapacity(value.Len())
		value.Foreach(func(k, v *ast.Term) {
			next.Insert(reconstructTemplateStringTerm(k, env), reconstructTemplateStringTerm(v, env))
		})
		cpy.Value = next
	case *ast.ArrayComprehension:
		body, bodyEnv := reconstructTemplateStringBody(value.Body, templateStringElideGeneratedBindings)
		if len(body) == 0 {
			body = ast.NewBody(ast.NewExpr(ast.BooleanTerm(true)))
		}
		value.Body = body
		value.Term = reconstructTemplateStringTerm(value.Term, env.merge(bodyEnv))
	case *ast.SetComprehension:
		body, bodyEnv := reconstructTemplateStringBody(value.Body, templateStringElideGeneratedBindings)
		if len(body) == 0 {
			body = ast.NewBody(ast.NewExpr(ast.BooleanTerm(true)))
		}
		value.Body = body
		value.Term = reconstructTemplateStringTerm(value.Term, env.merge(bodyEnv))
	case *ast.ObjectComprehension:
		body, bodyEnv := reconstructTemplateStringBody(value.Body, templateStringElideGeneratedBindings)
		if len(body) == 0 {
			body = ast.NewBody(ast.NewExpr(ast.BooleanTerm(true)))
		}
		merged := env.merge(bodyEnv)
		value.Body = body
		value.Key = reconstructTemplateStringTerm(value.Key, merged)
		value.Value = reconstructTemplateStringTerm(value.Value, merged)
	case *ast.TemplateString:
		for i, part := range value.Parts {
			switch p := part.(type) {
			case *ast.Term:
				value.Parts[i] = reconstructTemplateStringTerm(p, env)
			case *ast.Expr:
				value.Parts[i] = reconstructTemplateStringExpr(p, env, templateStringPreserveBindings)
			}
		}
	}

	return cpy
}

func reconstructTemplateStringTermFromArray(term *ast.Term, env templateStringEnv) (*ast.Term, bool) {
	if term == nil {
		return nil, false
	}

	array, ok := term.Value.(*ast.Array)
	if !ok {
		return nil, false
	}

	parts := make([]ast.Node, 0, array.Len())
	for i := range array.Len() {
		part, ok := reconstructTemplateStringPart(array.Elem(i), env)
		if !ok {
			return nil, false
		}
		parts = append(parts, part)
	}

	template := ast.TemplateStringTerm(false, parts...)
	template.Location = term.Location
	return template, true
}

func reconstructTemplateStringPart(term *ast.Term, env templateStringEnv) (ast.Node, bool) {
	if term == nil {
		return nil, false
	}

	rewritten := reconstructTemplateStringTerm(term, env)
	if _, ok := rewritten.Value.(ast.String); ok {
		return rewritten, true
	}

	expr, ok := decodeTemplateStringExprFromTerm(rewritten, env)
	if !ok {
		return nil, false
	}
	return expr, true
}

func templateStringGeneratedBinding(expr *ast.Expr, rest ast.Body, env templateStringEnv) (templateStringBinding, bool) {
	if expr == nil {
		return templateStringBinding{}, false
	}

	if binding, ok := templateStringEqualityBinding(expr, rest, env); ok {
		return binding, true
	}

	return templateStringCallBinding(expr, env)
}

func templateStringEqualityBinding(expr *ast.Expr, rest ast.Body, env templateStringEnv) (templateStringBinding, bool) {
	if !expr.IsEquality() && !expr.IsAssignment() {
		return templateStringBinding{}, false
	}

	ops := expr.Operands()
	if len(ops) != 2 {
		return templateStringBinding{}, false
	}

	try := func(v ast.Var, value *ast.Term) (templateStringBinding, bool) {
		if !v.IsGenerated() {
			return templateStringBinding{}, false
		}
		if !isTemplateStringWrapperTerm(value, env) {
			return templateStringBinding{}, false
		}
		if countTemplateStringVarUses(rest, v) > 1 {
			return templateStringBinding{}, false
		}

		decoded, ok := decodeTemplateStringExprFromTerm(value, env)
		if !ok {
			return templateStringBinding{}, false
		}
		decoded.With = append(cloneExprWith(expr.With), cloneExprWith(decoded.With)...)
		decoded.Location = expr.Location
		return templateStringBinding{varName: v, expr: decoded}, true
	}

	if v, ok := ops[0].Value.(ast.Var); ok {
		if binding, ok := try(v, ops[1]); ok {
			return binding, true
		}
	}
	if v, ok := ops[1].Value.(ast.Var); ok {
		if binding, ok := try(v, ops[0]); ok {
			return binding, true
		}
	}

	return templateStringBinding{}, false
}

func templateStringCallBinding(expr *ast.Expr, env templateStringEnv) (templateStringBinding, bool) {
	terms, ok := expr.Terms.([]*ast.Term)
	if !ok || len(terms) != 3 || !isInternalTemplateStringTerm(terms[0]) {
		return templateStringBinding{}, false
	}

	outVar, ok := terms[2].Value.(ast.Var)
	if !ok {
		return templateStringBinding{}, false
	}

	template, ok := reconstructTemplateStringTermFromArray(terms[1], env)
	if !ok {
		return templateStringBinding{}, false
	}

	decoded := ast.NewExpr(template)
	decoded.With = cloneExprWith(expr.With)
	decoded.Location = expr.Location
	return templateStringBinding{varName: outVar, expr: decoded}, true
}

func templateStringCallAssignment(expr *ast.Expr, env templateStringEnv) (*ast.Expr, bool) {
	binding, ok := templateStringCallBinding(expr, env)
	if !ok {
		return nil, false
	}

	term := exprToTerm(binding.expr)
	if term == nil {
		return nil, false
	}

	replacement := ast.Equality.Expr(ast.NewTerm(binding.varName), term)
	replacement.With = cloneExprWith(binding.expr.With)
	replacement.Location = expr.Location
	replacement.Generated = expr.Generated
	return replacement, true
}

func rewriteStandaloneTemplateStringCall(expr *ast.Expr, env templateStringEnv) (*ast.Expr, bool) {
	terms, ok := expr.Terms.([]*ast.Term)
	if !ok || len(terms) != 2 || !isInternalTemplateStringTerm(terms[0]) {
		return nil, false
	}

	template, ok := reconstructTemplateStringTermFromArray(terms[1], env)
	if !ok {
		return nil, false
	}

	rewritten := ast.NewExpr(template)
	rewritten.With = cloneExprWith(expr.With)
	rewritten.Location = expr.Location
	rewritten.Generated = expr.Generated
	return rewritten, true
}

func canElideTemplateStringBinding(binding templateStringBinding, rest ast.Body) bool {
	if !binding.varName.IsGenerated() {
		return false
	}
	return countTemplateStringVarUses(rest, binding.varName) <= 1
}

func isTemplateStringWrapperTerm(term *ast.Term, env templateStringEnv) bool {
	if term == nil {
		return false
	}

	switch value := term.Value.(type) {
	case ast.Set:
		return value.Len() == 1
	case *ast.SetComprehension:
		return true
	case ast.Var:
		_, ok := env.lookup(value)
		return ok && value.IsGenerated()
	default:
		return false
	}
}

func decodeTemplateStringExprFromTerm(term *ast.Term, env templateStringEnv) (*ast.Expr, bool) {
	if term == nil {
		return nil, false
	}

	switch value := term.Value.(type) {
	case ast.Var:
		if expr, ok := env.lookup(value); ok {
			return expr.Copy(), true
		}
		return ast.NewExpr(term.Copy()), true
	case ast.Set:
		if value.Len() == 1 {
			return decodeTemplateStringExprFromTerm(value.Slice()[0], env)
		}
	case *ast.SetComprehension:
		if expr, ok := unwrapTemplateSetComprehension(value, env); ok {
			return expr, true
		}
	case ast.Call:
		if isInternalTemplateStringCall(value) && len(value) >= 2 {
			if template, ok := reconstructTemplateStringTermFromArray(value[1], env); ok {
				return ast.NewExpr(template), true
			}
		}
	}

	rewritten := reconstructTemplateStringTerm(term, env)
	return ast.NewExpr(rewritten), true
}

func unwrapTemplateSetComprehension(sc *ast.SetComprehension, env templateStringEnv) (*ast.Expr, bool) {
	termVar, ok := sc.Term.Value.(ast.Var)
	if !ok {
		return nil, false
	}

	body, bodyEnv := reconstructTemplateStringBody(sc.Body.Copy(), templateStringElideGeneratedBindings)
	merged := env.merge(bodyEnv)

	if expr, ok := merged.lookup(termVar); ok && len(body) == 0 {
		return expr.Copy(), true
	}

	if len(body) == 1 {
		expr := body[0]
		if expr.IsEquality() || expr.IsAssignment() {
			ops := expr.Operands()
			if len(ops) == 2 {
				if v, ok := ops[0].Value.(ast.Var); ok && v.Equal(termVar) {
					if decoded, ok := decodeTemplateStringExprFromTerm(ops[1], merged); ok {
						decoded.With = append(cloneExprWith(expr.With), cloneExprWith(decoded.With)...)
						return decoded, true
					}
				}
				if v, ok := ops[1].Value.(ast.Var); ok && v.Equal(termVar) {
					if decoded, ok := decodeTemplateStringExprFromTerm(ops[0], merged); ok {
						decoded.With = append(cloneExprWith(expr.With), cloneExprWith(decoded.With)...)
						return decoded, true
					}
				}
			}
		}

		if stripped, ok := stripGeneratedCallOutput(expr, termVar); ok {
			return stripped, true
		}
	}

	if expr, ok := merged.lookup(termVar); ok {
		if t, ok := expr.Terms.(*ast.Term); ok {
			if outVar, ok := t.Value.(ast.Var); ok && len(body) > 0 {
				if stripped, ok := stripGeneratedCallOutput(body[len(body)-1], outVar); ok {
					return stripped, true
				}
			}
		}
	}

	return nil, false
}

func stripGeneratedCallOutput(expr *ast.Expr, out ast.Var) (*ast.Expr, bool) {
	if expr == nil || !expr.IsCall() {
		return nil, false
	}

	terms, ok := expr.Terms.([]*ast.Term)
	if !ok || len(terms) < 2 {
		return nil, false
	}

	last, ok := terms[len(terms)-1].Value.(ast.Var)
	if !ok || !last.Equal(out) {
		return nil, false
	}

	call := make([]*ast.Term, len(terms)-1)
	copy(call, terms[:len(terms)-1])
	result := ast.NewExpr(call)
	result.With = cloneExprWith(expr.With)
	result.Location = expr.Location
	return result, true
}

func countTemplateStringVarUses(body ast.Body, v ast.Var) int {
	if len(body) == 0 {
		return 0
	}

	n := 0
	ast.WalkVars(body, func(x ast.Var) bool {
		if x.Equal(v) {
			n++
		}
		return false
	})
	return n
}

func isInternalTemplateStringTerm(term *ast.Term) bool {
	if term == nil {
		return false
	}
	ref, ok := term.Value.(ast.Ref)
	return ok && isInternalTemplateStringRef(ref)
}

func isInternalTemplateStringCall(call ast.Call) bool {
	if len(call) == 0 {
		return false
	}
	return isInternalTemplateStringTerm(call[0])
}

func isInternalTemplateStringRef(ref ast.Ref) bool {
	return ref.Equal(ast.InternalTemplateString.Ref())
}

func exprToTerm(expr *ast.Expr) *ast.Term {
	if expr == nil {
		return nil
	}

	switch terms := expr.Terms.(type) {
	case *ast.Term:
		return terms.Copy()
	case []*ast.Term:
		if len(terms) == 0 {
			return nil
		}
		call := make(ast.Call, len(terms))
		for i, t := range terms {
			call[i] = t.Copy()
		}
		return ast.NewTerm(call)
	default:
		return nil
	}
}

func cloneExprWith(withs []*ast.With) []*ast.With {
	if len(withs) == 0 {
		return nil
	}

	cpy := make([]*ast.With, len(withs))
	for i, w := range withs {
		cpy[i] = w.Copy()
	}
	return cpy
}
