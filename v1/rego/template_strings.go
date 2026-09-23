// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import (
	"fmt"

	"github.com/open-policy-agent/opa/v1/ast"
)

// reconstructTemplateStrings rewrites internal.template_string calls left in
// partial evaluation output back into template-string syntax. Calls whose
// components can't be represented as a template string are left untouched.
func reconstructTemplateStrings(queries []ast.Body, support []*ast.Module) {
	for i := range queries {
		r := newTemplateStringReconstructor(queries[i])
		queries[i] = r.body(queries[i])
	}

	for _, mod := range support {
		for _, rule := range mod.Rules {
			r := newTemplateStringReconstructor(rule)
			for rr := rule; rr != nil; rr = rr.Else {
				rr.Body = r.body(rr.Body)
			}
		}
	}
}

type templateStringReconstructor struct {
	// counts holds the number of occurrences of each var within the enclosing
	// query or rule. Vars occurring exactly twice (once where bound, and once
	// where used) are candidates for inlining.
	counts map[ast.Var]int
	fresh  int
}

func newTemplateStringReconstructor(x any) *templateStringReconstructor {
	r := &templateStringReconstructor{counts: map[ast.Var]int{}}
	ast.WalkVars(x, func(v ast.Var) bool {
		r.counts[v]++
		return false
	})
	return r
}

func (r *templateStringReconstructor) freshVar() *ast.Term {
	for {
		v := ast.Var(fmt.Sprintf("__ts%d__", r.fresh))
		r.fresh++
		if _, ok := r.counts[v]; !ok {
			r.counts[v] = 2
			return ast.NewTerm(v)
		}
	}
}

func (r *templateStringReconstructor) body(body ast.Body) ast.Body {
	r.nested(body)

	tried := map[*ast.Expr]struct{}{}
	for {
		idx := -1
		for i, expr := range body {
			if _, ok := tried[expr]; !ok && isTemplateStringCall(expr) {
				idx = i
				break
			}
		}
		if idx < 0 {
			return body
		}
		tried[body[idx]] = struct{}{}
		if b, ok := r.reconstruct(body, idx); ok {
			body = b
		}
	}
}

// nested reconstructs template strings in all closures nested within x.
func (r *templateStringReconstructor) nested(x any) {
	ast.NewGenericVisitor(func(x any) bool {
		switch x := x.(type) {
		case *ast.ArrayComprehension:
			x.Body = r.body(x.Body)
			r.nested(x.Term)
			return true
		case *ast.SetComprehension:
			x.Body = r.body(x.Body)
			r.nested(x.Term)
			return true
		case *ast.ObjectComprehension:
			x.Body = r.body(x.Body)
			r.nested(x.Key)
			r.nested(x.Value)
			return true
		case *ast.Every:
			x.Body = r.body(x.Body)
			r.nested(x.Domain)
			return true
		}
		return false
	}).Walk(x)
}

func isTemplateStringCall(expr *ast.Expr) bool {
	return templateStringCallTerms(expr) != nil
}

// templateStringCallTerms returns the operator, array operand and (optional)
// output of the internal.template_string call in expr. The call may appear with
// an output operand, as a term expression, or as one side of an equality.
func templateStringCallTerms(expr *ast.Expr) []*ast.Term {
	if expr.Negated || len(expr.With) > 0 {
		return nil
	}
	switch x := expr.Terms.(type) {
	case []*ast.Term:
		if expr.IsEquality() {
			if call := templateStringCallTerm(x[2]); call != nil {
				return append(call, x[1])
			}
			if call := templateStringCallTerm(x[1]); call != nil {
				return append(call, x[2])
			}
			return nil
		}
		if len(x) == 3 && isTemplateStringOperator(x[0]) {
			return x
		}
	case *ast.Term:
		return templateStringCallTerm(x)
	}
	return nil
}

func templateStringCallTerm(t *ast.Term) []*ast.Term {
	if call, ok := t.Value.(ast.Call); ok && len(call) == 2 && isTemplateStringOperator(call[0]) {
		return []*ast.Term{call[0], call[1]}
	}
	return nil
}

func isTemplateStringOperator(t *ast.Term) bool {
	ref, ok := t.Value.(ast.Ref)
	return ok && ref.Equal(ast.InternalTemplateString.Ref())
}

func (r *templateStringReconstructor) reconstruct(body ast.Body, idx int) (ast.Body, bool) {
	expr := body[idx]
	terms := templateStringCallTerms(expr)
	arr, ok := terms[1].Value.(*ast.Array)
	if !ok {
		return nil, false
	}
	var out *ast.Term
	if len(terms) == 3 {
		out = terms[2]
	}

	removed := map[int]struct{}{}
	var bindings []*ast.Expr
	parts := make([]ast.Node, 0, arr.Len())

	for i := range arr.Len() {
		elem := arr.Elem(i)

		if v, ok := elem.Value.(ast.Var); ok {
			j, rhs := r.findBinding(body, idx, v, removed)
			if j < 0 {
				return nil, false
			}
			removed[j] = struct{}{}
			elem = rhs
		}

		switch x := elem.Value.(type) {
		case ast.String:
			parts = append(parts, elem)
		case *ast.SetComprehension:
			v, ok := x.Term.Value.(ast.Var)
			if !ok {
				return nil, false
			}
			t, with, ok := r.resolve(v, x.Body)
			if !ok {
				return nil, false
			}
			parts = append(parts, templateStringExpr(t, with, expr.Location))
		case ast.Set:
			if x.Len() != 1 {
				return nil, false
			}
			t := x.Slice()[0]
			if _, ok := t.Value.(ast.Var); !ok && !ast.IsConstant(t.Value) {
				// A set term is undefined when its element is, whereas a non-var
				// template-string expression evaluates to "<undefined>"; so bind
				// the element to a var to retain the original semantics.
				f := r.freshVar()
				bindings = append(bindings, ast.Equality.Expr(f, t).SetLocation(expr.Location))
				t = f
			}
			parts = append(parts, templateStringExpr(t, nil, expr.Location))
		default:
			return nil, false
		}
	}

	ts := ast.TemplateStringTerm(false, parts...).SetLocation(expr.Location)
	replacement := ast.NewExpr(ts).SetLocation(expr.Location)
	if out != nil {
		replacement = ast.Equality.Expr(out, ts).SetLocation(expr.Location)
	}

	result := make(ast.Body, 0, len(body)+len(bindings))
	for i := range body {
		if _, ok := removed[i]; ok {
			continue
		}
		if i == idx {
			result = append(result, bindings...)
			if k := r.inlineOutput(body, idx, out); k >= 0 {
				body[k] = substituteEqualitySide(body[k], out.Value.(ast.Var), ts)
				continue
			}
			result = append(result, replacement)
			continue
		}
		result = append(result, body[i])
	}
	for i := range result {
		result[i].Index = i
	}

	return result, true
}

// findBinding returns the index and right-hand side of the equality in body
// binding v to a set or set comprehension, if v is used nowhere else.
func (r *templateStringReconstructor) findBinding(body ast.Body, idx int, v ast.Var, removed map[int]struct{}) (int, *ast.Term) {
	if r.counts[v] != 2 {
		return -1, nil
	}
	for j, e := range body {
		if _, ok := removed[j]; ok || j == idx {
			continue
		}
		rhs := equalityOther(e, v)
		if rhs == nil {
			continue
		}
		switch rhs.Value.(type) {
		case *ast.SetComprehension, ast.Set:
			return j, rhs
		}
		return -1, nil
	}
	return -1, nil
}

// inlineOutput returns the index of the expression following idx that unifies
// out with some other term, if out is a var used nowhere else.
func (r *templateStringReconstructor) inlineOutput(body ast.Body, idx int, out *ast.Term) int {
	if out == nil {
		return -1
	}
	v, ok := out.Value.(ast.Var)
	if !ok || r.counts[v] != 2 {
		return -1
	}
	for k := idx + 1; k < len(body); k++ {
		if equalityOther(body[k], v) != nil {
			return k
		}
	}
	return -1
}

// resolve reduces a template-string capture comprehension body into the single
// term it binds x to, inlining intermediate bindings introduced by partial
// evaluation.
func (r *templateStringReconstructor) resolve(x ast.Var, body ast.Body) (*ast.Term, []*ast.With, bool) {
	if r.counts[x] != 2 {
		return nil, nil, false
	}

	var capture *ast.Expr
	var t *ast.Term
	rest := make([]*ast.Expr, 0, len(body))
	for _, e := range body {
		if capture == nil {
			if other := equalityOther(e, x); other != nil {
				capture, t = e, other.Copy()
				continue
			}
		}
		if e.Negated || len(e.With) > 0 {
			return nil, nil, false
		}
		rest = append(rest, e.Copy())
	}
	if capture == nil || capture.Negated {
		return nil, nil, false
	}

	for len(rest) > 0 {
		progress := false
		for i, e := range rest {
			v, u := r.bindingOf(e)
			if u == nil {
				continue
			}
			others := append(append([]*ast.Expr{}, rest[:i]...), rest[i+1:]...)
			found := false
			if nt, ok := substituteVar(t, v, u); ok {
				t, found = nt, true
			} else {
				for j, o := range others {
					if ne, ok := substituteVar(o, v, u); ok {
						others[j], found = ne, true
						break
					}
				}
			}
			if !found {
				return nil, nil, false
			}
			rest, progress = others, true
			break
		}
		if !progress {
			return nil, nil, false
		}
	}

	var with []*ast.With
	for _, w := range capture.With {
		with = append(with, w.Copy())
	}
	return t, with, true
}

// bindingOf returns the var bound by e and the term it's bound to, if e binds a
// var used exactly once elsewhere.
func (r *templateStringReconstructor) bindingOf(e *ast.Expr) (ast.Var, *ast.Term) {
	if e.IsEquality() {
		for _, pair := range [][2]*ast.Term{{e.Operand(0), e.Operand(1)}, {e.Operand(1), e.Operand(0)}} {
			if v, ok := pair[0].Value.(ast.Var); ok && r.counts[v] == 2 {
				return v, pair[1]
			}
		}
		return "", nil
	}

	if !e.IsCall() {
		return "", nil
	}
	terms := e.Terms.([]*ast.Term)
	if len(terms) < 2 {
		return "", nil
	}
	v, ok := terms[len(terms)-1].Value.(ast.Var)
	if !ok || r.counts[v] != 2 {
		return "", nil
	}
	if bi, ok := ast.BuiltinMap[e.Operator().String()]; ok {
		if bi.Relation || bi.Decl == nil || len(bi.Decl.FuncArgs().Args) != len(terms)-2 {
			return "", nil
		}
	}
	return v, ast.CallTerm(terms[:len(terms)-1]...)
}

// equalityOther returns the side of the equality e opposite to v, or nil if e
// isn't a plain equality with v on one side.
func equalityOther(e *ast.Expr, v ast.Var) *ast.Term {
	if e.Negated || len(e.With) > 0 || !e.IsEquality() {
		return nil
	}
	a, b := e.Operand(0), e.Operand(1)
	if a.Value.Compare(v) == 0 {
		return b
	}
	if b.Value.Compare(v) == 0 {
		return a
	}
	return nil
}

func substituteEqualitySide(e *ast.Expr, v ast.Var, t *ast.Term) *ast.Expr {
	cpy := e.Copy()
	terms := cpy.Terms.([]*ast.Term)
	for i := 1; i < len(terms); i++ {
		if terms[i].Value.Compare(v) == 0 {
			terms[i] = t
			break
		}
	}
	return cpy
}

// substituteVar replaces all occurrences of v in x with u. Template strings are
// not descended into, as substituting a term for a var inside a template-string
// expression changes how undefined values are rendered; if v occurs within one,
// substitution fails.
func substituteVar[T *ast.Term | *ast.Expr](x T, v ast.Var, u *ast.Term) (T, bool) {
	found, blocked := false, false
	ast.NewGenericVisitor(func(n any) bool {
		if t, ok := n.(*ast.Term); ok {
			if _, ok := t.Value.(*ast.TemplateString); ok {
				ast.WalkVars(t, func(w ast.Var) bool {
					if w.Equal(v) {
						blocked = true
					}
					return blocked
				})
				return true
			}
		}
		return false
	}).Walk(x)
	if blocked {
		return x, false
	}

	var cpy any
	switch x := any(x).(type) {
	case *ast.Term:
		cpy = x.Copy()
	case *ast.Expr:
		cpy = x.Copy()
	}

	ast.NewGenericVisitor(func(n any) bool {
		switch n := n.(type) {
		case *ast.Term:
			if _, ok := n.Value.(*ast.TemplateString); ok {
				return true
			}
			if n.Value.Compare(v) == 0 {
				n.Value = u.Value
				found = true
				return true
			}
		}
		return false
	}).Walk(cpy)

	return cpy.(T), found
}

func templateStringExpr(t *ast.Term, with []*ast.With, loc *ast.Location) *ast.Expr {
	if t.Location != nil {
		loc = t.Location
	}
	e := &ast.Expr{Location: loc, With: with}
	if call, ok := t.Value.(ast.Call); ok {
		e.Terms = []*ast.Term(call)
	} else {
		e.Terms = t
	}
	return e
}
