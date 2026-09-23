// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import (
	"strings"

	"github.com/open-policy-agent/opa/v1/ast"
)

// templateStringRef is the compiler's lowered form of a template string.
var templateStringRef = ast.InternalTemplateString.Ref()

// reconstructPartialTemplateStrings rewrites residual queries and support
// modules so internal.template_string calls that can be expressed in Rego
// source become template strings again. Generated bindings introduced while
// lowering interpolations are inlined when they only exist to build those
// strings.
func reconstructPartialTemplateStrings(queries []ast.Body, support []*ast.Module) ([]ast.Body, []*ast.Module) {
	for i, q := range queries {
		queries[i] = reconstructTemplateStringBody(q)
	}
	for i, mod := range support {
		support[i] = reconstructTemplateStringModule(mod)
	}
	return queries, support
}

func reconstructTemplateStringModule(mod *ast.Module) *ast.Module {
	if mod == nil || !containsTemplateCall(mod) {
		return mod
	}
	cpy := mod.Copy()
	for _, rule := range cpy.Rules {
		reconstructTemplateStringRule(rule)
	}
	return cpy
}

func reconstructTemplateStringRule(rule *ast.Rule) {
	if rule == nil {
		return
	}
	rule.Body = reconstructTemplateStringBody(rule.Body)
	if rule.Head != nil {
		reconstructTemplateStringHead(rule.Head, &rule.Body)
	}
	reconstructTemplateStringRule(rule.Else)
}

func reconstructTemplateStringHead(head *ast.Head, body *ast.Body) {
	if head.Key != nil {
		head.Key = reconstructNestedTerm(head.Key)
	}
	if head.Value != nil {
		head.Value = reconstructNestedTerm(head.Value)
	}
	for i, t := range head.Args {
		head.Args[i] = reconstructNestedTerm(t)
	}
	for i, t := range head.Reference {
		head.Reference[i] = reconstructNestedTerm(t)
	}
	liftTemplateHeadBindings(head, body)
}

// reconstructTemplateStringBody returns body with representable
// internal.template_string calls rewritten to template strings.
func reconstructTemplateStringBody(body ast.Body) ast.Body {
	if len(body) == 0 || !containsTemplateCall(body) {
		return body
	}

	body = body.Copy()
	for i, expr := range body {
		body[i] = reconstructNestedExpr(expr)
	}

	res := &bindingResolver{defs: indexTemplateBindings(body), inlined: map[ast.Var]struct{}{}}
	foldBare := map[ast.Var]struct{}{}
	for i, expr := range body {
		body[i] = rewriteTemplateExpr(expr, res, body, foldBare)
	}
	return dropInlinedTemplateBindings(body, res, foldBare)
}

type binding struct {
	value *ast.Term
	with  []*ast.With
	expr  *ast.Expr
}

type bindingResolver struct {
	defs    map[ast.Var]*binding
	parent  *bindingResolver
	inlined map[ast.Var]struct{}
}

func (r *bindingResolver) lookup(v ast.Var) (*binding, *bindingResolver) {
	for cur := r; cur != nil; cur = cur.parent {
		if b, ok := cur.defs[v]; ok {
			return b, cur
		}
	}
	return nil, nil
}

type varUse struct {
	v     ast.Var
	owner *bindingResolver
}

// decode resolves template-string parts against generated bindings.
// used is committed only when a whole template call is rewritten.
type decode struct {
	res  *bindingResolver
	seen map[ast.Var]bool
	used []varUse
}

func (d *decode) note(v ast.Var, owner *bindingResolver) {
	if owner == nil || owner.inlined == nil {
		return
	}
	d.used = append(d.used, varUse{v: v, owner: owner})
}

func (d *decode) commit() {
	for _, u := range d.used {
		u.owner.inlined[u.v] = struct{}{}
	}
}

func (d *decode) absorbParent(sub *decode) {
	for _, u := range sub.used {
		if u.owner == sub.res {
			continue
		}
		d.used = append(d.used, u)
	}
}

func indexTemplateBindings(body ast.Body) map[ast.Var]*binding {
	defs := make(map[ast.Var]*binding)
	for _, expr := range body {
		if expr.Negated {
			continue
		}
		if expr.IsEquality() {
			left, right := expr.Operand(0), expr.Operand(1)
			if left == nil || right == nil || isTemplateCallTerm(left) || isTemplateCallTerm(right) {
				continue
			}
			if v, ok := generatedVar(left); ok {
				if _, exists := defs[v]; !exists {
					defs[v] = &binding{value: right, with: expr.With, expr: expr}
				}
				continue
			}
			if v, ok := generatedVar(right); ok {
				if _, exists := defs[v]; !exists {
					defs[v] = &binding{value: left, with: expr.With, expr: expr}
				}
			}
			continue
		}
		if !expr.IsCall() || isTemplateStringExpr(expr) {
			continue
		}
		ops := expr.Operands()
		if len(ops) == 0 {
			continue
		}
		v, ok := generatedVar(ops[len(ops)-1])
		if !ok {
			continue
		}
		if _, exists := defs[v]; exists {
			continue
		}
		terms := expr.Terms.([]*ast.Term)
		callTerms := make([]*ast.Term, len(terms)-1)
		copy(callTerms, terms[:len(terms)-1])
		defs[v] = &binding{value: ast.CallTerm(callTerms...), with: expr.With, expr: expr}
	}
	return defs
}

func rewriteTemplateExpr(expr *ast.Expr, res *bindingResolver, body ast.Body, foldBare map[ast.Var]struct{}) *ast.Expr {
	if parts, out, ok := templateCallOperands(expr); ok {
		if rewritten, ok := rewriteTemplateCall(expr, parts, out, res, body, foldBare); ok {
			return rewritten
		}
		return expr
	}
	if expr.IsEquality() {
		left, right := expr.Operand(0), expr.Operand(1)
		switch {
		case isTemplateCallTerm(right):
			if rewritten, ok := rewriteTemplateCall(expr, templateCallTermParts(right), left, res, body, foldBare); ok {
				return rewritten
			}
		case isTemplateCallTerm(left):
			if rewritten, ok := rewriteTemplateCall(expr, templateCallTermParts(left), right, res, body, foldBare); ok {
				return rewritten
			}
		}
	}
	expr.Terms = rewriteTemplateTerms(expr.Terms, res)
	return expr
}

func rewriteTemplateCall(src *ast.Expr, parts, out *ast.Term, res *bindingResolver, body ast.Body, foldBare map[ast.Var]struct{}) (*ast.Expr, bool) {
	if parts == nil {
		return nil, false
	}
	dec := &decode{res: res, seen: map[ast.Var]bool{}}
	ts, ok := dec.templateFromPartsTerm(parts)
	if !ok {
		return nil, false
	}
	dec.commit()

	if out != nil {
		out = rewriteTemplateTerm(out, res)
	}
	if v, isGen := generatedVar(out); isGen && bareTruthinessOnly(body, v, src) {
		foldBare[v] = struct{}{}
		return exprFromTerm(src, ts), true
	}
	if out == nil {
		return exprFromTerm(src, ts), true
	}
	eq := ast.Equality.Expr(out, ts)
	eq.With = copyWiths(src.With)
	eq.Negated = src.Negated
	eq.Location = src.Location
	return eq, true
}

func bareTruthinessOnly(body ast.Body, v ast.Var, def *ast.Expr) bool {
	bare := 0
	for _, expr := range body {
		if expr == def {
			continue
		}
		n := countVar(expr, v)
		if n == 0 {
			continue
		}
		if n == 1 && isBareVar(expr, v) {
			bare++
			continue
		}
		return false
	}
	return bare == 1
}

func dropInlinedTemplateBindings(body ast.Body, res *bindingResolver, foldBare map[ast.Var]struct{}) ast.Body {
	dropExpr := map[*ast.Expr]struct{}{}
	for {
		progress := false
		for v := range res.inlined {
			b := res.defs[v]
			if b == nil || b.expr == nil {
				continue
			}
			if _, dropped := dropExpr[b.expr]; dropped {
				continue
			}
			outside := 0
			for _, expr := range body {
				if expr == b.expr {
					continue
				}
				if _, dropped := dropExpr[expr]; dropped {
					continue
				}
				outside += countVar(expr, v)
			}
			if outside == 0 {
				dropExpr[b.expr] = struct{}{}
				progress = true
			}
		}
		if !progress {
			break
		}
	}

	out := make(ast.Body, 0, len(body))
	for _, expr := range body {
		if _, drop := dropExpr[expr]; drop {
			continue
		}
		if v, ok := bareVar(expr); ok {
			if _, fold := foldBare[v]; fold {
				continue
			}
		}
		out = append(out, expr)
	}
	if len(out) == 0 {
		return ast.NewBody(ast.NewExpr(ast.BooleanTerm(true)))
	}
	return out
}

func (d *decode) templateFromPartsTerm(term *ast.Term) (*ast.Term, bool) {
	arrTerm := term
	if v, ok := term.Value.(ast.Var); ok {
		b, owner := d.res.lookup(v)
		if b == nil || len(b.with) > 0 {
			return nil, false
		}
		if _, ok := b.value.Value.(*ast.Array); !ok {
			return nil, false
		}
		if d.seen[v] {
			return nil, false
		}
		d.seen[v] = true
		d.note(v, owner)
		arrTerm = b.value
	}
	arr, ok := arrTerm.Value.(*ast.Array)
	if !ok {
		return nil, false
	}

	parts := make([]ast.Node, 0, arr.Len())
	var pending strings.Builder
	flush := func() {
		if pending.Len() == 0 {
			return
		}
		parts = append(parts, ast.StringTerm(pending.String()))
		pending.Reset()
	}
	for i := range arr.Len() {
		node, ok := d.partNode(arr.Elem(i))
		if !ok {
			return nil, false
		}
		if t, ok := node.(*ast.Term); ok {
			if s, ok := t.Value.(ast.String); ok {
				pending.WriteString(string(s))
				continue
			}
		}
		flush()
		parts = append(parts, node)
	}
	flush()
	if len(parts) == 0 {
		parts = append(parts, ast.StringTerm(""))
	}
	return ast.TemplateStringTerm(templatePartsMultiline(parts), parts...), true
}

func templatePartsMultiline(parts []ast.Node) bool {
	for _, p := range parts {
		t, ok := p.(*ast.Term)
		if !ok {
			continue
		}
		s, ok := t.Value.(ast.String)
		if ok && strings.Contains(string(s), "\n") {
			return true
		}
	}
	return false
}

// partNode decodes a template-string part. Sets and capture comprehensions
// are wrappers the compiler inserted around the interpolated expression.
func (d *decode) partNode(term *ast.Term) (ast.Node, bool) {
	switch v := term.Value.(type) {
	case ast.String:
		return term, true
	case ast.Number, ast.Boolean, ast.Null:
		return ast.NewExpr(term), true
	case ast.Var:
		b, owner := d.res.lookup(v)
		if b == nil {
			return ast.NewExpr(term), true
		}
		if d.seen[v] {
			return nil, false
		}
		d.seen[v] = true
		defer func() { d.seen[v] = false }()
		d.note(v, owner)
		node, ok := d.partNode(b.value)
		if !ok {
			return nil, false
		}
		if len(b.with) > 0 {
			node = applyWith(node, b.with)
		}
		return node, true
	case ast.Set:
		// An empty set is the lowered form of an undefined interpolation.
		// That prints as "<undefined>" and is not a template-string expression.
		// A singleton set is the compiler wrapper around a var or ref; the
		// element itself is the interpolated value, so a var is kept (it may
		// be a comprehension iterator) rather than replaced by its binding.
		if v.Len() != 1 {
			return nil, false
		}
		elem := v.Slice()[0]
		if _, ok := elem.Value.(ast.Var); ok {
			return ast.NewExpr(elem), true
		}
		return d.valueNode(elem)
	case *ast.SetComprehension:
		return d.unwrapPartComprehension(v)
	case ast.Call:
		if isTemplateOp(v) {
			ts, ok := d.templateFromCall(v)
			if !ok {
				return nil, false
			}
			return ast.NewExpr(ts), true
		}
		return d.valueNode(term)
	default:
		return d.valueNode(term)
	}
}

// unwrapPartComprehension reduces a compiler capture {v | v = expr} to expr.
// User comprehensions are values of that capture, not the capture itself, so
// they stay intact one level down.
func (d *decode) unwrapPartComprehension(sc *ast.SetComprehension) (ast.Node, bool) {
	v, ok := generatedVar(sc.Term)
	if !ok {
		return nil, false
	}
	inner := &bindingResolver{
		defs:    indexTemplateBindings(sc.Body),
		parent:  d.res,
		inlined: map[ast.Var]struct{}{},
	}
	b, ok := inner.defs[v]
	if !ok || !onlyHelperBindings(sc.Body, inner.defs) {
		return nil, false
	}
	sub := &decode{res: inner, seen: d.seen}
	node, ok := sub.valueFromBinding(b)
	if !ok {
		return nil, false
	}
	d.absorbParent(sub)
	return node, true
}

func onlyHelperBindings(body ast.Body, defs map[ast.Var]*binding) bool {
	if len(body) == 0 || len(defs) == 0 {
		return false
	}
	indexed := make(map[*ast.Expr]struct{}, len(defs))
	for v, b := range defs {
		if !v.IsGenerated() || b.expr == nil {
			return false
		}
		indexed[b.expr] = struct{}{}
	}
	for _, expr := range body {
		if _, ok := indexed[expr]; !ok {
			return false
		}
	}
	return true
}

func (d *decode) valueFromBinding(b *binding) (ast.Node, bool) {
	node, ok := d.valueNode(b.value)
	if !ok {
		return nil, false
	}
	if len(b.with) > 0 {
		node = applyWith(node, b.with)
	}
	return node, true
}

func (d *decode) valueNode(term *ast.Term) (ast.Node, bool) {
	switch v := term.Value.(type) {
	case ast.Var:
		b, owner := d.res.lookup(v)
		if b == nil || !v.IsGenerated() {
			return ast.NewExpr(term), true
		}
		if d.seen[v] {
			return nil, false
		}
		d.seen[v] = true
		defer func() { d.seen[v] = false }()
		d.note(v, owner)
		return d.valueFromBinding(b)
	case ast.Call:
		if isTemplateOp(v) {
			ts, ok := d.templateFromCall(v)
			if !ok {
				return nil, false
			}
			return ast.NewExpr(ts), true
		}
		rebuilt, ok := d.substCall(v)
		if !ok {
			return nil, false
		}
		return ast.NewExpr(ast.NewTerm(rebuilt)), true
	case ast.Ref:
		rebuilt, ok := d.substRef(v)
		if !ok {
			return nil, false
		}
		return ast.NewExpr(rebuilt), true
	case ast.Set:
		rebuilt, ok := d.substSet(v)
		if !ok {
			return nil, false
		}
		return ast.NewExpr(rebuilt), true
	case *ast.Array:
		rebuilt, ok := d.substArray(v)
		if !ok {
			return nil, false
		}
		return ast.NewExpr(rebuilt), true
	case ast.Object:
		rebuilt, ok := d.substObject(v)
		if !ok {
			return nil, false
		}
		return ast.NewExpr(rebuilt), true
	default:
		rebuilt, ok := d.substTerm(term)
		if !ok {
			return nil, false
		}
		return ast.NewExpr(rebuilt), true
	}
}

func (d *decode) templateFromCall(c ast.Call) (*ast.Term, bool) {
	if len(c) < 2 || len(c) > 3 {
		return nil, false
	}
	return d.templateFromPartsTerm(c[1])
}

func (d *decode) substTerm(term *ast.Term) (*ast.Term, bool) {
	if term == nil {
		return nil, false
	}
	switch v := term.Value.(type) {
	case ast.Var:
		b, owner := d.res.lookup(v)
		if b == nil || !v.IsGenerated() {
			return term, true
		}
		if len(b.with) > 0 {
			return nil, false
		}
		if d.seen[v] {
			return nil, false
		}
		d.seen[v] = true
		defer func() { d.seen[v] = false }()
		d.note(v, owner)
		return d.substTerm(b.value)
	case ast.Call:
		if isTemplateOp(v) {
			ts, ok := d.templateFromCall(v)
			if !ok {
				return nil, false
			}
			return ts, true
		}
		rebuilt, ok := d.substCall(v)
		if !ok {
			return nil, false
		}
		return ast.NewTerm(rebuilt), true
	case ast.Ref:
		return d.substRef(v)
	case ast.Set:
		return d.substSet(v)
	case *ast.Array:
		return d.substArray(v)
	case ast.Object:
		return d.substObject(v)
	case *ast.ArrayComprehension:
		term, ok := d.substTerm(v.Term)
		if !ok {
			return nil, false
		}
		v.Term = term
		return ast.NewTerm(v), true
	case *ast.ObjectComprehension:
		key, ok := d.substTerm(v.Key)
		if !ok {
			return nil, false
		}
		val, ok := d.substTerm(v.Value)
		if !ok {
			return nil, false
		}
		v.Key = key
		v.Value = val
		return ast.NewTerm(v), true
	case *ast.SetComprehension:
		term, ok := d.substTerm(v.Term)
		if !ok {
			return nil, false
		}
		v.Term = term
		return ast.NewTerm(v), true
	default:
		return term, true
	}
}

func (d *decode) substRef(ref ast.Ref) (*ast.Term, bool) {
	out := make(ast.Ref, len(ref))
	for i, t := range ref {
		nt, ok := d.substTerm(t)
		if !ok {
			return nil, false
		}
		out[i] = nt
	}
	return ast.NewTerm(out), true
}

func (d *decode) substCall(c ast.Call) (ast.Call, bool) {
	out := make(ast.Call, len(c))
	for i, t := range c {
		nt, ok := d.substTerm(t)
		if !ok {
			return nil, false
		}
		out[i] = nt
	}
	return out, true
}

func (d *decode) substArray(arr *ast.Array) (*ast.Term, bool) {
	elems := make([]*ast.Term, arr.Len())
	for i := range arr.Len() {
		nt, ok := d.substTerm(arr.Elem(i))
		if !ok {
			return nil, false
		}
		elems[i] = nt
	}
	return ast.NewTerm(ast.NewArray(elems...)), true
}

func (d *decode) substSet(s ast.Set) (*ast.Term, bool) {
	elems := make([]*ast.Term, 0, s.Len())
	ok := true
	s.Foreach(func(t *ast.Term) {
		if !ok {
			return
		}
		nt, good := d.substTerm(t)
		if !good {
			ok = false
			return
		}
		elems = append(elems, nt)
	})
	if !ok {
		return nil, false
	}
	return ast.NewTerm(ast.NewSet(elems...)), true
}

func (d *decode) substObject(obj ast.Object) (*ast.Term, bool) {
	pairs := make([][2]*ast.Term, 0, obj.Len())
	ok := true
	obj.Foreach(func(k, v *ast.Term) {
		if !ok {
			return
		}
		nk, okk := d.substTerm(k)
		nv, okv := d.substTerm(v)
		if !okk || !okv {
			ok = false
			return
		}
		pairs = append(pairs, [2]*ast.Term{nk, nv})
	})
	if !ok {
		return nil, false
	}
	return ast.NewTerm(ast.NewObject(pairs...)), true
}

func reconstructNestedExpr(expr *ast.Expr) *ast.Expr {
	if expr == nil {
		return nil
	}
	expr.Terms = reconstructNestedTerms(expr.Terms)
	for _, w := range expr.With {
		if w == nil {
			continue
		}
		w.Target = reconstructNestedTerm(w.Target)
		w.Value = reconstructNestedTerm(w.Value)
	}
	return expr
}

func reconstructNestedTerms(terms any) any {
	switch ts := terms.(type) {
	case *ast.Term:
		return reconstructNestedTerm(ts)
	case []*ast.Term:
		for i, t := range ts {
			ts[i] = reconstructNestedTerm(t)
		}
		return ts
	case *ast.Every:
		ts.Domain = reconstructNestedTerm(ts.Domain)
		ts.Key = reconstructNestedTerm(ts.Key)
		ts.Value = reconstructNestedTerm(ts.Value)
		ts.Body = reconstructTemplateStringBody(ts.Body)
		return ts
	default:
		return terms
	}
}

func reconstructNestedTerm(term *ast.Term) *ast.Term {
	if term == nil {
		return nil
	}
	switch v := term.Value.(type) {
	case ast.Ref:
		for i, t := range v {
			v[i] = reconstructNestedTerm(t)
		}
	case ast.Call:
		for i, t := range v {
			v[i] = reconstructNestedTerm(t)
		}
	case *ast.Array:
		for i := range v.Len() {
			v.Set(i, reconstructNestedTerm(v.Elem(i)))
		}
	case ast.Set:
		next := make([]*ast.Term, 0, v.Len())
		changed := false
		v.Foreach(func(t *ast.Term) {
			nt := reconstructNestedTerm(t)
			if nt != t {
				changed = true
			}
			next = append(next, nt)
		})
		if changed {
			term.Value = ast.NewSet(next...)
		}
	case ast.Object:
		pairs := make([][2]*ast.Term, 0, v.Len())
		changed := false
		v.Foreach(func(k, val *ast.Term) {
			nk := reconstructNestedTerm(k)
			nv := reconstructNestedTerm(val)
			if nk != k || nv != val {
				changed = true
			}
			pairs = append(pairs, [2]*ast.Term{nk, nv})
		})
		if changed {
			term.Value = ast.NewObject(pairs...)
		}
	case *ast.SetComprehension:
		v.Body = reconstructTemplateStringBody(v.Body)
		v.Term = reconstructNestedTerm(v.Term)
	case *ast.ArrayComprehension:
		v.Body = reconstructTemplateStringBody(v.Body)
		v.Term = reconstructNestedTerm(v.Term)
		v.Term = liftTemplateTerm(v.Term, &v.Body)
	case *ast.ObjectComprehension:
		v.Body = reconstructTemplateStringBody(v.Body)
		v.Key = reconstructNestedTerm(v.Key)
		v.Value = reconstructNestedTerm(v.Value)
		v.Key = liftTemplateTerm(v.Key, &v.Body)
		v.Value = liftTemplateTerm(v.Value, &v.Body)
	}
	return term
}

func rewriteTemplateTerms(terms any, res *bindingResolver) any {
	switch ts := terms.(type) {
	case *ast.Term:
		return rewriteTemplateTerm(ts, res)
	case []*ast.Term:
		for i, t := range ts {
			ts[i] = rewriteTemplateTerm(t, res)
		}
		return ts
	case *ast.Every:
		ts.Domain = rewriteTemplateTerm(ts.Domain, res)
		ts.Key = rewriteTemplateTerm(ts.Key, res)
		ts.Value = rewriteTemplateTerm(ts.Value, res)
		return ts
	default:
		return terms
	}
}

func rewriteTemplateTerm(term *ast.Term, res *bindingResolver) *ast.Term {
	if term == nil {
		return nil
	}
	dec := &decode{res: res, seen: map[ast.Var]bool{}}
	next, ok := dec.rewriteTerm(term)
	if !ok {
		return term
	}
	dec.commit()
	return next
}

// rewriteTerm replaces template-string calls nested in term. Generated bindings
// are inlined only while decoding those calls, not for every expression in the body.
func (d *decode) rewriteTerm(term *ast.Term) (*ast.Term, bool) {
	switch v := term.Value.(type) {
	case ast.Call:
		if isTemplateOp(v) {
			return d.templateFromCall(v)
		}
		rebuilt, changed, ok := d.rewriteCall(v)
		if !ok {
			return term, false
		}
		if !changed {
			return term, true
		}
		return ast.NewTerm(rebuilt), true
	case ast.Ref:
		rebuilt, changed, ok := d.rewriteRef(v)
		if !ok || !changed {
			return term, ok
		}
		return ast.NewTerm(rebuilt), true
	case *ast.Array:
		rebuilt, changed, ok := d.rewriteArray(v)
		if !ok || !changed {
			return term, ok
		}
		return ast.NewTerm(rebuilt), true
	case ast.Set:
		rebuilt, changed, ok := d.rewriteSet(v)
		if !ok || !changed {
			return term, ok
		}
		return ast.NewTerm(rebuilt), true
	case ast.Object:
		rebuilt, changed, ok := d.rewriteObject(v)
		if !ok || !changed {
			return term, ok
		}
		return ast.NewTerm(rebuilt), true
	case *ast.ArrayComprehension:
		nt, ok := d.rewriteTerm(v.Term)
		if !ok {
			return term, false
		}
		v.Term = nt
		return term, true
	case *ast.SetComprehension:
		nt, ok := d.rewriteTerm(v.Term)
		if !ok {
			return term, false
		}
		v.Term = nt
		return term, true
	case *ast.ObjectComprehension:
		nk, ok := d.rewriteTerm(v.Key)
		if !ok {
			return term, false
		}
		nv, ok := d.rewriteTerm(v.Value)
		if !ok {
			return term, false
		}
		v.Key = nk
		v.Value = nv
		return term, true
	default:
		return term, true
	}
}

func (d *decode) rewriteCall(c ast.Call) (ast.Call, bool, bool) {
	out := make(ast.Call, len(c))
	changed := false
	for i, t := range c {
		nt, ok := d.rewriteTerm(t)
		if !ok {
			return nil, false, false
		}
		if nt != t {
			changed = true
		}
		out[i] = nt
	}
	return out, changed, true
}

func (d *decode) rewriteRef(ref ast.Ref) (ast.Ref, bool, bool) {
	out := make(ast.Ref, len(ref))
	changed := false
	for i, t := range ref {
		nt, ok := d.rewriteTerm(t)
		if !ok {
			return nil, false, false
		}
		if nt != t {
			changed = true
		}
		out[i] = nt
	}
	return out, changed, true
}

func (d *decode) rewriteArray(arr *ast.Array) (*ast.Array, bool, bool) {
	elems := make([]*ast.Term, arr.Len())
	changed := false
	for i := range arr.Len() {
		nt, ok := d.rewriteTerm(arr.Elem(i))
		if !ok {
			return nil, false, false
		}
		if nt != arr.Elem(i) {
			changed = true
		}
		elems[i] = nt
	}
	if !changed {
		return arr, false, true
	}
	return ast.NewArray(elems...), true, true
}

func (d *decode) rewriteSet(s ast.Set) (ast.Set, bool, bool) {
	elems := make([]*ast.Term, 0, s.Len())
	changed := false
	ok := true
	s.Foreach(func(t *ast.Term) {
		if !ok {
			return
		}
		nt, good := d.rewriteTerm(t)
		if !good {
			ok = false
			return
		}
		if nt != t {
			changed = true
		}
		elems = append(elems, nt)
	})
	if !ok {
		return nil, false, false
	}
	if !changed {
		return s, false, true
	}
	return ast.NewSet(elems...), true, true
}

func (d *decode) rewriteObject(obj ast.Object) (ast.Object, bool, bool) {
	pairs := make([][2]*ast.Term, 0, obj.Len())
	changed := false
	ok := true
	obj.Foreach(func(k, v *ast.Term) {
		if !ok {
			return
		}
		nk, okk := d.rewriteTerm(k)
		nv, okv := d.rewriteTerm(v)
		if !okk || !okv {
			ok = false
			return
		}
		if nk != k || nv != v {
			changed = true
		}
		pairs = append(pairs, [2]*ast.Term{nk, nv})
	})
	if !ok {
		return nil, false, false
	}
	if !changed {
		return obj, false, true
	}
	return ast.NewObject(pairs...), true, true
}

func liftTemplateHeadBindings(head *ast.Head, body *ast.Body) {
	if head == nil || body == nil {
		return
	}
	repl := templateAssignments(*body)
	if len(repl) == 0 {
		return
	}
	used := map[ast.Var]struct{}{}
	for v, rhs := range repl {
		if !headUsesVar(head, v) {
			continue
		}
		if countVarExcept(body, v, rhs.expr) > 0 {
			continue
		}
		used[v] = struct{}{}
	}
	if len(used) == 0 {
		return
	}
	head.Key = replaceTemplateVars(head.Key, repl, used)
	head.Value = replaceTemplateVars(head.Value, repl, used)
	for i, t := range head.Args {
		head.Args[i] = replaceTemplateVars(t, repl, used)
	}
	for i, t := range head.Reference {
		head.Reference[i] = replaceTemplateVars(t, repl, used)
	}
	filtered := make(ast.Body, 0, len(*body))
	for _, expr := range *body {
		drop := false
		for v := range used {
			if repl[v].expr == expr {
				drop = true
				break
			}
		}
		if !drop {
			filtered = append(filtered, expr)
		}
	}
	*body = filtered
	if len(*body) == 0 {
		*body = ast.NewBody(ast.NewExpr(ast.BooleanTerm(true)))
	}
}

type templateAssign struct {
	rhs  *ast.Term
	expr *ast.Expr
}

func templateAssignments(body ast.Body) map[ast.Var]templateAssign {
	out := map[ast.Var]templateAssign{}
	for _, expr := range body {
		if expr.Negated || len(expr.With) > 0 || !expr.IsEquality() {
			continue
		}
		left, right := expr.Operand(0), expr.Operand(1)
		if v, ok := generatedVar(left); ok && isTemplateStringTerm(right) {
			out[v] = templateAssign{rhs: right, expr: expr}
			continue
		}
		if v, ok := generatedVar(right); ok && isTemplateStringTerm(left) {
			out[v] = templateAssign{rhs: left, expr: expr}
		}
	}
	return out
}

func liftTemplateTerm(term *ast.Term, body *ast.Body) *ast.Term {
	v, ok := generatedVar(term)
	if !ok || body == nil {
		return term
	}
	repl := templateAssignments(*body)
	asg, ok := repl[v]
	if !ok || countVarExcept(body, v, asg.expr) > 0 {
		return term
	}
	filtered := make(ast.Body, 0, len(*body)-1)
	for _, expr := range *body {
		if expr != asg.expr {
			filtered = append(filtered, expr)
		}
	}
	if len(filtered) == 0 {
		filtered = ast.NewBody(ast.NewExpr(ast.BooleanTerm(true)))
	}
	*body = filtered
	return asg.rhs
}

func headUsesVar(head *ast.Head, v ast.Var) bool {
	if countVar(head.Key, v) > 0 || countVar(head.Value, v) > 0 || countVar(head.Args, v) > 0 {
		return true
	}
	return countVar(head.Reference, v) > 0
}

func replaceTemplateVars(term *ast.Term, repl map[ast.Var]templateAssign, used map[ast.Var]struct{}) *ast.Term {
	v, ok := generatedVar(term)
	if !ok {
		return term
	}
	if _, ok := used[v]; !ok {
		return term
	}
	return repl[v].rhs.Copy()
}

func countVarExcept(body *ast.Body, v ast.Var, skip *ast.Expr) int {
	n := 0
	for _, expr := range *body {
		if expr == skip {
			continue
		}
		n += countVar(expr, v)
	}
	return n
}

func containsTemplateCall(x any) bool {
	found := false
	ast.WalkExprs(x, func(expr *ast.Expr) bool {
		if isTemplateStringExpr(expr) {
			found = true
			return true
		}
		return false
	})
	if found {
		return true
	}
	ast.WalkTerms(x, func(term *ast.Term) bool {
		if isTemplateCallTerm(term) {
			found = true
			return true
		}
		return false
	})
	return found
}

func isTemplateStringExpr(expr *ast.Expr) bool {
	return expr != nil && expr.IsCall() && expr.Operator().Equal(templateStringRef)
}

func isTemplateCallTerm(term *ast.Term) bool {
	if term == nil {
		return false
	}
	c, ok := term.Value.(ast.Call)
	return ok && isTemplateOp(c)
}

func isTemplateStringTerm(term *ast.Term) bool {
	if term == nil {
		return false
	}
	_, ok := term.Value.(*ast.TemplateString)
	return ok
}

func isTemplateOp(c ast.Call) bool {
	if len(c) == 0 {
		return false
	}
	ref, ok := c[0].Value.(ast.Ref)
	return ok && ref.Equal(templateStringRef)
}

func templateCallOperands(expr *ast.Expr) (parts, out *ast.Term, ok bool) {
	if !isTemplateStringExpr(expr) {
		return nil, nil, false
	}
	ops := expr.Operands()
	switch len(ops) {
	case 1:
		return ops[0], nil, true
	case 2:
		return ops[0], ops[1], true
	default:
		return nil, nil, false
	}
}

func templateCallTermParts(term *ast.Term) *ast.Term {
	c, ok := term.Value.(ast.Call)
	if !ok || len(c) < 2 {
		return nil
	}
	return c[1]
}

func generatedVar(term *ast.Term) (ast.Var, bool) {
	if term == nil {
		return "", false
	}
	v, ok := term.Value.(ast.Var)
	if !ok || !v.IsGenerated() {
		return "", false
	}
	return v, true
}

func bareVar(expr *ast.Expr) (ast.Var, bool) {
	if expr == nil || expr.Negated || len(expr.With) > 0 {
		return "", false
	}
	t, ok := expr.Terms.(*ast.Term)
	if !ok {
		return "", false
	}
	v, ok := t.Value.(ast.Var)
	return v, ok
}

func isBareVar(expr *ast.Expr, v ast.Var) bool {
	bv, ok := bareVar(expr)
	return ok && bv == v
}

func countVar(x any, v ast.Var) int {
	if x == nil {
		return 0
	}
	n := 0
	ast.WalkVars(x, func(vv ast.Var) bool {
		if vv == v {
			n++
		}
		return false
	})
	return n
}

func exprFromTerm(src *ast.Expr, term *ast.Term) *ast.Expr {
	expr := ast.NewExpr(term)
	expr.With = copyWiths(src.With)
	expr.Negated = src.Negated
	expr.Location = src.Location
	return expr
}

func applyWith(node ast.Node, with []*ast.With) ast.Node {
	switch n := node.(type) {
	case *ast.Expr:
		cpy := n.Copy()
		cpy.With = append(cpy.With, copyWiths(with)...)
		return cpy
	case *ast.Term:
		expr := ast.NewExpr(n)
		expr.With = copyWiths(with)
		return expr
	default:
		return node
	}
}

func copyWiths(with []*ast.With) []*ast.With {
	if len(with) == 0 {
		return nil
	}
	out := make([]*ast.With, len(with))
	for i, w := range with {
		out[i] = w.Copy()
	}
	return out
}
