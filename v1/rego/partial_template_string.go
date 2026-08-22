package rego

import "github.com/open-policy-agent/opa/v1/ast"

func reconstructPartialTemplateStrings(pq *PartialQueries) {
	for i := range pq.Queries {
		pq.Queries[i], _ = reconstructTemplateStringBody(pq.Queries[i], templateStringElideGeneratedBindings)
	}

	for i := range pq.Support {
		pq.Support[i] = reconstructTemplateStringModule(pq.Support[i])
	}
}

func reconstructTemplateStringModule(mod *ast.Module) *ast.Module {
	cpy := mod.Copy()

	for i := range cpy.Rules {
		cpy.Rules[i] = reconstructTemplateStringRule(cpy.Rules[i])
	}

	return cpy
}

func reconstructTemplateStringRule(rule *ast.Rule) *ast.Rule {
	cpy := rule.Copy()

	if cpy.Head.Key != nil {
		cpy.Head.Key = reconstructTemplateStringTerm(cpy.Head.Key, templateStringEnv{})
	}

	if cpy.Head.Value != nil {
		cpy.Head.Value = reconstructTemplateStringTerm(cpy.Head.Value, templateStringEnv{})
	}

	body, env := reconstructTemplateStringBody(cpy.Body, templateStringElideGeneratedBindings)
	cpy.Body = body

	if cpy.Head.Value != nil {
		if v, ok := cpy.Head.Value.Value.(ast.Var); ok {
			if expr, ok := env.lookup(v); ok {
				if term, ok := expr.Terms.(*ast.Term); ok {
					cpy.Head.Value = term.Copy()
				}
			}
		}
	}

	if cpy.Else != nil {
		cpy.Else = reconstructTemplateStringRule(cpy.Else)
	}

	return cpy
}
