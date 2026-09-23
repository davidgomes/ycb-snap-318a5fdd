// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import "github.com/open-policy-agent/opa/v1/ast"

// internalTemplateStringRef is the compiler name of the template-string builtin.
// Partial evaluation leaves calls to that builtin, plus generated bindings the
// compiler inserted while lowering interpolations, in residual queries and
// support modules. Reconstruction turns those calls back into template-string
// terms when the residual is still representable as ordinary Rego.
var internalTemplateStringRef = ast.InternalTemplateString.Ref()

// templateStringEnv records generated bindings that reconstruction has removed
// from a body. Parts are set-wrapped interpolation values (the compiler wraps
// every non-literal part in a singleton set or set comprehension). Values are
// the strings produced by internal.template_string itself.
type templateStringEnv struct {
	parts  map[ast.Var]*ast.Expr
	values map[ast.Var]*ast.Expr
}

func newTemplateStringEnv() templateStringEnv {
	return templateStringEnv{
		parts:  map[ast.Var]*ast.Expr{},
		values: map[ast.Var]*ast.Expr{},
	}
}

func (e templateStringEnv) copy() templateStringEnv {
	out := newTemplateStringEnv()
	for k, v := range e.parts {
		out.parts[k] = v
	}
	for k, v := range e.values {
		out.values[k] = v
	}
	return out
}

func reconstructPartialTemplateStrings(pq *PartialQueries) {
	if pq == nil {
		return
	}
	for i, query := range pq.Queries {
		if !containsInternalTemplateString(query) {
			continue
		}
		pq.Queries[i] = reconstructTemplateStringQuery(query)
	}
	for i, mod := range pq.Support {
		if mod == nil || !containsInternalTemplateString(mod) {
			continue
		}
		pq.Support[i] = reconstructTemplateStringModule(mod)
	}
}

func reconstructTemplateStringQuery(body ast.Body) ast.Body {
	rewritten, _ := reconstructTemplateStringBody(body, nil, newTemplateStringEnv())
	// A partial rewrite can drop a generated binding that a remaining
	// internal.template_string call still needs. Keep the original query then.
	if containsInternalTemplateString(rewritten) {
		return body
	}
	if len(rewritten) == 0 {
		return ast.Body{}
	}
	return rewritten
}

func reconstructTemplateStringModule(mod *ast.Module) *ast.Module {
	cpy := mod.Copy()
	for i, rule := range cpy.Rules {
		cpy.Rules[i] = reconstructTemplateStringRule(rule)
		setRuleModule(cpy.Rules[i], cpy)
	}
	return cpy
}

func setRuleModule(rule *ast.Rule, mod *ast.Module) {
	for rule != nil {
		rule.Module = mod
		rule = rule.Else
	}
}

func reconstructTemplateStringRule(rule *ast.Rule) *ast.Rule {
	if rule == nil {
		return nil
	}
	// Copy first so Else is preserved, then rebuild Else from the copy so each
	// branch gets its own binding environment.
	original := rule.Copy()
	cpy := original.Copy()
	elseRule := cpy.Else
	cpy.Else = nil

	body, env := reconstructTemplateStringBody(cpy.Body, cpy.Head, newTemplateStringEnv())
	if len(body) == 0 {
		body = ast.NewBody(ast.NewExpr(ast.BooleanTerm(true)))
	}
	cpy.Body = body
	applyTemplateStringHead(cpy.Head, env)
	// Same fallback as queries: an incomplete rewrite must not drop bindings a
	// remaining internal.template_string call still references.
	if containsInternalTemplateString(cpy.Head) || containsInternalTemplateString(cpy.Body) {
		cpy = original.Copy()
		cpy.Else = nil
	}
	if elseRule != nil {
		cpy.Else = reconstructTemplateStringRule(elseRule)
	}
	return cpy
}

func applyTemplateStringHead(head *ast.Head, env templateStringEnv) {
	if head == nil {
		return
	}
	if head.Key != nil {
		head.Key = reconstructTemplateStringTerm(head.Key, env)
	}
	if head.Value != nil {
		head.Value = reconstructTemplateStringTerm(head.Value, env)
	}
	for i := range head.Args {
		head.Args[i] = reconstructTemplateStringTerm(head.Args[i], env)
	}
	for i := range head.Reference {
		head.Reference[i] = reconstructTemplateStringTerm(head.Reference[i], env)
	}
}

func reconstructTemplateStringBody(body ast.Body, head *ast.Head, parent templateStringEnv) (ast.Body, templateStringEnv) {
	env := parent.copy()
	current := body
	// Drop generated template-string scaffolding before rewriting the calls
	// that remain. Bindings are removed only once every remaining use is a
	// template-string part, and a later binding can unlock an earlier call, so
	// repeat until a pass removes nothing.
	for {
		next := make(ast.Body, 0, len(current))
		removed := false
		for i, expr := range current {
			rest := make(ast.Body, 0, len(next)+len(current)-i-1)
			rest = append(rest, next...)
			rest = append(rest, current[i+1:]...)
			binding, ok := templateStringGeneratedBinding(expr, rest, head, env)
			if !ok {
				next = append(next, expr)
				continue
			}
			if binding.part {
				env.parts[binding.name] = binding.expr
			} else {
				env.values[binding.name] = binding.expr
			}
			removed = true
		}
		current = next
		if !removed {
			break
		}
	}

	result := make(ast.Body, 0, len(current))
	for _, expr := range current {
		result = append(result, reconstructTemplateStringExpr(expr, env))
	}
	return result, env
}

type templateStringBinding struct {
	name ast.Var
	expr *ast.Expr
	part bool
}

func templateStringGeneratedBinding(expr *ast.Expr, rest ast.Body, head *ast.Head, env templateStringEnv) (templateStringBinding, bool) {
	if binding, ok := templateStringEqualityBinding(expr, rest, head, env); ok {
		return binding, true
	}
	return templateStringCallBinding(expr, env)
}

func templateStringEqualityBinding(expr *ast.Expr, rest ast.Body, head *ast.Head, env templateStringEnv) (templateStringBinding, bool) {
	name, rhs, ok := generatedVarBinding(expr)
	if !ok {
		return templateStringBinding{}, false
	}
	part, decoded, ok := decodeTemplateStringBindingRHS(rhs, env)
	if !ok {
		return templateStringBinding{}, false
	}
	if part && !onlyTemplateStringPartUses(rest, head, name) {
		return templateStringBinding{}, false
	}
	// with modifiers sit on the generated equality after compilation. Keep them
	// on the decoded interpolation so the reconstructed expression still applies
	// the override.
	if len(expr.With) > 0 {
		decoded = decoded.Copy()
		decoded.With = append(cloneWithModifiers(decoded.With, env), cloneWithModifiers(expr.With, env)...)
	}
	return templateStringBinding{name: name, expr: decoded, part: part}, true
}

func templateStringCallBinding(expr *ast.Expr, env templateStringEnv) (templateStringBinding, bool) {
	terms, ok := expr.Terms.([]*ast.Term)
	if !ok || !isInternalTemplateStringCall(terms) || len(terms) != 3 {
		return templateStringBinding{}, false
	}
	out, ok := terms[2].Value.(ast.Var)
	if !ok || !out.IsGenerated() || len(expr.With) > 0 || expr.Negated {
		return templateStringBinding{}, false
	}
	tmpl, ok := templateStringTermFromParts(terms[1], env)
	if !ok {
		return templateStringBinding{}, false
	}
	return templateStringBinding{
		name: out,
		expr: ast.NewExpr(tmpl),
		part: false,
	}, true
}

func reconstructTemplateStringExpr(expr *ast.Expr, env templateStringEnv) *ast.Expr {
	if expr == nil {
		return nil
	}
	if rewritten, ok := rewriteResidualTemplateStringCall(expr, env); ok {
		return rewritten
	}

	cpy := expr.Copy()
	switch ts := cpy.Terms.(type) {
	case []*ast.Term:
		for i := range ts {
			ts[i] = reconstructTemplateStringTerm(ts[i], env)
		}
	case *ast.Term:
		cpy.Terms = reconstructTemplateStringTerm(ts, env)
	case *ast.Every:
		cpy.Terms = reconstructEvery(ts, env)
	case *ast.SomeDecl:
		decl := ts.Copy()
		for i := range decl.Symbols {
			decl.Symbols[i] = reconstructTemplateStringTerm(decl.Symbols[i], env)
		}
		cpy.Terms = decl
	}
	cpy.With = cloneWithModifiers(cpy.With, env)
	return cpy
}

// rewriteResidualTemplateStringCall turns a call that cannot be dropped into
// an equality that binds its output to a template string. Calls with no output
// become the template-string expression itself.
func rewriteResidualTemplateStringCall(expr *ast.Expr, env templateStringEnv) (*ast.Expr, bool) {
	terms, ok := expr.Terms.([]*ast.Term)
	if !ok || !isInternalTemplateStringCall(terms) || len(terms) < 2 {
		return nil, false
	}
	tmpl, ok := templateStringTermFromParts(terms[1], env)
	if !ok {
		return nil, false
	}
	var rewritten *ast.Expr
	switch len(terms) {
	case 2:
		rewritten = ast.NewExpr(tmpl)
	case 3:
		rewritten = ast.Equality.Expr(terms[2].Copy(), tmpl)
	default:
		return nil, false
	}
	rewritten.Location = expr.Location
	rewritten.Negated = expr.Negated
	rewritten.Generated = expr.Generated
	rewritten.With = cloneWithModifiers(expr.With, env)
	return rewritten, true
}

func reconstructEvery(every *ast.Every, env templateStringEnv) *ast.Every {
	cpy := every.Copy()
	if cpy.Key != nil {
		cpy.Key = reconstructTemplateStringTerm(cpy.Key, env)
	}
	cpy.Value = reconstructTemplateStringTerm(cpy.Value, env)
	cpy.Domain = reconstructTemplateStringTerm(cpy.Domain, env)
	body, _ := reconstructTemplateStringBody(cpy.Body, nil, env)
	if len(body) == 0 {
		body = ast.NewBody(ast.NewExpr(ast.BooleanTerm(true)))
	}
	cpy.Body = body
	return cpy
}

func reconstructTemplateStringTerm(term *ast.Term, env templateStringEnv) *ast.Term {
	if term == nil {
		return nil
	}
	if v, ok := term.Value.(ast.Var); ok {
		if expr, ok := env.values[v]; ok && len(expr.With) == 0 && !expr.Negated {
			if replaced, ok := exprToTerm(expr); ok {
				cpy := replaced.Copy()
				if cpy.Location == nil {
					cpy.Location = term.Location
				}
				return cpy
			}
		}
		return term.Copy()
	}

	out := *term
	out.Value = reconstructTemplateStringValue(term.Value, env)
	return &out
}

func reconstructTemplateStringValue(value ast.Value, env templateStringEnv) ast.Value {
	switch v := value.(type) {
	case ast.Null, ast.Boolean, ast.Number, ast.String, ast.Var:
		return v
	case ast.Ref:
		return reconstructRef(v, env)
	case ast.Call:
		if isInternalTemplateStringCall([]*ast.Term(v)) {
			if tmpl, ok := templateStringTermFromParts(v[1], env); ok && len(v) == 2 {
				return tmpl.Value
			}
		}
		out := make(ast.Call, len(v))
		for i := range v {
			out[i] = reconstructTemplateStringTerm(v[i], env)
		}
		return out
	case *ast.Array:
		out := make([]*ast.Term, v.Len())
		for i := range v.Len() {
			out[i] = reconstructTemplateStringTerm(v.Elem(i), env)
		}
		return ast.NewArray(out...)
	case ast.Set:
		elems := v.Slice()
		out := make([]*ast.Term, len(elems))
		for i := range elems {
			out[i] = reconstructTemplateStringTerm(elems[i], env)
		}
		return ast.NewSet(out...)
	case ast.Object:
		pairs := make([][2]*ast.Term, 0, v.Len())
		v.Foreach(func(k, val *ast.Term) {
			pairs = append(pairs, ast.Item(
				reconstructTemplateStringTerm(k, env),
				reconstructTemplateStringTerm(val, env),
			))
		})
		return ast.NewObject(pairs...)
	case *ast.ArrayComprehension:
		return reconstructArrayComprehension(v, env)
	case *ast.ObjectComprehension:
		return reconstructObjectComprehension(v, env)
	case *ast.SetComprehension:
		return reconstructSetComprehension(v, env)
	case *ast.TemplateString:
		parts := make([]ast.Node, len(v.Parts))
		for i, part := range v.Parts {
			parts[i] = reconstructTemplateStringPartNode(part, env)
		}
		return &ast.TemplateString{MultiLine: v.MultiLine, Parts: parts}
	default:
		return value
	}
}

func reconstructRef(ref ast.Ref, env templateStringEnv) ast.Ref {
	if len(ref) == 0 {
		return ast.Ref{}
	}
	out := make(ast.Ref, len(ref))
	for i := range ref {
		if i == 0 {
			out[i] = reconstructRefHead(ref[i], env)
			continue
		}
		out[i] = reconstructTemplateStringTerm(ref[i], env)
	}
	if head, ok := out[0].Value.(ast.Ref); ok {
		flat := make(ast.Ref, 0, len(head)+len(out)-1)
		flat = append(flat, head...)
		flat = append(flat, out[1:]...)
		return flat
	}
	return out
}

func reconstructRefHead(term *ast.Term, env templateStringEnv) *ast.Term {
	v, ok := term.Value.(ast.Var)
	if !ok {
		return reconstructTemplateStringTerm(term, env)
	}
	expr, ok := env.values[v]
	if !ok || len(expr.With) > 0 || expr.Negated {
		return term.Copy()
	}
	replaced, ok := exprToTerm(expr)
	if !ok {
		return term.Copy()
	}
	switch replaced.Value.(type) {
	case ast.Var, ast.Ref:
		cpy := replaced.Copy()
		if cpy.Location == nil {
			cpy.Location = term.Location
		}
		return cpy
	default:
		return term.Copy()
	}
}

func reconstructArrayComprehension(comp *ast.ArrayComprehension, env templateStringEnv) *ast.ArrayComprehension {
	body, inner := reconstructTemplateStringBody(comp.Body, nil, env)
	body = bodyOrTrue(body)
	return &ast.ArrayComprehension{
		Term: reconstructTemplateStringTerm(comp.Term, inner),
		Body: body,
	}
}

func reconstructObjectComprehension(comp *ast.ObjectComprehension, env templateStringEnv) *ast.ObjectComprehension {
	body, inner := reconstructTemplateStringBody(comp.Body, nil, env)
	body = bodyOrTrue(body)
	return &ast.ObjectComprehension{
		Key:   reconstructTemplateStringTerm(comp.Key, inner),
		Value: reconstructTemplateStringTerm(comp.Value, inner),
		Body:  body,
	}
}

func reconstructSetComprehension(comp *ast.SetComprehension, env templateStringEnv) *ast.SetComprehension {
	body, inner := reconstructTemplateStringBody(comp.Body, nil, env)
	body = bodyOrTrue(body)
	return &ast.SetComprehension{
		Term: reconstructTemplateStringTerm(comp.Term, inner),
		Body: body,
	}
}

func bodyOrTrue(body ast.Body) ast.Body {
	if len(body) == 0 {
		return ast.NewBody(ast.NewExpr(ast.BooleanTerm(true)))
	}
	return body
}

func reconstructTemplateStringPartNode(part ast.Node, env templateStringEnv) ast.Node {
	switch p := part.(type) {
	case *ast.Expr:
		return reconstructTemplateStringExpr(p, env)
	case *ast.Term:
		return reconstructTemplateStringTerm(p, env)
	default:
		return part
	}
}

func templateStringTermFromParts(partsTerm *ast.Term, env templateStringEnv) (*ast.Term, bool) {
	if partsTerm == nil {
		return nil, false
	}
	arr, ok := partsTerm.Value.(*ast.Array)
	if !ok {
		return nil, false
	}
	parts := make([]ast.Node, 0, arr.Len())
	for i := range arr.Len() {
		part, ok := templateStringPart(arr.Elem(i), env)
		if !ok {
			return nil, false
		}
		parts = append(parts, part)
	}
	term := ast.TemplateStringTerm(false, parts...)
	term.Location = partsTerm.Location
	return term, true
}

func templateStringPart(term *ast.Term, env templateStringEnv) (ast.Node, bool) {
	if term == nil {
		return nil, false
	}
	if str, ok := term.Value.(ast.String); ok {
		return ast.StringTerm(string(str)).SetLocation(term.Location), true
	}
	expr, ok := exprFromInterpolationTerm(term, nil, env)
	if !ok {
		return nil, false
	}
	return expr, true
}

func decodeTemplateStringBindingRHS(term *ast.Term, env templateStringEnv) (bool, *ast.Expr, bool) {
	if term == nil {
		return false, nil, false
	}
	switch value := term.Value.(type) {
	case *ast.SetComprehension:
		expr, ok := decodeTemplateStringExprFromSetComprehension(value, env)
		if !ok {
			return false, nil, false
		}
		return true, expr, true
	case ast.Set:
		if value.Len() != 1 {
			return false, nil, false
		}
		expr, ok := exprFromInterpolationTerm(value.Slice()[0], nil, env)
		if !ok {
			return false, nil, false
		}
		return true, expr, true
	case ast.Var:
		if expr, ok := env.values[value]; ok {
			return false, expr.Copy(), true
		}
		return false, nil, false
	case ast.Call:
		if isInternalTemplateStringCall([]*ast.Term(value)) && len(value) == 2 {
			tmpl, ok := templateStringTermFromParts(value[1], env)
			if !ok {
				return false, nil, false
			}
			return false, ast.NewExpr(tmpl), true
		}
	}
	return false, nil, false
}

func exprFromInterpolationTerm(term *ast.Term, src *ast.Expr, env templateStringEnv) (*ast.Expr, bool) {
	if term == nil {
		return nil, false
	}
	var expr *ast.Expr
	switch value := term.Value.(type) {
	case *ast.SetComprehension:
		var ok bool
		expr, ok = decodeTemplateStringExprFromSetComprehension(value, env)
		if !ok {
			return nil, false
		}
	case ast.Set:
		if value.Len() != 1 {
			return nil, false
		}
		var ok bool
		expr, ok = exprFromInterpolationTerm(value.Slice()[0], nil, env)
		if !ok {
			return nil, false
		}
	case ast.Var:
		if e, ok := env.parts[value]; ok {
			expr = e.Copy()
			break
		}
		if e, ok := env.values[value]; ok {
			expr = e.Copy()
			break
		}
		if value.IsGenerated() {
			return nil, false
		}
		expr = ast.NewExpr(ast.NewTerm(value).SetLocation(term.Location))
	case ast.Call:
		rewritten := reconstructTemplateStringTerm(term, env)
		expr = ast.NewExpr(rewritten)
	default:
		rewritten := reconstructTemplateStringTerm(term, env)
		expr = ast.NewExpr(rewritten)
	}
	if src != nil {
		if expr.Location == nil {
			expr.Location = src.Location
		}
		if len(src.With) > 0 {
			expr.With = append(cloneWithModifiers(expr.With, env), cloneWithModifiers(src.With, env)...)
		}
		expr.Negated = expr.Negated || src.Negated
	}
	return expr, true
}

func decodeTemplateStringExprFromSetComprehension(comp *ast.SetComprehension, env templateStringEnv) (*ast.Expr, bool) {
	if comp == nil || comp.Term == nil {
		return nil, false
	}
	headVar, ok := comp.Term.Value.(ast.Var)
	if !ok {
		return nil, false
	}

	if len(comp.Body) == 1 {
		if rhs, ok := equalityDefinesVar(comp.Body[0], headVar); ok {
			if expr, ok := exprFromInterpolationTerm(rhs, comp.Body[0], env); ok && interpolationExprClosed(expr) {
				return expr, true
			}
		}
	}

	rewritten, gen := reconstructTemplateStringBody(comp.Body, nil, env)
	if expr, ok := gen.values[headVar]; ok && len(rewritten) == 0 && interpolationExprClosed(expr) {
		return expr.Copy(), true
	}
	if expr, ok := recoverGeneratedCallExpr(comp, headVar, env); ok && interpolationExprClosed(expr) {
		return expr, true
	}
	return nil, false
}

// interpolationExprClosed reports whether expr can stand alone as a template
// interpolation. Generated variables are allowed only when the expression
// itself binds them, as in a comprehension.
func interpolationExprClosed(expr *ast.Expr) bool {
	if expr == nil {
		return false
	}
	bound := map[ast.Var]struct{}{}
	collectLocallyBoundVars(expr, bound)
	closed := true
	ast.WalkVars(expr, func(v ast.Var) bool {
		if !v.IsGenerated() || v.IsWildcard() {
			return false
		}
		if _, ok := bound[v]; !ok {
			closed = false
			return true
		}
		return false
	})
	return closed
}

func collectLocallyBoundVars(x any, bound map[ast.Var]struct{}) {
	ast.WalkBodies(x, func(body ast.Body) bool {
		for _, expr := range body {
			if name, _, ok := generatedVarBinding(expr); ok {
				bound[name] = struct{}{}
			}
		}
		return false
	})
	ast.WalkClosures(x, func(closure any) bool {
		switch c := closure.(type) {
		case *ast.ArrayComprehension:
			if c.Term != nil {
				if v, ok := c.Term.Value.(ast.Var); ok && v.IsGenerated() {
					bound[v] = struct{}{}
				}
			}
		case *ast.SetComprehension:
			if c.Term != nil {
				if v, ok := c.Term.Value.(ast.Var); ok && v.IsGenerated() {
					bound[v] = struct{}{}
				}
			}
		case *ast.ObjectComprehension:
			if c.Key != nil {
				if v, ok := c.Key.Value.(ast.Var); ok && v.IsGenerated() {
					bound[v] = struct{}{}
				}
			}
			if c.Value != nil {
				if v, ok := c.Value.Value.(ast.Var); ok && v.IsGenerated() {
					bound[v] = struct{}{}
				}
			}
		}
		return false
	})
}

func recoverGeneratedCallExpr(comp *ast.SetComprehension, head ast.Var, env templateStringEnv) (*ast.Expr, bool) {
	bindings := map[ast.Var]*ast.Term{}
	var call *ast.Expr
	for _, expr := range comp.Body {
		if name, rhs, ok := generatedVarBinding(expr); ok {
			bindings[name] = rhs
			continue
		}
		if expr.IsCall() {
			if call != nil {
				return nil, false
			}
			call = expr
			continue
		}
		return nil, false
	}

	if call == nil {
		term, ok := resolveBindingTerm(head, bindings)
		if !ok {
			return nil, false
		}
		inlined := inlineGeneratedTerm(term, bindings, map[ast.Var]struct{}{head: {}})
		return exprFromInterpolationTerm(inlined, nil, env)
	}

	terms, ok := call.Terms.([]*ast.Term)
	if !ok || len(terms) < 2 {
		return nil, false
	}
	outTerm := terms[len(terms)-1]
	outVar, ok := outTerm.Value.(ast.Var)
	if !ok || !outputBindsHead(head, outVar, bindings) {
		return nil, false
	}

	seen := map[ast.Var]struct{}{outVar: {}, head: {}}
	args := make([]*ast.Term, len(terms)-1)
	for i, arg := range terms[:len(terms)-1] {
		args[i] = inlineGeneratedTerm(arg, bindings, seen)
	}
	callTerm := reconstructTemplateStringTerm(ast.CallTerm(args...), env)
	expr := ast.NewExpr(callTerm)
	expr.With = cloneWithModifiers(call.With, env)
	expr.Location = call.Location
	return expr, true
}

func outputBindsHead(head, out ast.Var, bindings map[ast.Var]*ast.Term) bool {
	if head == out {
		return true
	}
	term, ok := resolveBindingTerm(head, bindings)
	if !ok {
		return false
	}
	v, ok := term.Value.(ast.Var)
	return ok && v == out
}

func resolveBindingTerm(v ast.Var, bindings map[ast.Var]*ast.Term) (*ast.Term, bool) {
	seen := map[ast.Var]struct{}{}
	term, ok := bindings[v]
	if !ok {
		return nil, false
	}
	for {
		if _, dup := seen[v]; dup {
			return nil, false
		}
		seen[v] = struct{}{}
		next, ok := term.Value.(ast.Var)
		if !ok {
			return term, true
		}
		v = next
		nextTerm, ok := bindings[v]
		if !ok {
			return ast.NewTerm(v), true
		}
		term = nextTerm
	}
}

func inlineGeneratedTerm(term *ast.Term, bindings map[ast.Var]*ast.Term, seen map[ast.Var]struct{}) *ast.Term {
	if term == nil {
		return nil
	}
	if v, ok := term.Value.(ast.Var); ok {
		if _, skip := seen[v]; skip {
			return term.Copy()
		}
		if bound, ok := bindings[v]; ok && v.IsGenerated() {
			seen[v] = struct{}{}
			inlined := inlineGeneratedTerm(bound, bindings, seen)
			delete(seen, v)
			return inlined
		}
		return term.Copy()
	}

	switch v := term.Value.(type) {
	case ast.Ref:
		if len(v) == 0 {
			return term.Copy()
		}
		head := inlineGeneratedTerm(v[0], bindings, seen)
		rest := make([]*ast.Term, len(v)-1)
		for i := 1; i < len(v); i++ {
			rest[i-1] = inlineGeneratedTerm(v[i], bindings, seen)
		}
		switch h := head.Value.(type) {
		case ast.Ref:
			ref := make(ast.Ref, 0, len(h)+len(rest))
			ref = append(ref, h...)
			ref = append(ref, rest...)
			return ast.NewTerm(ref).SetLocation(term.Location)
		case ast.Var:
			ref := make(ast.Ref, 0, 1+len(rest))
			ref = append(ref, ast.NewTerm(h).SetLocation(head.Location))
			ref = append(ref, rest...)
			return ast.NewTerm(ref).SetLocation(term.Location)
		default:
			if len(rest) == 0 {
				return head
			}
			ref := make(ast.Ref, 0, 1+len(rest))
			ref = append(ref, head)
			ref = append(ref, rest...)
			return ast.NewTerm(ref).SetLocation(term.Location)
		}
	case ast.Call:
		args := make([]*ast.Term, len(v))
		for i := range v {
			args[i] = inlineGeneratedTerm(v[i], bindings, seen)
		}
		out := ast.CallTerm(args...)
		out.Location = term.Location
		return out
	case *ast.Array:
		elems := make([]*ast.Term, v.Len())
		for i := range v.Len() {
			elems[i] = inlineGeneratedTerm(v.Elem(i), bindings, seen)
		}
		out := ast.NewTerm(ast.NewArray(elems...))
		out.Location = term.Location
		return out
	case ast.Set:
		elems := v.Slice()
		outElems := make([]*ast.Term, len(elems))
		for i := range elems {
			outElems[i] = inlineGeneratedTerm(elems[i], bindings, seen)
		}
		out := ast.NewTerm(ast.NewSet(outElems...))
		out.Location = term.Location
		return out
	case ast.Object:
		pairs := make([][2]*ast.Term, 0, v.Len())
		v.Foreach(func(k, val *ast.Term) {
			pairs = append(pairs, ast.Item(
				inlineGeneratedTerm(k, bindings, seen),
				inlineGeneratedTerm(val, bindings, seen),
			))
		})
		out := ast.NewTerm(ast.NewObject(pairs...))
		out.Location = term.Location
		return out
	case *ast.SetComprehension:
		body := inlineGeneratedBody(v.Body, bindings, seen)
		out := ast.SetComprehensionTerm(inlineGeneratedTerm(v.Term, bindings, seen), body)
		out.Location = term.Location
		return out
	case *ast.ArrayComprehension:
		body := inlineGeneratedBody(v.Body, bindings, seen)
		out := ast.ArrayComprehensionTerm(inlineGeneratedTerm(v.Term, bindings, seen), body)
		out.Location = term.Location
		return out
	case *ast.ObjectComprehension:
		body := inlineGeneratedBody(v.Body, bindings, seen)
		out := ast.ObjectComprehensionTerm(
			inlineGeneratedTerm(v.Key, bindings, seen),
			inlineGeneratedTerm(v.Value, bindings, seen),
			body,
		)
		out.Location = term.Location
		return out
	default:
		return term.Copy()
	}
}

func inlineGeneratedBody(body ast.Body, bindings map[ast.Var]*ast.Term, seen map[ast.Var]struct{}) ast.Body {
	out := make(ast.Body, len(body))
	for i, expr := range body {
		cpy := expr.Copy()
		switch ts := cpy.Terms.(type) {
		case []*ast.Term:
			for j := range ts {
				ts[j] = inlineGeneratedTerm(ts[j], bindings, seen)
			}
		case *ast.Term:
			cpy.Terms = inlineGeneratedTerm(ts, bindings, seen)
		}
		out[i] = cpy
	}
	return out
}

func generatedVarBinding(expr *ast.Expr) (ast.Var, *ast.Term, bool) {
	if expr == nil || expr.Negated || (!expr.IsEquality() && !expr.IsAssignment()) {
		return "", nil, false
	}
	ops := expr.Operands()
	if len(ops) != 2 {
		return "", nil, false
	}
	if v, ok := ops[0].Value.(ast.Var); ok && v.IsGenerated() {
		return v, ops[1], true
	}
	if v, ok := ops[1].Value.(ast.Var); ok && v.IsGenerated() {
		return v, ops[0], true
	}
	return "", nil, false
}

func equalityDefinesVar(expr *ast.Expr, v ast.Var) (*ast.Term, bool) {
	name, rhs, ok := generatedVarBinding(expr)
	if !ok || name != v {
		// Also accept a non-generated head, which fast-path comprehensions use.
		if expr == nil || (!expr.IsEquality() && !expr.IsAssignment()) {
			return nil, false
		}
		ops := expr.Operands()
		if len(ops) != 2 {
			return nil, false
		}
		if lhs, ok := ops[0].Value.(ast.Var); ok && lhs == v {
			return ops[1], true
		}
		if rhs, ok := ops[1].Value.(ast.Var); ok && rhs == v {
			return ops[0], true
		}
		return nil, false
	}
	return rhs, true
}

func onlyTemplateStringPartUses(rest ast.Body, head *ast.Head, v ast.Var) bool {
	total := 0
	ast.WalkVars(rest, func(name ast.Var) bool {
		if name == v {
			total++
		}
		return false
	})
	if head != nil {
		ast.WalkVars(head, func(name ast.Var) bool {
			if name == v {
				total++
			}
			return false
		})
	}
	if total == 0 {
		return false
	}
	parts := 0
	countTemplateStringPartUses(rest, v, false, &parts)
	return total == parts
}

func countTemplateStringPartUses(x any, v ast.Var, inPart bool, parts *int) {
	if x == nil {
		return
	}
	switch n := x.(type) {
	case ast.Body:
		for _, expr := range n {
			countTemplateStringPartUses(expr, v, inPart, parts)
		}
	case []*ast.Expr:
		for _, expr := range n {
			countTemplateStringPartUses(expr, v, inPart, parts)
		}
	case *ast.Expr:
		if terms, ok := n.Terms.([]*ast.Term); ok && !inPart && isInternalTemplateStringCall(terms) && len(terms) >= 2 {
			countTemplateStringPartUses(terms[0], v, false, parts)
			countTemplateStringPartUses(terms[1], v, true, parts)
			for _, term := range terms[2:] {
				countTemplateStringPartUses(term, v, false, parts)
			}
		} else {
			countTemplateStringPartUses(n.Terms, v, inPart, parts)
		}
		for _, with := range n.With {
			countTemplateStringPartUses(with, v, false, parts)
		}
	case []*ast.Term:
		for _, term := range n {
			countTemplateStringPartUses(term, v, inPart, parts)
		}
	case *ast.Term:
		if call, ok := n.Value.(ast.Call); ok && !inPart && isInternalTemplateStringCall([]*ast.Term(call)) && len(call) >= 2 {
			countTemplateStringPartUses(call[0], v, false, parts)
			countTemplateStringPartUses(call[1], v, true, parts)
			for _, term := range call[2:] {
				countTemplateStringPartUses(term, v, false, parts)
			}
			return
		}
		if name, ok := n.Value.(ast.Var); ok && name == v {
			if inPart {
				*parts++
			}
			return
		}
		countTemplateStringPartUses(n.Value, v, inPart, parts)
	case ast.Ref:
		for _, term := range n {
			countTemplateStringPartUses(term, v, inPart, parts)
		}
	case ast.Call:
		if !inPart && isInternalTemplateStringCall([]*ast.Term(n)) && len(n) >= 2 {
			countTemplateStringPartUses(n[0], v, false, parts)
			countTemplateStringPartUses(n[1], v, true, parts)
			for _, term := range n[2:] {
				countTemplateStringPartUses(term, v, false, parts)
			}
			return
		}
		for _, term := range n {
			countTemplateStringPartUses(term, v, inPart, parts)
		}
	case *ast.Array:
		for i := range n.Len() {
			countTemplateStringPartUses(n.Elem(i), v, inPart, parts)
		}
	case ast.Set:
		for _, term := range n.Slice() {
			countTemplateStringPartUses(term, v, inPart, parts)
		}
	case ast.Object:
		n.Foreach(func(k, val *ast.Term) {
			countTemplateStringPartUses(k, v, inPart, parts)
			countTemplateStringPartUses(val, v, inPart, parts)
		})
	case *ast.ArrayComprehension:
		countTemplateStringPartUses(n.Term, v, inPart, parts)
		countTemplateStringPartUses(n.Body, v, inPart, parts)
	case *ast.SetComprehension:
		countTemplateStringPartUses(n.Term, v, inPart, parts)
		countTemplateStringPartUses(n.Body, v, inPart, parts)
	case *ast.ObjectComprehension:
		countTemplateStringPartUses(n.Key, v, inPart, parts)
		countTemplateStringPartUses(n.Value, v, inPart, parts)
		countTemplateStringPartUses(n.Body, v, inPart, parts)
	case *ast.Every:
		countTemplateStringPartUses(n.Key, v, inPart, parts)
		countTemplateStringPartUses(n.Value, v, inPart, parts)
		countTemplateStringPartUses(n.Domain, v, inPart, parts)
		countTemplateStringPartUses(n.Body, v, inPart, parts)
	case *ast.SomeDecl:
		for _, term := range n.Symbols {
			countTemplateStringPartUses(term, v, inPart, parts)
		}
	case *ast.With:
		countTemplateStringPartUses(n.Target, v, false, parts)
		countTemplateStringPartUses(n.Value, v, false, parts)
	case *ast.Head:
		countTemplateStringPartUses(n.Args, v, inPart, parts)
		countTemplateStringPartUses(n.Key, v, inPart, parts)
		countTemplateStringPartUses(n.Value, v, inPart, parts)
	case ast.Var:
		if inPart && n == v {
			*parts++
		}
	default:
		if !inPart {
			return
		}
		ast.WalkVars(x, func(name ast.Var) bool {
			if name == v {
				*parts++
			}
			return false
		})
	}
}

func containsInternalTemplateString(x any) bool {
	found := false
	ast.WalkTerms(x, func(term *ast.Term) bool {
		if termIsInternalTemplateString(term) {
			found = true
			return true
		}
		return false
	})
	return found
}

func termIsInternalTemplateString(term *ast.Term) bool {
	if term == nil {
		return false
	}
	switch value := term.Value.(type) {
	case ast.Ref:
		return value.Equal(internalTemplateStringRef)
	case ast.Call:
		return isInternalTemplateStringCall([]*ast.Term(value))
	default:
		return false
	}
}

func isInternalTemplateStringCall(terms []*ast.Term) bool {
	if len(terms) < 2 || terms[0] == nil {
		return false
	}
	ref, ok := terms[0].Value.(ast.Ref)
	return ok && ref.Equal(internalTemplateStringRef)
}

func exprToTerm(expr *ast.Expr) (*ast.Term, bool) {
	if expr == nil {
		return nil, false
	}
	switch ts := expr.Terms.(type) {
	case *ast.Term:
		return ts, true
	case []*ast.Term:
		if len(ts) == 0 {
			return nil, false
		}
		return ast.CallTerm(ts...), true
	default:
		return nil, false
	}
}

func cloneWithModifiers(withs []*ast.With, env templateStringEnv) []*ast.With {
	if len(withs) == 0 {
		return nil
	}
	out := make([]*ast.With, len(withs))
	for i, with := range withs {
		cpy := with.Copy()
		cpy.Target = reconstructTemplateStringTerm(cpy.Target, env)
		cpy.Value = reconstructTemplateStringTerm(cpy.Value, env)
		out[i] = cpy
	}
	return out
}
