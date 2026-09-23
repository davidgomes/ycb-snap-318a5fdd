// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import (
	"fmt"
	"slices"
	"strings"

	"github.com/open-policy-agent/opa/v1/ast"
)

// reconstructTemplateStrings rewrites calls to internal.template_string found in
// partial evaluation output back into template-string terms. The compiler lowers
// $"a {x}" into internal.template_string(["a ", {__local0__ | __local0__ = x}]),
// and both compilation and partial evaluation introduce generated bindings around
// that call. Where the original template-string can be expressed in Rego source,
// these bindings are folded back into the template-string; otherwise the lowered
// form is left untouched.
//
// arity is used to look up the arity of non-relation functions, and must return
// -1 for anything else.
func reconstructTemplateStrings(pq *PartialQueries, arity func(ast.Ref) int) {
	found := false
	for _, q := range pq.Queries {
		found = found || containsTemplateStringCall(q)
	}
	for _, mod := range pq.Support {
		found = found || containsTemplateStringCall(mod)
	}
	if !found {
		return
	}

	tr := &tsReconstructor{arity: arity, used: ast.NewVarSet()}
	for _, q := range pq.Queries {
		ast.WalkVars(q, tr.markUsed)
	}
	for _, mod := range pq.Support {
		ast.WalkVars(mod, tr.markUsed)
	}

	for i := range pq.Queries {
		if containsTemplateStringCall(pq.Queries[i]) {
			pq.Queries[i] = tr.query(pq.Queries[i].Copy())
		}
	}

	for i, mod := range pq.Support {
		if !containsTemplateStringCall(mod) {
			continue
		}
		cpy := mod.Copy()
		for _, rule := range cpy.Rules {
			for r := rule; r != nil; r = r.Else {
				tr.rule(r)
			}
		}
		pq.Support[i] = cpy
	}
}

type tsReconstructor struct {
	arity func(ast.Ref) int
	used  ast.VarSet
	next  int
}

type tsScope struct {
	// counts holds var occurrences in the rule or query owning the body. They're
	// computed once up front; since reconstruction only ever removes or moves
	// occurrences of generated vars, the counts remain upper bounds.
	counts map[ast.Var]int

	// visible holds vars that may be referenced from the body being processed:
	// vars from enclosing bodies and from the body itself.
	visible ast.VarSet

	// head holds the head nodes of the rule or comprehension owning the body.
	head []any

	// mayEmpty is set when the body may be left without exprs.
	mayEmpty bool
}

func (tr *tsReconstructor) markUsed(v ast.Var) bool {
	tr.used.Add(v)
	return false
}

func (tr *tsReconstructor) fresh() *ast.Term {
	for {
		v := ast.Var(fmt.Sprintf("__localts%d__", tr.next))
		tr.next++
		if !tr.used.Contains(v) {
			tr.used.Add(v)
			return ast.NewTerm(v)
		}
	}
}

func (tr *tsReconstructor) query(body ast.Body) ast.Body {
	return tr.body(body, &tsScope{counts: countVars(body), visible: ast.ReservedVars.Copy()})
}

func (tr *tsReconstructor) rule(rule *ast.Rule) {
	if !containsTemplateStringCall(rule.Body) {
		return
	}
	visible := rule.Head.Args.Vars()
	visible.Update(ast.ReservedVars)
	sc := &tsScope{counts: countVars(rule.Head, rule.Head.Reference, rule.Body), visible: visible, head: []any{rule.Head, rule.Head.Reference}, mayEmpty: true}
	rule.Body = tr.body(rule.Body, sc)
	if len(rule.Body) == 0 {
		rule.Body = ast.NewBody(ast.NewExpr(ast.BooleanTerm(true)))
	}
}

func (tr *tsReconstructor) body(body ast.Body, sc *tsScope) ast.Body {
	visible := sc.visible.Copy()
	visible.Update(topLevelVars(body))
	inner := &tsScope{counts: sc.counts, visible: visible, head: sc.head, mayEmpty: sc.mayEmpty}

	for _, expr := range body {
		tr.closures(expr, visible, sc.counts)
	}

	out := make(ast.Body, 0, len(body))
	var assigned []*ast.Expr

	for _, expr := range body {
		var hoisted []*ast.Expr
		var dropped []*ast.Expr

		ctx := tsContext{preceding: out, with: expr.With, hoist: !expr.Negated}

		reconstruct := func(terms []*ast.Term) (*ast.Term, bool) {
			ts, h, d, ok := tr.templateString(terms, ctx, inner)
			if ok {
				hoisted = append(hoisted, h...)
				dropped = append(dropped, d...)
			}
			return ts, ok
		}

		if terms, ok := expr.Terms.([]*ast.Term); ok && isTemplateStringCall(terms) {
			if ts, ok := reconstruct(terms); ok {
				switch len(terms) {
				case 2:
					expr.Terms = ts
				case 3:
					expr.Terms = ast.Equality.Expr(ts, terms[2]).Terms
					assigned = append(assigned, expr)
				}
			}
		}

		tr.callTerms(expr, func(t *ast.Term) {
			if ts, ok := reconstruct(t.Value.(ast.Call)); ok {
				t.Value = ts.Value
				if expr.IsEquality() {
					assigned = append(assigned, expr)
				}
			}
		})

		out = slices.DeleteFunc(out, func(e *ast.Expr) bool { return slices.Contains(dropped, e) })
		out = append(out, hoisted...)
		out = append(out, expr)
	}

	for _, expr := range assigned {
		out = elideOutput(out, expr, inner)
	}

	return out
}

// closures processes the bodies of closures (comprehensions and every) nested
// directly under x.
func (tr *tsReconstructor) closures(x any, visible ast.VarSet, counts map[ast.Var]int) {
	scope := func(head ...any) *tsScope {
		return &tsScope{counts: counts, visible: visible, head: head}
	}

	var vis *ast.GenericVisitor
	vis = ast.NewGenericVisitor(func(x any) bool {
		switch x := x.(type) {
		case *ast.ArrayComprehension:
			x.Body = tr.body(x.Body, scope(x.Term))
			return true
		case *ast.SetComprehension:
			x.Body = tr.body(x.Body, scope(x.Term))
			return true
		case *ast.ObjectComprehension:
			x.Body = tr.body(x.Body, scope(x.Key, x.Value))
			return true
		case *ast.Every:
			vis.Walk(x.Domain)
			sc := scope()
			sc.visible = visible.Copy()
			sc.visible.Update(x.KeyValueVars())
			x.Body = tr.body(x.Body, sc)
			return true
		}
		return false
	})
	vis.Walk(x)
}

// callTerms calls f on all internal.template_string call terms under expr that
// are not nested inside closures.
func (*tsReconstructor) callTerms(expr *ast.Expr, f func(*ast.Term)) {
	ast.NewGenericVisitor(func(x any) bool {
		switch x := x.(type) {
		case *ast.ArrayComprehension, *ast.SetComprehension, *ast.ObjectComprehension, *ast.Every, *ast.TemplateString:
			return true
		case *ast.Term:
			if call, ok := x.Value.(ast.Call); ok && len(call) == 2 && isTemplateStringCall(call) {
				f(x)
			}
		}
		return false
	}).Walk(expr)
}

// tsContext describes the expr an internal.template_string call is found in.
type tsContext struct {
	// preceding holds the exprs before the call's expr in the enclosing body.
	preceding ast.Body

	// with holds the with-modifiers of the call's expr. Generated bindings are
	// only folded into, and exprs only hoisted out of, the call when evaluated
	// under the same modifiers.
	with []*ast.With

	// hoist is set when new exprs may be inserted ahead of the call's expr, which
	// isn't the case for negated exprs.
	hoist bool
}

// templateString reconstructs a template-string term from the operator and
// operands of an internal.template_string call. Preceding exprs of the enclosing
// body are consulted for generated bindings of hoisted comprehensions; those
// bindings are returned for removal. Exprs that must be inserted ahead of the
// call are returned as hoisted.
func (tr *tsReconstructor) templateString(terms []*ast.Term, ctx tsContext, sc *tsScope) (ts *ast.Term, hoisted, dropped []*ast.Expr, ok bool) {
	arr, ok := terms[1].Value.(*ast.Array)
	if !ok {
		return nil, nil, nil, false
	}

	var parts []ast.Node
	var lit strings.Builder

	literal := func(s string) {
		lit.WriteString(s)
	}
	interpolate := func(e *ast.Expr) {
		if lit.Len() > 0 {
			parts = append(parts, ast.StringTerm(lit.String()))
			lit.Reset()
		}
		parts = append(parts, e)
	}

	for i := range arr.Len() {
		elem := arr.Elem(i)
		switch v := elem.Value.(type) {
		case ast.String:
			literal(string(v))
		case ast.Number, ast.Boolean, ast.Null:
			literal(v.String())
		case ast.Var:
			binding, compr := comprehensionBinding(v, ctx, sc.counts)
			if binding == nil {
				return nil, nil, nil, false
			}
			e, ok := tr.collapse(compr, sc)
			if !ok {
				return nil, nil, nil, false
			}
			dropped = append(dropped, binding)
			interpolate(e)
		case *ast.SetComprehension:
			e, ok := tr.collapse(v, sc)
			if !ok {
				return nil, nil, nil, false
			}
			interpolate(e)
		case ast.Set:
			switch v.Len() {
			case 0:
				literal("<undefined>")
			case 1:
				x := v.Slice()[0]
				switch {
				case ast.IsConstant(x.Value):
					if s, ok := x.Value.(ast.String); ok {
						literal(string(s))
					} else {
						literal(x.Value.String())
					}
				case alwaysDefined(x):
					interpolate(ast.NewExpr(x))
				case !ctx.hoist:
					return nil, nil, nil, false
				default:
					// Set elements must be defined for the call to succeed, whereas
					// template-string expressions render undefined values. Bind the
					// element ahead of the template-string to preserve that.
					h := tr.fresh()
					binding := ast.Equality.Expr(h, x)
					for _, w := range ctx.with {
						binding.With = append(binding.With, w.Copy())
					}
					hoisted = append(hoisted, binding)
					interpolate(ast.NewExpr(ast.NewTerm(h.Value)))
				}
			default:
				return nil, nil, nil, false
			}
		default:
			return nil, nil, nil, false
		}
	}

	if len(parts) == 0 {
		return ast.StringTerm(lit.String()).SetLocation(terms[0].Location), hoisted, dropped, true
	}
	if lit.Len() > 0 {
		parts = append(parts, ast.StringTerm(lit.String()))
	}

	return ast.TemplateStringTerm(false, parts...).SetLocation(terms[0].Location), hoisted, dropped, true
}

// collapse turns a comprehension produced by the template-string rewriting, e.g.
// {__local0__ | __local1__ = input.x; count(__local1__, __local2__); __local0__ = __local2__},
// back into the template-string expression it was created from, e.g. count(input.x).
func (tr *tsReconstructor) collapse(compr *ast.SetComprehension, sc *tsScope) (*ast.Expr, bool) {
	head, ok := compr.Term.Value.(ast.Var)
	if !ok {
		return nil, false
	}

	body := slices.Clone(compr.Body)
	for changed := true; changed && len(body) > 1; {
		changed = false
		for i, def := range body {
			g, val, ok := tr.definition(def)
			if !ok || g.Equal(head) || sc.counts[g] != 2 {
				continue
			}
			j := slices.IndexFunc(body[i+1:], func(e *ast.Expr) bool {
				return topLevelVarCount(e, g) == 1
			})
			if j < 0 {
				continue
			}
			j += i + 1
			if body[j].Negated || len(body[j].With) > 0 {
				continue
			}
			body[j] = body[j].Copy()
			substitute(body[j], g, val)
			body = slices.Delete(body, i, i+1)
			changed = true
			break
		}
	}

	if len(body) != 1 || body[0].Negated || !body[0].IsEquality() {
		return nil, false
	}

	expr := body[0]
	var t *ast.Term
	switch a, b := expr.Operand(0), expr.Operand(1); {
	case a.Value.Compare(head) == 0:
		t = b
	case b.Value.Compare(head) == 0:
		t = a
	default:
		return nil, false
	}

	part := &ast.Expr{Terms: t, With: expr.With, Location: expr.Location}
	if call, ok := t.Value.(ast.Call); ok {
		part.Terms = []*ast.Term(call)
	}

	vars := part.Vars(ast.VarVisitorParams{SkipRefCallHead: true})
	if vars.Contains(head) || vars.DiffCount(sc.visible) > 0 {
		return nil, false
	}

	return part, true
}

// definition returns the generated var defined by expr, and the term it's bound
// to, if expr is a unification with a generated var or a function call with a
// generated output var.
func (tr *tsReconstructor) definition(expr *ast.Expr) (ast.Var, *ast.Term, bool) {
	if expr.Negated || len(expr.With) > 0 {
		return "", nil, false
	}

	terms, ok := expr.Terms.([]*ast.Term)
	if !ok {
		return "", nil, false
	}

	if expr.IsEquality() {
		for _, pair := range [2][2]*ast.Term{{terms[1], terms[2]}, {terms[2], terms[1]}} {
			if g, ok := pair[0].Value.(ast.Var); ok && g.IsGenerated() && !pair[1].Vars().Contains(g) {
				return g, pair[1], true
			}
		}
		return "", nil, false
	}

	op, ok := terms[0].Value.(ast.Ref)
	if !ok || len(terms) < 2 || tr.arity(op) != len(terms)-2 {
		return "", nil, false
	}

	g, ok := terms[len(terms)-1].Value.(ast.Var)
	if !ok || !g.IsGenerated() {
		return "", nil, false
	}

	call := ast.CallTerm(terms[:len(terms)-1]...)
	if call.Vars().Contains(g) {
		return "", nil, false
	}

	return g, call.SetLocation(expr.Location), true
}

// elideOutput folds a reconstructed `<template-string> = out` expr into the
// single place out is used, provided out is generated. The use may be another
// expr in the same body evaluated under the same with-modifiers, or the head of
// the rule or comprehension owning the body.
func elideOutput(body ast.Body, expr *ast.Expr, sc *tsScope) ast.Body {
	if expr.Negated {
		return body
	}

	ts, out := expr.Operand(0), expr.Operand(1)
	if _, ok := out.Value.(*ast.TemplateString); ok {
		ts, out = out, ts
	}
	g, ok := out.Value.(ast.Var)
	if _, isTS := ts.Value.(*ast.TemplateString); !isTS || !ok || !g.IsGenerated() {
		return body
	}

	i := slices.Index(body, expr)
	if i < 0 {
		return body
	}

	var use *ast.Expr
	var uses int
	for j, e := range body {
		if n := countVars(e)[g]; j != i && n > 0 {
			use = e
			uses += n
		}
	}

	switch {
	case uses == 1:
		if sc.counts[g] != 2 || use.IsSome() || topLevelVarCount(use, g) != 1 || !withEqual(use.With, expr.With) {
			return body
		}
		substitute(use, g, ts)
	case uses == 0 && len(sc.head) > 0:
		if len(expr.With) > 0 || (len(body) == 1 && !sc.mayEmpty) {
			return body
		}
		n := countVars(sc.head...)[g]
		if n == 0 || sc.counts[g] != n+1 {
			return body
		}
		for _, h := range sc.head {
			substitute(h, g, ts)
		}
	default:
		return body
	}

	return slices.Delete(body, i, i+1)
}

// comprehensionBinding finds the generated binding `v = {... | ...}` for a
// comprehension hoisted out of the internal.template_string call.
func comprehensionBinding(v ast.Var, ctx tsContext, counts map[ast.Var]int) (*ast.Expr, *ast.SetComprehension) {
	if !v.IsGenerated() || counts[v] != 2 {
		return nil, nil
	}

	for i := len(ctx.preceding) - 1; i >= 0; i-- {
		expr := ctx.preceding[i]
		if expr.Negated || !expr.IsEquality() {
			continue
		}
		a, b := expr.Operand(0), expr.Operand(1)
		if b.Value.Compare(v) == 0 {
			a, b = b, a
		}
		if a.Value.Compare(v) != 0 {
			continue
		}
		if compr, ok := b.Value.(*ast.SetComprehension); ok && withEqual(expr.With, ctx.with) {
			return expr, compr
		}
		return nil, nil
	}

	return nil, nil
}

func isTemplateStringCall(terms []*ast.Term) bool {
	if len(terms) != 2 && len(terms) != 3 {
		return false
	}
	ref, ok := terms[0].Value.(ast.Ref)
	return ok && ref.Equal(ast.InternalTemplateString.Ref())
}

func containsTemplateStringCall(x any) bool {
	var found bool
	ast.NewGenericVisitor(func(x any) bool {
		if found {
			return true
		}
		switch x := x.(type) {
		case *ast.Expr:
			if terms, ok := x.Terms.([]*ast.Term); ok && isTemplateStringCall(terms) {
				found = true
			}
		case ast.Call:
			if isTemplateStringCall(x) {
				found = true
			}
		}
		return found
	}).Walk(x)
	return found
}

// alwaysDefined reports whether evaluating t can never be undefined, i.e. t
// contains no refs or calls outside of closures.
func alwaysDefined(t *ast.Term) bool {
	defined := true
	ast.NewGenericVisitor(func(x any) bool {
		switch x.(type) {
		case *ast.ArrayComprehension, *ast.SetComprehension, *ast.ObjectComprehension:
			return true
		case ast.Ref, ast.Call:
			defined = false
		}
		return !defined
	}).Walk(t)
	return defined
}

func withEqual(a, b []*ast.With) bool {
	return slices.EqualFunc(a, b, (*ast.With).Equal)
}

func countVars(xs ...any) map[ast.Var]int {
	counts := map[ast.Var]int{}
	for _, x := range xs {
		ast.WalkVars(x, func(v ast.Var) bool {
			counts[v]++
			return false
		})
	}
	return counts
}

// topLevelVars returns the vars in body that are not nested inside closures.
func topLevelVars(body ast.Body) ast.VarSet {
	vars := ast.NewVarSet()
	var vis *ast.GenericVisitor
	vis = ast.NewGenericVisitor(func(x any) bool {
		switch x := x.(type) {
		case *ast.ArrayComprehension, *ast.SetComprehension, *ast.ObjectComprehension:
			return true
		case *ast.Every:
			vis.Walk(x.Domain)
			return true
		case ast.Var:
			vars.Add(x)
		}
		return false
	})
	vis.Walk(body)
	return vars
}

func topLevelVarCount(expr *ast.Expr, v ast.Var) int {
	var n int
	var vis *ast.GenericVisitor
	vis = ast.NewGenericVisitor(func(x any) bool {
		switch x := x.(type) {
		case *ast.ArrayComprehension, *ast.SetComprehension, *ast.ObjectComprehension:
			return true
		case *ast.Every:
			vis.Walk(x.Domain)
			return true
		case ast.Var:
			if x.Equal(v) {
				n++
			}
		}
		return false
	})
	vis.Walk(expr)
	return n
}

// substitute replaces v with t in x, in place.
func substitute(x any, v ast.Var, t *ast.Term) {
	f := func(x ast.Var) (ast.Value, error) {
		if x.Equal(v) {
			return t.Copy().Value, nil
		}
		return x, nil
	}
	if term, ok := x.(*ast.Term); ok {
		value, _ := ast.TransformVars(term.Value, f)
		term.Value = value.(ast.Value)
		return
	}
	_, _ = ast.TransformVars(x, f)
}
