// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import (
	"github.com/open-policy-agent/opa/v1/ast"
)

// reconstructPartialTemplateStrings rewrites residual queries and support
// modules so internal.template_string calls introduced by the compiler are
// surfaced as template-string terms again. Bindings that partial evaluation
// keeps only to feed those calls are folded back into the template when the
// result is still valid Rego.
func reconstructPartialTemplateStrings(queries []ast.Body, support []*ast.Module) ([]ast.Body, []*ast.Module) {
	for i := range queries {
		queries[i] = reconstructTemplateStringsBody(queries[i], nil)
	}
	for _, mod := range support {
		ast.WalkRules(mod, func(rule *ast.Rule) bool {
			protected := map[ast.Var]struct{}{}
			ast.WalkVars(rule.Head, func(v ast.Var) bool {
				protected[v] = struct{}{}
				return false
			})
			rule.Body = reconstructTemplateStringsBody(rule.Body, protected)
			return false
		})
	}
	return queries, support
}

type tsBind struct {
	term *ast.Term
	with []*ast.With
	pure bool
}

func reconstructTemplateStringsBody(body ast.Body, protected map[ast.Var]struct{}) ast.Body {
	if len(body) == 0 {
		return body
	}
	for _, expr := range body {
		reconstructTemplateStringsNested(expr)
	}

	r := newTSReconstructor(body)
	for _, expr := range body {
		r.rewriteExpr(expr)
	}
	return r.removeInlined(body, protected)
}

func reconstructTemplateStringsNested(expr *ast.Expr) {
	ast.WalkClosures(expr, func(x any) bool {
		switch x := x.(type) {
		case *ast.SetComprehension:
			x.Body = reconstructTemplateStringsBody(x.Body, nil)
		case *ast.ArrayComprehension:
			x.Body = reconstructTemplateStringsBody(x.Body, nil)
		case *ast.ObjectComprehension:
			x.Body = reconstructTemplateStringsBody(x.Body, nil)
		case *ast.Every:
			x.Body = reconstructTemplateStringsBody(x.Body, nil)
		}
		// Nested closures are rewritten when their own body is processed.
		return true
	})
}

type tsReconstructor struct {
	setBindings   map[ast.Var]tsBind
	valueBindings map[ast.Var]tsBind
	consumed      map[ast.Var]struct{}
}

func newTSReconstructor(body ast.Body) *tsReconstructor {
	r := &tsReconstructor{
		setBindings:   map[ast.Var]tsBind{},
		valueBindings: map[ast.Var]tsBind{},
		consumed:      map[ast.Var]struct{}{},
	}
	poison := map[ast.Var]struct{}{}
	for _, expr := range body {
		if expr.Negated || (!expr.IsEquality() && !expr.IsAssignment()) {
			continue
		}
		lhs, rhs := eqOperands(expr)
		if lhs == nil || rhs == nil {
			continue
		}
		v, ok := lhs.Value.(ast.Var)
		if !ok || !v.IsGenerated() {
			if rv, ok := rhs.Value.(ast.Var); ok && rv.IsGenerated() {
				v = rv
				rhs = lhs
			} else {
				continue
			}
		}
		if _, dup := poison[v]; dup {
			continue
		}
		if _, exists := r.setBindings[v]; exists {
			delete(r.setBindings, v)
			poison[v] = struct{}{}
			continue
		}
		if _, exists := r.valueBindings[v]; exists {
			delete(r.valueBindings, v)
			poison[v] = struct{}{}
			continue
		}
		b := tsBind{term: rhs, with: expr.With, pure: pureTerm(rhs)}
		switch rhs.Value.(type) {
		case ast.Set, *ast.SetComprehension:
			r.setBindings[v] = b
		default:
			r.valueBindings[v] = b
		}
	}
	return r
}

func (r *tsReconstructor) rewriteExpr(expr *ast.Expr) {
	terms, ok := expr.Terms.([]*ast.Term)
	if !ok || !isTemplateStringCall(terms) {
		r.rewriteExprTerms(expr)
		return
	}
	saved := copyVars(r.consumed)
	ts, ok := r.buildTemplate(terms[1], expr.With)
	if !ok {
		r.consumed = saved
		r.rewriteExprTerms(expr)
		return
	}
	if len(terms) == 2 {
		expr.Terms = ts
		return
	}
	if len(terms) != 3 {
		r.consumed = saved
		return
	}
	out := terms[2]
	if call, ok := out.Value.(ast.Call); ok && isTemplateStringCall(call) {
		outTS, ok := r.buildTemplate(call[1], expr.With)
		if !ok {
			r.consumed = saved
			return
		}
		out = outTS
	}
	eq := ast.Equality.Expr(out, ts)
	eq.With = expr.With
	eq.Location = expr.Location
	eq.Index = expr.Index
	eq.Generated = expr.Generated
	*expr = *eq
}

func (r *tsReconstructor) rewriteExprTerms(expr *ast.Expr) {
	switch terms := expr.Terms.(type) {
	case *ast.Term:
		r.rewriteTerm(terms, nil)
	case []*ast.Term:
		for _, t := range terms {
			r.rewriteTerm(t, nil)
		}
	case *ast.Every:
		r.rewriteTerm(terms.Domain, nil)
		if terms.Key != nil {
			r.rewriteTerm(terms.Key, nil)
		}
		if terms.Value != nil {
			r.rewriteTerm(terms.Value, nil)
		}
	}
	for _, w := range expr.With {
		r.rewriteTerm(w.Target, nil)
		r.rewriteTerm(w.Value, nil)
	}
}

func (r *tsReconstructor) rewriteTerm(t *ast.Term, parentWith []*ast.With) {
	if t == nil {
		return
	}
	if call, ok := t.Value.(ast.Call); ok && isTemplateStringCall(call) && len(call) == 2 {
		saved := copyVars(r.consumed)
		if ts, ok := r.buildTemplate(call[1], parentWith); ok {
			t.Value = ts.Value
			if t.Location == nil {
				t.Location = ts.Location
			}
			return
		}
		r.consumed = saved
	}
	switch v := t.Value.(type) {
	case ast.Call:
		for _, a := range v {
			r.rewriteTerm(a, parentWith)
		}
	case ast.Ref:
		for _, a := range v[1:] {
			r.rewriteTerm(a, parentWith)
		}
	case *ast.Array:
		for i := range v.Len() {
			r.rewriteTerm(v.Elem(i), parentWith)
		}
	case *ast.TemplateString:
		for _, p := range v.Parts {
			if pt, ok := p.(*ast.Term); ok {
				r.rewriteTerm(pt, parentWith)
			}
		}
	}
}

func (r *tsReconstructor) buildTemplate(arr *ast.Term, parentWith []*ast.With) (*ast.Term, bool) {
	array, ok := arr.Value.(*ast.Array)
	if !ok {
		return nil, false
	}
	parts := make([]ast.Node, 0, array.Len())
	for i := range array.Len() {
		part, ok := r.partNode(array.Elem(i), parentWith)
		if !ok {
			return nil, false
		}
		parts = append(parts, part)
	}
	return ast.TemplateStringTerm(false, mergeTemplateStringParts(parts)...), true
}

func (r *tsReconstructor) partNode(t *ast.Term, parentWith []*ast.With) (ast.Node, bool) {
	if t == nil {
		return nil, false
	}
	if call, ok := t.Value.(ast.Call); ok && isTemplateStringCall(call) && len(call) == 2 {
		return r.buildTemplate(call[1], parentWith)
	}
	switch v := t.Value.(type) {
	case ast.String:
		return t, true
	case ast.Var:
		return r.partFromVar(v, parentWith)
	case ast.Set:
		return r.partFromSet(v)
	case *ast.SetComprehension:
		return r.partFromComprehension(v)
	default:
		return r.exprPart(t)
	}
}

func (r *tsReconstructor) partFromVar(v ast.Var, parentWith []*ast.With) (ast.Node, bool) {
	if b, ok := r.setBindings[v]; ok {
		switch val := b.term.Value.(type) {
		case ast.Set:
			expr, ok := r.partFromSet(val)
			if !ok {
				return nil, false
			}
			if !r.consume(v, true) {
				return nil, false
			}
			return attachWith(expr, b.with, parentWith), true
		case *ast.SetComprehension:
			expr, ok := r.partFromComprehension(val)
			if !ok {
				return nil, false
			}
			if !r.consume(v, true) {
				return nil, false
			}
			return attachWith(expr, b.with, parentWith), true
		default:
			return nil, false
		}
	}
	// A bare generated var in the parts array is the set of values to
	// interpolate. Without a set binding it is not representable as a
	// template expression.
	if v.IsGenerated() {
		return nil, false
	}
	return r.exprPart(ast.NewTerm(v))
}

func (r *tsReconstructor) partFromSet(s ast.Set) (ast.Node, bool) {
	if s.Len() != 1 {
		return nil, false
	}
	return r.exprPart(s.Slice()[0])
}

func (r *tsReconstructor) partFromComprehension(sc *ast.SetComprehension) (ast.Node, bool) {
	locals, ok := comprehensionBindings(sc.Body)
	if !ok {
		return nil, false
	}
	tv, ok := sc.Term.Value.(ast.Var)
	if !ok {
		return nil, false
	}
	b, ok := locals[tv]
	if !ok || !reachableBindings(tv, locals) {
		return nil, false
	}
	term, withs, ok := substLocal(b.term, locals, map[ast.Var]struct{}{}, 0)
	if !ok {
		return nil, false
	}
	if leftoverGenerated(term, locals) {
		return nil, false
	}
	withs = append(withs, b.with...)
	e := ast.NewExpr(term)
	e.With = withs
	// Inline outer value aliases (partial-eval locals closed over by the
	// comprehension) so the expression names the residual term.
	return r.inlineExpr(e)
}

func (r *tsReconstructor) exprPart(t *ast.Term) (ast.Node, bool) {
	e, ok := r.inlineTerm(t)
	if !ok {
		return nil, false
	}
	if !exprRepresentable(e, r.setBindings) {
		return nil, false
	}
	return e, true
}

func (r *tsReconstructor) inlineTerm(t *ast.Term) (*ast.Expr, bool) {
	term, withs, ok := r.substValue(t, map[ast.Var]struct{}{}, 0)
	if !ok {
		return nil, false
	}
	e := ast.NewExpr(term)
	e.With = withs
	return e, true
}

func (r *tsReconstructor) inlineExpr(e *ast.Expr) (*ast.Expr, bool) {
	term, ok := e.Terms.(*ast.Term)
	if !ok {
		return e, true
	}
	inlined, ok := r.inlineTerm(term)
	if !ok {
		return nil, false
	}
	inlined.With = append(inlined.With, e.With...)
	if !exprRepresentable(inlined, r.setBindings) {
		return nil, false
	}
	return inlined, true
}

func (r *tsReconstructor) substValue(t *ast.Term, stack map[ast.Var]struct{}, depth int) (*ast.Term, []*ast.With, bool) {
	if t == nil || depth > 32 {
		return nil, nil, false
	}
	if v, ok := t.Value.(ast.Var); ok && v.IsGenerated() {
		b, ok := r.valueBindings[v]
		if !ok {
			return t, nil, true
		}
		if _, seen := stack[v]; seen {
			return nil, nil, false
		}
		stack[v] = struct{}{}
		defer delete(stack, v)
		nt, withs, ok := r.substValue(b.term, stack, depth+1)
		if !ok {
			return nil, nil, false
		}
		if !r.consume(v, b.pure) {
			return nil, nil, false
		}
		return nt.Copy(), append(withs, copyWiths(b.with)...), true
	}
	switch v := t.Value.(type) {
	case ast.Ref:
		ref := make(ast.Ref, len(v))
		for i, elem := range v {
			nt, ws, ok := r.substValue(elem, stack, depth+1)
			if !ok || len(ws) > 0 {
				return nil, nil, false
			}
			if i == 0 {
				if _, ok := nt.Value.(ast.Var); !ok {
					return nil, nil, false
				}
			}
			ref[i] = nt
		}
		return ast.NewTerm(ref).SetLocation(t.Location), nil, true
	case ast.Call:
		call := make(ast.Call, len(v))
		for i, elem := range v {
			nt, ws, ok := r.substValue(elem, stack, depth+1)
			if !ok || len(ws) > 0 {
				return nil, nil, false
			}
			call[i] = nt
		}
		return ast.CallTerm(call...).SetLocation(t.Location), nil, true
	case *ast.Array:
		arr := ast.NewArray()
		for i := range v.Len() {
			nt, ws, ok := r.substValue(v.Elem(i), stack, depth+1)
			if !ok || len(ws) > 0 {
				return nil, nil, false
			}
			arr = arr.Append(nt)
		}
		return ast.NewTerm(arr).SetLocation(t.Location), nil, true
	case *ast.TemplateString:
		cpy := v.Copy()
		for i, p := range cpy.Parts {
			switch p := p.(type) {
			case *ast.Term:
				nt, ws, ok := r.substValue(p, stack, depth+1)
				if !ok || len(ws) > 0 {
					return nil, nil, false
				}
				cpy.Parts[i] = nt
			case *ast.Expr:
				term, ok := p.Terms.(*ast.Term)
				if !ok {
					return nil, nil, false
				}
				nt, ws, ok := r.substValue(term, stack, depth+1)
				if !ok || len(ws) > 0 {
					return nil, nil, false
				}
				p.Terms = nt
			default:
				return nil, nil, false
			}
		}
		return ast.NewTerm(cpy).SetLocation(t.Location), nil, true
	default:
		return t.Copy(), nil, true
	}
}

func (r *tsReconstructor) consume(v ast.Var, pure bool) bool {
	if _, ok := r.consumed[v]; ok && !pure {
		return false
	}
	r.consumed[v] = struct{}{}
	return true
}

func (r *tsReconstructor) removeInlined(body ast.Body, protected map[ast.Var]struct{}) ast.Body {
	if len(r.consumed) == 0 {
		return body
	}
	uses := map[ast.Var]int{}
	for _, expr := range body {
		ast.WalkVars(expr, func(v ast.Var) bool {
			uses[v]++
			return false
		})
	}
	for v := range protected {
		uses[v]++
	}
	out := make(ast.Body, 0, len(body))
	for _, expr := range body {
		if lhs := generatedEqVar(expr); lhs != "" {
			if _, ok := r.consumed[lhs]; ok && uses[lhs] == 1 {
				continue
			}
		}
		out = append(out, expr)
	}
	return out
}

func comprehensionBindings(body ast.Body) (map[ast.Var]tsBind, bool) {
	m := make(map[ast.Var]tsBind, len(body))
	for _, expr := range body {
		if expr.Negated {
			return nil, false
		}
		if expr.IsEquality() || expr.IsAssignment() {
			lhs, rhs := eqOperands(expr)
			if lhs == nil || rhs == nil {
				return nil, false
			}
			v, ok := lhs.Value.(ast.Var)
			if !ok || !v.IsGenerated() {
				if rv, ok := rhs.Value.(ast.Var); ok && rv.IsGenerated() {
					v = rv
					rhs = lhs
				} else {
					return nil, false
				}
			}
			if _, exists := m[v]; exists {
				return nil, false
			}
			m[v] = tsBind{term: rhs, with: expr.With, pure: pureTerm(rhs)}
			continue
		}
		if b, ok := callOutputBinding(expr); ok {
			if _, exists := m[b.v]; exists {
				return nil, false
			}
			m[b.v] = b.bind
			continue
		}
		return nil, false
	}
	return m, true
}

type callBind struct {
	v    ast.Var
	bind tsBind
}

func callOutputBinding(expr *ast.Expr) (callBind, bool) {
	if !expr.IsCall() {
		return callBind{}, false
	}
	op := expr.Operator()
	name, ok := ast.BuiltinNameFromRef(op)
	if !ok {
		return callBind{}, false
	}
	bi := ast.BuiltinMap[name]
	if bi == nil || bi.Decl == nil || bi.Decl.Result() == nil || bi.Relation {
		return callBind{}, false
	}
	ops := expr.Operands()
	if len(ops) != bi.Decl.Arity()+1 {
		return callBind{}, false
	}
	out := ops[len(ops)-1]
	v, ok := out.Value.(ast.Var)
	if !ok || !v.IsGenerated() {
		return callBind{}, false
	}
	args := append([]*ast.Term{expr.OperatorTerm()}, ops[:len(ops)-1]...)
	return callBind{v: v, bind: tsBind{term: ast.CallTerm(args...), with: expr.With, pure: false}}, true
}

func substLocal(t *ast.Term, locals map[ast.Var]tsBind, stack map[ast.Var]struct{}, depth int) (*ast.Term, []*ast.With, bool) {
	if t == nil || depth > 32 {
		return nil, nil, false
	}
	if v, ok := t.Value.(ast.Var); ok {
		b, ok := locals[v]
		if !ok {
			return t, nil, true
		}
		if _, seen := stack[v]; seen {
			return nil, nil, false
		}
		stack[v] = struct{}{}
		defer delete(stack, v)
		nt, withs, ok := substLocal(b.term, locals, stack, depth+1)
		if !ok {
			return nil, nil, false
		}
		return nt.Copy(), append(withs, copyWiths(b.with)...), true
	}
	switch v := t.Value.(type) {
	case ast.Ref:
		ref := make(ast.Ref, len(v))
		for i, elem := range v {
			nt, ws, ok := substLocal(elem, locals, stack, depth+1)
			if !ok || len(ws) > 0 {
				return nil, nil, false
			}
			ref[i] = nt
		}
		return ast.NewTerm(ref).SetLocation(t.Location), nil, true
	case ast.Call:
		call := make(ast.Call, len(v))
		for i, elem := range v {
			nt, ws, ok := substLocal(elem, locals, stack, depth+1)
			if !ok || len(ws) > 0 {
				return nil, nil, false
			}
			call[i] = nt
		}
		return ast.CallTerm(call...).SetLocation(t.Location), nil, true
	case *ast.Array:
		arr := ast.NewArray()
		for i := range v.Len() {
			nt, ws, ok := substLocal(v.Elem(i), locals, stack, depth+1)
			if !ok || len(ws) > 0 {
				return nil, nil, false
			}
			arr = arr.Append(nt)
		}
		return ast.NewTerm(arr).SetLocation(t.Location), nil, true
	case *ast.TemplateString:
		cpy := v.Copy()
		for i, p := range cpy.Parts {
			switch p := p.(type) {
			case *ast.Term:
				nt, ws, ok := substLocal(p, locals, stack, depth+1)
				if !ok || len(ws) > 0 {
					return nil, nil, false
				}
				cpy.Parts[i] = nt
			case *ast.Expr:
				term, ok := p.Terms.(*ast.Term)
				if !ok {
					return nil, nil, false
				}
				nt, ws, ok := substLocal(term, locals, stack, depth+1)
				if !ok || len(ws) > 0 {
					return nil, nil, false
				}
				p.Terms = nt
			default:
				return nil, nil, false
			}
		}
		return ast.NewTerm(cpy).SetLocation(t.Location), nil, true
	case ast.Set:
		cpy := ast.NewSet()
		for _, elem := range v.Slice() {
			nt, ws, ok := substLocal(elem, locals, stack, depth+1)
			if !ok || len(ws) > 0 {
				return nil, nil, false
			}
			cpy.Add(nt)
		}
		return ast.NewTerm(cpy).SetLocation(t.Location), nil, true
	default:
		return t.Copy(), nil, true
	}
}

func reachableBindings(root ast.Var, locals map[ast.Var]tsBind) bool {
	seen := map[ast.Var]struct{}{}
	var walk func(t *ast.Term)
	walk = func(t *ast.Term) {
		if t == nil {
			return
		}
		ast.WalkVars(t, func(v ast.Var) bool {
			if _, ok := locals[v]; !ok {
				return false
			}
			if _, ok := seen[v]; ok {
				return false
			}
			seen[v] = struct{}{}
			walk(locals[v].term)
			return false
		})
	}
	seen[root] = struct{}{}
	if b, ok := locals[root]; ok {
		walk(b.term)
	}
	return len(seen) == len(locals)
}

func leftoverGenerated(t *ast.Term, locals map[ast.Var]tsBind) bool {
	left := false
	ast.WalkVars(t, func(v ast.Var) bool {
		if _, ok := locals[v]; ok {
			left = true
			return true
		}
		return false
	})
	return left
}

func exprRepresentable(e *ast.Expr, setBindings map[ast.Var]tsBind) bool {
	ok := true
	ast.WalkVars(e, func(v ast.Var) bool {
		if _, isSet := setBindings[v]; isSet {
			ok = false
			return true
		}
		return false
	})
	return ok
}

func eqOperands(expr *ast.Expr) (*ast.Term, *ast.Term) {
	ops := expr.Operands()
	if len(ops) != 2 {
		return nil, nil
	}
	return ops[0], ops[1]
}

func generatedEqVar(expr *ast.Expr) ast.Var {
	if expr.Negated || (!expr.IsEquality() && !expr.IsAssignment()) {
		return ""
	}
	lhs, rhs := eqOperands(expr)
	if lhs == nil {
		return ""
	}
	if v, ok := lhs.Value.(ast.Var); ok && v.IsGenerated() {
		return v
	}
	if rhs != nil {
		if v, ok := rhs.Value.(ast.Var); ok && v.IsGenerated() {
			return v
		}
	}
	return ""
}

func isTemplateStringCall(terms []*ast.Term) bool {
	if len(terms) < 2 {
		return false
	}
	ref, ok := terms[0].Value.(ast.Ref)
	if !ok {
		return false
	}
	name, ok := ast.BuiltinNameFromRef(ref)
	return ok && name == ast.InternalTemplateString.Name
}

func pureTerm(t *ast.Term) bool {
	switch t.Value.(type) {
	case ast.Var, ast.Ref, ast.String, ast.Number, ast.Boolean, ast.Null:
		return true
	default:
		return false
	}
}

func mergeTemplateStringParts(parts []ast.Node) []ast.Node {
	if len(parts) == 0 {
		return []ast.Node{ast.StringTerm("")}
	}
	out := make([]ast.Node, 0, len(parts))
	for _, p := range parts {
		term, ok := p.(*ast.Term)
		if !ok {
			out = append(out, p)
			continue
		}
		s, ok := term.Value.(ast.String)
		if !ok || len(out) == 0 {
			out = append(out, p)
			continue
		}
		prev, ok := out[len(out)-1].(*ast.Term)
		if !ok {
			out = append(out, p)
			continue
		}
		ps, ok := prev.Value.(ast.String)
		if !ok {
			out = append(out, p)
			continue
		}
		out[len(out)-1] = ast.StringTerm(string(ps) + string(s))
	}
	return out
}

func attachWith(n ast.Node, withs, parent []*ast.With) ast.Node {
	if len(withs) == 0 || withsEqual(withs, parent) {
		return n
	}
	expr, ok := n.(*ast.Expr)
	if !ok {
		return n
	}
	expr.With = append(expr.With, copyWiths(withs)...)
	return expr
}

func withsEqual(a, b []*ast.With) bool {
	if len(a) != len(b) || len(a) == 0 {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}

func copyWiths(ws []*ast.With) []*ast.With {
	if len(ws) == 0 {
		return nil
	}
	out := make([]*ast.With, len(ws))
	for i, w := range ws {
		out[i] = w.Copy()
	}
	return out
}

func copyVars(in map[ast.Var]struct{}) map[ast.Var]struct{} {
	out := make(map[ast.Var]struct{}, len(in))
	for v := range in {
		out[v] = struct{}{}
	}
	return out
}
