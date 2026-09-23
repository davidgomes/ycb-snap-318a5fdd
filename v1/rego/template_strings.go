// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import (
	"slices"

	"github.com/open-policy-agent/opa/v1/ast"
)

// reconstructTemplateStrings rewrites calls to the internal template-string
// built-in in partial evaluation output back into template-string syntax.
// Calls that can't be faithfully represented as template strings are left as-is.
func reconstructTemplateStrings(queries []ast.Body, support []*ast.Module) {
	for i := range queries {
		queries[i] = reconstructTemplateStringsBody(queries[i])
	}
	for _, mod := range support {
		ast.WalkRules(mod, func(r *ast.Rule) bool {
			r.Body = reconstructTemplateStringsBody(r.Body)
			return false
		})
	}
}

func reconstructTemplateStringsBody(body ast.Body) ast.Body {
	for _, expr := range body {
		ast.NewGenericVisitor(func(x any) bool {
			switch x := x.(type) {
			case *ast.ArrayComprehension:
				x.Body = reconstructTemplateStringsBody(x.Body)
			case *ast.SetComprehension:
				x.Body = reconstructTemplateStringsBody(x.Body)
			case *ast.ObjectComprehension:
				x.Body = reconstructTemplateStringsBody(x.Body)
			case *ast.Every:
				x.Body = reconstructTemplateStringsBody(x.Body)
			default:
				return false
			}
			return true
		}).Walk(expr)
	}

	removed := map[int]struct{}{}
	for i, expr := range body {
		arr, out, ok := templateStringCall(expr)
		if !ok {
			continue
		}
		parts := make([]ast.Node, 0, arr.Len())
		var bindings []int
		for j := range arr.Len() {
			var part ast.Node
			var binding int
			part, binding, ok = templateStringPart(body, i, removed, arr.Elem(j))
			if !ok {
				break
			}
			if binding >= 0 {
				bindings = append(bindings, binding)
			}
			parts = append(parts, part)
		}
		if !ok {
			continue
		}
		for _, b := range bindings {
			removed[b] = struct{}{}
		}
		ts := ast.TemplateStringTerm(false, parts...).SetLocation(expr.Location)
		var repl *ast.Expr
		if out != nil {
			repl = ast.Equality.Expr(out, ts)
		} else {
			repl = ast.NewExpr(ts)
		}
		repl.SetLocation(expr.Location)
		repl.Index = expr.Index
		body[i] = repl
	}

	if len(removed) == 0 {
		return body
	}
	result := make(ast.Body, 0, len(body)-len(removed))
	for i, expr := range body {
		if _, ok := removed[i]; !ok {
			result = append(result, expr)
		}
	}
	for i := range result {
		result[i].Index = i
	}
	return result
}

// templateStringCall returns the parts array and, if present, the output term
// of a call to the internal template-string built-in.
func templateStringCall(expr *ast.Expr) (*ast.Array, *ast.Term, bool) {
	if expr.Negated || len(expr.With) > 0 {
		return nil, nil, false
	}
	var terms []*ast.Term
	switch t := expr.Terms.(type) {
	case []*ast.Term:
		terms = t
	case *ast.Term:
		if call, ok := t.Value.(ast.Call); ok {
			terms = call
		}
	}
	if len(terms) != 2 && len(terms) != 3 {
		return nil, nil, false
	}
	if terms[0].Value.Compare(ast.InternalTemplateString.Ref()) != 0 {
		return nil, nil, false
	}
	arr, ok := terms[1].Value.(*ast.Array)
	if !ok {
		return nil, nil, false
	}
	if len(terms) == 3 {
		return arr, terms[2], true
	}
	return arr, nil, true
}

// templateStringPart converts an element of the lowered template-string array
// back into a template-string part. If the element is a generated var bound
// in body, the index of the binding expression is returned so it can be removed.
func templateStringPart(body ast.Body, callIdx int, removed map[int]struct{}, elem *ast.Term) (ast.Node, int, bool) {
	switch v := elem.Value.(type) {
	case ast.String:
		return elem, -1, true
	case ast.Set:
		if v.Len() != 1 {
			return nil, -1, false
		}
		return termPart(v.Slice()[0], nil), -1, true
	case *ast.SetComprehension:
		part, ok := comprehensionPart(v)
		return part, -1, ok
	case ast.Var:
		if !v.IsGenerated() || countVar(body, v) != 2 {
			return nil, -1, false
		}
		for i, expr := range body {
			if i == callIdx {
				continue
			}
			if _, ok := removed[i]; ok {
				continue
			}
			if t := boundTerm(expr, v); t != nil {
				if sc, ok := t.Value.(*ast.SetComprehension); ok {
					if part, ok := comprehensionPart(sc); ok {
						return part, i, true
					}
				}
				return nil, -1, false
			}
		}
	}
	return nil, -1, false
}

func termPart(t *ast.Term, with []*ast.With) *ast.Expr {
	var expr *ast.Expr
	if call, ok := t.Value.(ast.Call); ok {
		expr = ast.NewExpr([]*ast.Term(call))
	} else {
		expr = ast.NewExpr(t)
	}
	expr.With = with
	return expr.SetLocation(t.Location)
}

// comprehensionPart reduces a capturing set comprehension like
// {x | y = input.a; count(y, z); x = z} to the single expression it computes;
// e.g. count(input.a).
func comprehensionPart(sc *ast.SetComprehension) (*ast.Expr, bool) {
	head, ok := sc.Term.Value.(ast.Var)
	if !ok {
		return nil, false
	}
	body := sc.Body.Copy()
	if len(body) > 1 && slices.ContainsFunc(body, func(e *ast.Expr) bool { return len(e.With) > 0 }) {
		return nil, false
	}

	for len(body) > 1 {
		inlined := false
		for i, expr := range body {
			v, t := inlineCandidate(expr, head)
			if t == nil || countVar(body, v) != 2 {
				continue
			}
			rest := slices.Concat(body[:i], body[i+1:])
			if substituteVar(rest, v, t) {
				body = rest
				inlined = true
				break
			}
		}
		if !inlined {
			return nil, false
		}
	}

	expr := body[0]
	if expr.Negated {
		return nil, false
	}
	t := boundTerm(expr, head)
	if t == nil || t.Vars().Contains(head) {
		return nil, false
	}
	return termPart(t, expr.With), true
}

// inlineCandidate returns a generated var and the term it can be replaced with,
// for expressions like `v = t` and `f(a, b, v)`.
func inlineCandidate(expr *ast.Expr, head ast.Var) (ast.Var, *ast.Term) {
	if expr.Negated || len(expr.With) > 0 || !expr.IsCall() {
		return "", nil
	}
	if expr.IsEquality() {
		for _, pair := range [][2]*ast.Term{{expr.Operand(0), expr.Operand(1)}, {expr.Operand(1), expr.Operand(0)}} {
			if v, ok := pair[0].Value.(ast.Var); ok && v.IsGenerated() && v != head && !pair[1].Vars().Contains(v) {
				return v, pair[1]
			}
		}
		return "", nil
	}
	name := expr.Operator().String()
	bi, ok := ast.BuiltinMap[name]
	if !ok || bi.Relation || bi.Decl == nil {
		return "", nil
	}
	terms := expr.Terms.([]*ast.Term)
	ops := terms[1:]
	if len(ops) != len(bi.Decl.FuncArgs().Args)+1 {
		return "", nil
	}
	out := ops[len(ops)-1]
	v, ok := out.Value.(ast.Var)
	if !ok || !v.IsGenerated() || v == head {
		return "", nil
	}
	call := ast.CallTerm(terms[:len(terms)-1]...).SetLocation(expr.Location)
	if call.Vars().Contains(v) {
		return "", nil
	}
	return v, call
}

func boundTerm(expr *ast.Expr, v ast.Var) *ast.Term {
	if expr.Negated || !expr.IsEquality() {
		return nil
	}
	a, b := expr.Operand(0), expr.Operand(1)
	if a.Value.Compare(v) == 0 {
		return b
	}
	if b.Value.Compare(v) == 0 {
		return a
	}
	return nil
}

func countVar(body ast.Body, v ast.Var) int {
	n := 0
	ast.WalkVars(body, func(x ast.Var) bool {
		if x == v {
			n++
		}
		return false
	})
	return n
}

// substituteVar replaces v with t in body. It returns false if v occurs in a
// position where t can't be substituted; e.g. as the head of a ref when t isn't a ref.
func substituteVar(body ast.Body, v ast.Var, t *ast.Term) bool {
	ok := true
	for _, expr := range body {
		_, err := ast.Transform(ast.NewGenericTransformer(func(x any) (any, error) {
			switch x := x.(type) {
			case ast.Var:
				if x == v {
					return t.Value, nil
				}
			case ast.Ref:
				if x[0].Value.Compare(v) == 0 {
					switch tv := t.Value.(type) {
					case ast.Ref:
						return tv.Concat(x[1:]), nil
					case ast.Var:
						return append(ast.Ref{ast.NewTerm(tv).SetLocation(x[0].Location)}, x[1:]...), nil
					default:
						ok = false
					}
				}
			}
			return x, nil
		}), expr)
		if err != nil || !ok {
			return false
		}
	}
	return true
}
