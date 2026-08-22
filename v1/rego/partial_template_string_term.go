package rego

import "github.com/open-policy-agent/opa/v1/ast"

func reconstructTemplateStringTerm(term *ast.Term, env templateStringEnv) *ast.Term {
	if term == nil {
		return nil
	}

	cpy := term.Copy()

	switch value := cpy.Value.(type) {
	case ast.Ref:
		for i := range value {
			value[i] = reconstructTemplateStringTerm(value[i], env)
		}
		cpy.Value = value
	case ast.Call:
		for i := range value {
			value[i] = reconstructTemplateStringTerm(value[i], env)
		}
		cpy.Value = value
		if rewritten := reconstructTemplateStringCallTerm(cpy, env); rewritten != nil {
			return rewritten
		}
	case *ast.Array:
		for i := range value.Len() {
			value.Set(i, reconstructTemplateStringTerm(value.Elem(i), env))
		}
	case ast.Set:
		rewritten := ast.NewSetWithCapacity(value.Len())
		value.Foreach(func(elem *ast.Term) {
			rewritten.Add(reconstructTemplateStringTerm(elem, env))
		})
		cpy.Value = rewritten
	case ast.Object:
		keys := value.Keys()
		rewritten := ast.NewObject()
		for i := range keys {
			key := reconstructTemplateStringTerm(keys[i], env)
			val := reconstructTemplateStringTerm(value.Get(keys[i]), env)
			rewritten.Insert(key, val)
		}
		cpy.Value = rewritten
	case *ast.ArrayComprehension:
		value.Term = reconstructTemplateStringTerm(value.Term, env)
		value.Body, _ = reconstructTemplateStringBody(value.Body, templateStringPreserveBindings)
	case *ast.SetComprehension:
		value.Term = reconstructTemplateStringTerm(value.Term, env)
		value.Body, _ = reconstructTemplateStringBody(value.Body, templateStringPreserveBindings)
	case *ast.ObjectComprehension:
		value.Key = reconstructTemplateStringTerm(value.Key, env)
		value.Value = reconstructTemplateStringTerm(value.Value, env)
		value.Body, _ = reconstructTemplateStringBody(value.Body, templateStringPreserveBindings)
	}

	return cpy
}

func reconstructTemplateStringCallTerm(term *ast.Term, env templateStringEnv) *ast.Term {
	call, ok := term.Value.(ast.Call)
	if !ok || len(call) != 2 {
		return term
	}

	if !templateStringIsInternalCall(call) {
		return term
	}

	template, ok := reconstructTemplateStringTermFromArray(call[1], env)
	if !ok {
		return term
	}

	return template
}

func reconstructTemplateStringTermFromArray(term *ast.Term, env templateStringEnv) (*ast.Term, bool) {
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

func decodeTemplateStringExprFromTerm(term *ast.Term, env templateStringEnv) (*ast.Expr, bool) {
	switch value := term.Value.(type) {
	case ast.Var:
		if expr, ok := env.lookup(value); ok {
			return expr, true
		}
		return ast.NewExpr(term.Copy()), true
	case ast.Set:
		if value.Len() == 1 {
			return ast.NewExpr(reconstructTemplateStringTerm(value.Slice()[0], env)), true
		}
		return ast.NewExpr(term.Copy()), true
	case *ast.SetComprehension:
		return decodeTemplateStringExprFromSetComprehension(value, env)
	default:
		return ast.NewExpr(reconstructTemplateStringTerm(term, env)), true
	}
}

func decodeTemplateStringExprFromSetComprehension(comp *ast.SetComprehension, env templateStringEnv) (*ast.Expr, bool) {
	headVar, ok := comp.Term.Value.(ast.Var)
	if !ok {
		return nil, false
	}

	bodyCopy := comp.Body.Copy()
	localEnv := env.copy()
	_, generated := reconstructTemplateStringBody(bodyCopy, templateStringElideGeneratedBindings)
	for v, expr := range generated {
		localEnv[v] = expr
	}

	if expr, ok := localEnv.lookup(headVar); ok {
		return expr, true
	}

	if len(bodyCopy) == 1 {
		if binding, ok := templateStringGeneratedBinding(bodyCopy[0], nil, localEnv); ok && binding.varName == headVar {
			return binding.expr.Copy(), true
		}
	}

	return nil, false
}

func templateStringIsInternalCall(terms []*ast.Term) bool {
	if len(terms) == 0 {
		return false
	}

	ref, ok := terms[0].Value.(ast.Ref)
	if !ok {
		return false
	}

	return ast.Compare(ref, ast.InternalTemplateString.Ref()) == 0
}
