// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import (
	"github.com/open-policy-agent/opa/v1/ast"
)

// restorePartialTemplateStrings rewrites residual partial-evaluation output so
// internal.template_string calls become template-string terms again. Partial
// evaluation keeps the compiler's lowered form, including generated bindings
// and set-comprehension wrappers around interpolated expressions. Where those
// wrappers still describe a single Rego expression, the original template
// string is reconstructed and the intermediate bindings are dropped.
func restorePartialTemplateStrings(queries []ast.Body, support []*ast.Module) {
	for i := range queries {
		queries[i] = restoreTemplateStringsInBody(queries[i], nil)
	}
	for _, mod := range support {
		ast.WalkRules(mod, func(rule *ast.Rule) bool {
			restoreTemplateStringsInRule(rule)
			return false
		})
	}
}

func restoreTemplateStringsInRule(rule *ast.Rule) {
	if rule == nil {
		return
	}
	used := ast.NewVarSet()
	if rule.Head != nil {
		ast.WalkVars(rule.Head, func(v ast.Var) bool {
			used.Add(v)
			return false
		})
	}
	rule.Body = restoreTemplateStringsInBody(rule.Body, used)
}

func restoreTemplateStringsInBody(body ast.Body, usedOutside ast.VarSet) ast.Body {
	if len(body) == 0 {
		return body
	}
	for _, expr := range body {
		restoreNestedTemplateStrings(expr)
	}
	binds := templateBindings(body)
	for i, expr := range body {
		if repl, ok := restoreTemplateStringExpr(expr, binds); ok {
			body[i] = repl
		}
	}
	body = dropTemplateSupportBindings(body, usedOutside)
	return foldTemplateAliases(body, usedOutside)
}

func restoreNestedTemplateStrings(expr *ast.Expr) {
	if expr == nil {
		return
	}
	for _, w := range expr.With {
		if w == nil {
			continue
		}
		restoreNestedTemplateTerm(w.Target)
		restoreNestedTemplateTerm(w.Value)
	}
	switch terms := expr.Terms.(type) {
	case *ast.Term:
		restoreNestedTemplateTerm(terms)
	case []*ast.Term:
		for _, term := range terms {
			restoreNestedTemplateTerm(term)
		}
	case *ast.Every:
		restoreNestedTemplateTerm(terms.Domain)
		terms.Body = restoreTemplateStringsInBody(terms.Body, everyDefinedVars(terms))
	}
}

func everyDefinedVars(ev *ast.Every) ast.VarSet {
	used := ast.NewVarSet()
	if ev == nil {
		return used
	}
	if ev.Key != nil {
		ast.WalkVars(ev.Key, func(v ast.Var) bool {
			used.Add(v)
			return false
		})
	}
	if ev.Value != nil {
		ast.WalkVars(ev.Value, func(v ast.Var) bool {
			used.Add(v)
			return false
		})
	}
	return used
}

func restoreNestedTemplateTerm(term *ast.Term) {
	if term == nil {
		return
	}
	switch v := term.Value.(type) {
	case ast.Ref:
		for _, elem := range v {
			restoreNestedTemplateTerm(elem)
		}
	case ast.Call:
		for _, elem := range v {
			restoreNestedTemplateTerm(elem)
		}
	case *ast.Array:
		for i := range v.Len() {
			restoreNestedTemplateTerm(v.Elem(i))
		}
	case ast.Object:
		v.Foreach(func(k, val *ast.Term) {
			restoreNestedTemplateTerm(k)
			restoreNestedTemplateTerm(val)
		})
	case ast.Set:
		v.Foreach(restoreNestedTemplateTerm)
	case *ast.SetComprehension:
		used := varsOf(v.Term)
		v.Body = restoreTemplateStringsInBody(v.Body, used)
		restoreNestedTemplateTerm(v.Term)
	case *ast.ArrayComprehension:
		used := varsOf(v.Term)
		v.Body = restoreTemplateStringsInBody(v.Body, used)
		restoreNestedTemplateTerm(v.Term)
	case *ast.ObjectComprehension:
		used := varsOf(v.Key)
		used.Update(varsOf(v.Value))
		v.Body = restoreTemplateStringsInBody(v.Body, used)
		restoreNestedTemplateTerm(v.Key)
		restoreNestedTemplateTerm(v.Value)
	case *ast.TemplateString:
		for _, part := range v.Parts {
			switch part := part.(type) {
			case *ast.Term:
				restoreNestedTemplateTerm(part)
			case *ast.Expr:
				restoreNestedTemplateStrings(part)
			}
		}
	}
}

func varsOf(term *ast.Term) ast.VarSet {
	used := ast.NewVarSet()
	if term == nil {
		return used
	}
	ast.WalkVars(term, func(v ast.Var) bool {
		used.Add(v)
		return false
	})
	return used
}

type templateBind struct {
	value *ast.Term
	with  []*ast.With
}

func templateBindings(body ast.Body) map[ast.Var]*templateBind {
	binds := make(map[ast.Var]*templateBind)
	for _, expr := range body {
		if v, value, with, ok := generatedEquality(expr); ok {
			binds[v] = &templateBind{value: value, with: with}
		}
	}
	for _, expr := range body {
		v, value, with, ok := generatedCallOutput(expr)
		if !ok {
			continue
		}
		if _, exists := binds[v]; exists {
			continue
		}
		binds[v] = &templateBind{value: value, with: with}
	}
	return binds
}

func generatedEquality(expr *ast.Expr) (ast.Var, *ast.Term, []*ast.With, bool) {
	if expr == nil || expr.Negated || (!expr.IsEquality() && !expr.IsAssignment()) || len(expr.Operands()) != 2 {
		return "", nil, nil, false
	}
	left := expr.Operand(0)
	v, ok := left.Value.(ast.Var)
	if !ok || !v.IsGenerated() {
		return "", nil, nil, false
	}
	return v, expr.Operand(1), expr.With, true
}

func generatedCallOutput(expr *ast.Expr) (ast.Var, *ast.Term, []*ast.With, bool) {
	if expr == nil || expr.Negated || !expr.IsCall() || expr.IsEquality() || expr.IsAssignment() {
		return "", nil, nil, false
	}
	ops := expr.Operands()
	if len(ops) == 0 {
		return "", nil, nil, false
	}
	v, ok := ops[len(ops)-1].Value.(ast.Var)
	if !ok || !v.IsGenerated() {
		return "", nil, nil, false
	}
	terms := expr.Terms.([]*ast.Term)
	call := ast.Call(append([]*ast.Term(nil), terms[:len(terms)-1]...))
	return v, ast.NewTerm(call).SetLocation(expr.Location), expr.With, true
}

func restoreTemplateStringExpr(expr *ast.Expr, binds map[ast.Var]*templateBind) (*ast.Expr, bool) {
	if expr == nil {
		return nil, false
	}
	// After a restored module is compiled again, the call is a term whose
	// value is the call, rather than a call expression with an output var.
	if term, ok := expr.Terms.(*ast.Term); ok {
		if ts, ok := templateStringCallTerm(term, binds); ok {
			repl := ast.NewExpr(ts)
			repl.Location = expr.Location
			repl.Negated = expr.Negated
			repl.With = expr.With
			return repl, true
		}
	}
	if expr.IsCall() && isTemplateStringOperator(expr.OperatorTerm()) {
		ts, ok := buildTemplateString(expr.Operand(0), binds)
		if !ok {
			return nil, false
		}
		var repl *ast.Expr
		if out := expr.Operand(1); out != nil {
			repl = ast.Equality.Expr(out, ts)
		} else {
			repl = ast.NewExpr(ts)
		}
		repl.Location = expr.Location
		repl.Negated = expr.Negated
		repl.With = expr.With
		return repl, true
	}
	if (!expr.IsEquality() && !expr.IsAssignment()) || len(expr.Operands()) != 2 {
		return nil, false
	}
	terms := expr.Terms.([]*ast.Term)
	for _, idx := range []int{1, 2} {
		ts, ok := templateStringCallTerm(terms[idx], binds)
		if !ok {
			continue
		}
		terms[idx] = ts
		return expr, true
	}
	return nil, false
}

func templateStringCallTerm(term *ast.Term, binds map[ast.Var]*templateBind) (*ast.Term, bool) {
	call, ok := term.Value.(ast.Call)
	if !ok || len(call) != 2 || !isTemplateStringOperator(call[0]) {
		return nil, false
	}
	return buildTemplateString(call[1], binds)
}

func isTemplateStringOperator(term *ast.Term) bool {
	if term == nil {
		return false
	}
	ref, ok := term.Value.(ast.Ref)
	return ok && ref.Equal(ast.InternalTemplateString.Ref())
}

func buildTemplateString(partsTerm *ast.Term, binds map[ast.Var]*templateBind) (*ast.Term, bool) {
	// Follow generated aliases of the parts array, but resolve each element on
	// its own. An element may carry a with modifier, which a term cannot.
	partsTerm, with, ok := derefGenerated(partsTerm, binds, map[ast.Var]struct{}{})
	if !ok || len(with) > 0 {
		return nil, false
	}
	arr, ok := partsTerm.Value.(*ast.Array)
	if !ok {
		return nil, false
	}
	parts := make([]ast.Node, 0, arr.Len())
	seen := map[ast.Var]struct{}{}
	for i := range arr.Len() {
		part, ok := resolveTemplatePart(arr.Elem(i), binds, seen)
		if !ok || containsTemplateStringBuiltin(part) {
			return nil, false
		}
		parts = appendTemplatePart(parts, part)
	}
	return ast.TemplateStringTerm(false, parts...).SetLocation(partsTerm.Location), true
}

func appendTemplatePart(parts []ast.Node, part ast.Node) []ast.Node {
	term, ok := part.(*ast.Term)
	if !ok {
		return append(parts, part)
	}
	str, ok := term.Value.(ast.String)
	if !ok || len(parts) == 0 {
		return append(parts, part)
	}
	prev, ok := parts[len(parts)-1].(*ast.Term)
	if !ok {
		return append(parts, part)
	}
	prevStr, ok := prev.Value.(ast.String)
	if !ok {
		return append(parts, part)
	}
	parts[len(parts)-1] = ast.StringTerm(string(prevStr) + string(str)).SetLocation(prev.Location)
	return parts
}

func resolveTemplatePart(term *ast.Term, binds map[ast.Var]*templateBind, seen map[ast.Var]struct{}) (ast.Node, bool) {
	// Each part gets its own cycle set so a generated binding reused by two
	// interpolations is not treated as a loop.
	partSeen := make(map[ast.Var]struct{}, len(seen))
	for v := range seen {
		partSeen[v] = struct{}{}
	}
	seen = partSeen
	term, with, ok := derefGenerated(term, binds, seen)
	if !ok {
		return nil, false
	}
	// Expressions that are not a bare var or ref are captured in a set
	// comprehension. The comprehension's value is the interpolation, including
	// when that value is itself a set or object.
	if sc, ok := term.Value.(*ast.SetComprehension); ok {
		expr, ok := setComprehensionExpr(sc, binds, seen)
		if !ok || containsTemplateStringBuiltin(expr) {
			return nil, false
		}
		if len(with) > 0 {
			if len(expr.With) > 0 {
				return nil, false
			}
			expr.With = with
		}
		return expr, true
	}

	inlined, with2, ok := inlineTemplateTerm(term, binds, seen)
	if !ok {
		return nil, false
	}
	if len(with) > 0 {
		if len(with2) > 0 {
			return nil, false
		}
		with2 = with
	}
	if len(with2) > 0 {
		return exprFromTerm(inlined, with2), true
	}
	switch v := inlined.Value.(type) {
	case ast.String, ast.Number, ast.Boolean, ast.Null:
		return inlined, true
	case ast.Set:
		// A singleton set placed directly in the parts array is the compiler's
		// wrapper (or a set comprehension partial evaluation already reduced).
		// The element is the interpolated value. Sets that are themselves the
		// interpolated value stay inside a set comprehension and are handled
		// above, so they are not unwrapped here.
		elems := v.Slice()
		if len(elems) == 1 {
			return exprFromTerm(elems[0], nil), true
		}
		return exprFromTerm(inlined, nil), true
	default:
		return exprFromTerm(inlined, nil), true
	}
}

// derefGenerated follows compiler-generated variable aliases until the term is
// no longer a generated var. with modifiers attached to those aliases are
// returned alongside the value.
func derefGenerated(term *ast.Term, binds map[ast.Var]*templateBind, seen map[ast.Var]struct{}) (*ast.Term, []*ast.With, bool) {
	var with []*ast.With
	for {
		v, ok := term.Value.(ast.Var)
		if !ok || !v.IsGenerated() {
			return term, with, true
		}
		bind, ok := binds[v]
		if !ok {
			return term, with, true
		}
		if _, loop := seen[v]; loop {
			return nil, nil, false
		}
		seen[v] = struct{}{}
		if len(bind.with) > 0 {
			if len(with) > 0 {
				return nil, nil, false
			}
			with = bind.with
		}
		term = bind.value
		if _, isVar := term.Value.(ast.Var); !isVar {
			return term, with, true
		}
	}
}

func exprFromTerm(term *ast.Term, with []*ast.With) *ast.Expr {
	var expr *ast.Expr
	if call, ok := term.Value.(ast.Call); ok {
		expr = ast.NewExpr([]*ast.Term(call))
	} else {
		expr = ast.NewExpr(term)
	}
	expr.With = with
	expr.Location = term.Location
	return expr
}

func inlineTemplateTerm(term *ast.Term, binds map[ast.Var]*templateBind, seen map[ast.Var]struct{}) (*ast.Term, []*ast.With, bool) {
	if term == nil {
		return nil, nil, false
	}
	if v, ok := term.Value.(ast.Var); ok && v.IsGenerated() {
		bind, ok := binds[v]
		if !ok {
			return term, nil, true
		}
		if _, loop := seen[v]; loop {
			return nil, nil, false
		}
		seen[v] = struct{}{}
		defer delete(seen, v)
		inlined, with, ok := inlineTemplateTerm(bind.value, binds, seen)
		if !ok {
			return nil, nil, false
		}
		if len(bind.with) > 0 {
			if len(with) > 0 {
				return nil, nil, false
			}
			with = bind.with
		}
		return inlined, with, true
	}

	switch v := term.Value.(type) {
	case ast.Ref:
		ref := make(ast.Ref, len(v))
		for i, elem := range v {
			next, with, ok := inlineTemplateTerm(elem, binds, seen)
			if !ok || len(with) > 0 {
				return nil, nil, false
			}
			ref[i] = next
		}
		return ast.NewTerm(ref).SetLocation(term.Location), nil, true
	case *ast.Array:
		arr := ast.NewArray()
		for i := range v.Len() {
			next, with, ok := inlineTemplateTerm(v.Elem(i), binds, seen)
			if !ok || len(with) > 0 {
				return nil, nil, false
			}
			arr = arr.Append(next)
		}
		return ast.NewTerm(arr).SetLocation(term.Location), nil, true
	case ast.Set:
		set := ast.NewSet()
		for _, elem := range v.Slice() {
			next, with, ok := inlineTemplateTerm(elem, binds, seen)
			if !ok || len(with) > 0 {
				return nil, nil, false
			}
			set.Add(next)
		}
		return ast.NewTerm(set).SetLocation(term.Location), nil, true
	case ast.Object:
		obj := ast.NewObject()
		failed := false
		v.Foreach(func(k, val *ast.Term) {
			if failed {
				return
			}
			nk, w1, ok1 := inlineTemplateTerm(k, binds, seen)
			nv, w2, ok2 := inlineTemplateTerm(val, binds, seen)
			if !ok1 || !ok2 || len(w1) > 0 || len(w2) > 0 {
				failed = true
				return
			}
			obj.Insert(nk, nv)
		})
		if failed {
			return nil, nil, false
		}
		return ast.NewTerm(obj).SetLocation(term.Location), nil, true
	case ast.Call:
		call := make(ast.Call, len(v))
		for i, elem := range v {
			next, with, ok := inlineTemplateTerm(elem, binds, seen)
			if !ok || len(with) > 0 {
				return nil, nil, false
			}
			call[i] = next
		}
		return ast.NewTerm(call).SetLocation(term.Location), nil, true
	case *ast.SetComprehension:
		expr, ok := setComprehensionExpr(v, binds, seen)
		if !ok {
			if containsTemplateStringBuiltin(v) {
				return nil, nil, false
			}
			return term, nil, true
		}
		return termFromExpr(expr)
	default:
		return term, nil, true
	}
}

func termFromExpr(expr *ast.Expr) (*ast.Term, []*ast.With, bool) {
	switch terms := expr.Terms.(type) {
	case *ast.Term:
		return terms, expr.With, true
	case []*ast.Term:
		return ast.NewTerm(ast.Call(terms)).SetLocation(expr.Location), expr.With, true
	default:
		return nil, nil, false
	}
}

func setComprehensionExpr(sc *ast.SetComprehension, outer map[ast.Var]*templateBind, seen map[ast.Var]struct{}) (*ast.Expr, bool) {
	if sc == nil {
		return nil, false
	}
	local := templateBindings(sc.Body)
	combined := make(map[ast.Var]*templateBind, len(outer)+len(local))
	for v, bind := range outer {
		combined[v] = bind
	}
	for v, bind := range local {
		combined[v] = bind
	}
	if !comprehensionBodyAccounted(sc.Body, local) {
		if containsTemplateStringBuiltin(sc) {
			return nil, false
		}
		return ast.NewExpr(ast.NewTerm(sc)), true
	}
	out, ok := sc.Term.Value.(ast.Var)
	if !ok {
		if len(sc.Body) > 0 {
			return ast.NewExpr(ast.NewTerm(sc)), true
		}
		inlined, with, ok := inlineTemplateTerm(sc.Term, combined, seen)
		if !ok {
			return nil, false
		}
		return exprFromTerm(inlined, with), true
	}
	bind, ok := combined[out]
	if !ok {
		return ast.NewExpr(ast.NewTerm(sc)), true
	}
	inlined, with, ok := inlineTemplateTerm(bind.value, combined, seen)
	if !ok {
		return nil, false
	}
	if len(bind.with) > 0 {
		if len(with) > 0 {
			return nil, false
		}
		with = bind.with
	}
	return exprFromTerm(inlined, with), true
}

func comprehensionBodyAccounted(body ast.Body, binds map[ast.Var]*templateBind) bool {
	for _, expr := range body {
		if expr == nil || expr.Negated {
			return false
		}
		if v, _, _, ok := generatedEquality(expr); ok {
			if _, defined := binds[v]; defined {
				continue
			}
		}
		if v, _, _, ok := generatedCallOutput(expr); ok {
			if _, defined := binds[v]; defined {
				continue
			}
		}
		return false
	}
	return true
}

func dropTemplateSupportBindings(body ast.Body, usedOutside ast.VarSet) ast.Body {
	for {
		counts := varCounts(body)
		next := make(ast.Body, 0, len(body))
		dropped := false
		for _, expr := range body {
			v, ok := supportBindingVar(expr)
			if ok && counts[v] == 1 && !usedOutside.Contains(v) {
				dropped = true
				continue
			}
			next = append(next, expr)
		}
		body = next
		if !dropped {
			return body
		}
	}
}

func supportBindingVar(expr *ast.Expr) (ast.Var, bool) {
	v, value, with, ok := generatedEquality(expr)
	if !ok || len(with) > 0 {
		return "", false
	}
	switch val := value.Value.(type) {
	case ast.Set, *ast.SetComprehension:
		return v, true
	case ast.Var:
		if val.IsGenerated() {
			return v, true
		}
	}
	return "", false
}

func foldTemplateAliases(body ast.Body, usedOutside ast.VarSet) ast.Body {
	for {
		counts := varCounts(body)
		folded := false
		for i, expr := range body {
			v, value, with, ok := generatedEquality(expr)
			if !ok || len(with) > 0 || usedOutside.Contains(v) || counts[v] != 2 {
				continue
			}
			if _, ok := value.Value.(*ast.TemplateString); !ok {
				continue
			}
			if !replaceVarTerm(body, i, v, value) {
				continue
			}
			body = append(body[:i], body[i+1:]...)
			folded = true
			break
		}
		if !folded {
			return body
		}
	}
}

func replaceVarTerm(body ast.Body, skip int, v ast.Var, repl *ast.Term) bool {
	replaced := false
	for i, expr := range body {
		if i == skip {
			continue
		}
		ast.WalkTerms(expr, func(term *ast.Term) bool {
			if replaced || term == nil {
				return replaced
			}
			if tv, ok := term.Value.(ast.Var); ok && tv == v {
				cpy := repl.Copy()
				term.Value = cpy.Value
				term.Location = cpy.Location
				replaced = true
				return true
			}
			return false
		})
		if replaced {
			return true
		}
	}
	return false
}

func varCounts(body ast.Body) map[ast.Var]int {
	counts := map[ast.Var]int{}
	ast.WalkVars(body, func(v ast.Var) bool {
		counts[v]++
		return false
	})
	return counts
}

func containsTemplateStringBuiltin(x any) bool {
	found := false
	ast.WalkExprs(x, func(expr *ast.Expr) bool {
		if expr.IsCall() && isTemplateStringOperator(expr.OperatorTerm()) {
			found = true
			return true
		}
		return false
	})
	if found {
		return true
	}
	ast.WalkTerms(x, func(term *ast.Term) bool {
		call, ok := term.Value.(ast.Call)
		if ok && len(call) > 0 && isTemplateStringOperator(call[0]) {
			found = true
			return true
		}
		return false
	})
	return found
}
