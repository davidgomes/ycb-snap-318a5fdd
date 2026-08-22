// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import "github.com/open-policy-agent/opa/v1/ast"

// restoreTemplateStrings changes the compiler representation of template
// strings back into Rego syntax before partial results are exposed.
func restoreTemplateStrings(x any) {
	ast.WalkExprs(x, func(expr *ast.Expr) bool {
		var terms ast.Call
		switch t := expr.Terms.(type) {
		case ast.Call:
			terms = t
		case []*ast.Term:
			terms = ast.Call(t)
		case *ast.Term:
			var ok bool
			terms, ok = t.Value.(ast.Call)
			if !ok {
				return false
			}
		default:
			return false
		}
		if len(terms) != 2 && len(terms) != 3 {
			return false
		}
		if terms[0].String() != ast.InternalTemplateString.Name && terms[0].Value.String() != ast.InternalTemplateString.Name {
			return false
		}
		parts, ok := terms[1].Value.(*ast.Array)
		if !ok {
			return false
		}
		partsValue := make([]ast.Node, parts.Len())
		for i := 0; i < parts.Len(); i++ {
			part := parts.Elem(i)
			if _, ok := part.Value.(ast.String); ok {
				partsValue[i] = part
			} else {
				partsValue[i] = &ast.Expr{Terms: part}
			}
		}
		template := ast.NewTerm(&ast.TemplateString{Parts: partsValue})
		if len(terms) == 2 {
			expr.Terms = template
		} else {
			replacement := ast.Equality.Expr(terms[2], template)
			replacement.Index = expr.Index
			replacement.Location = expr.Location
			replacement.Negated = expr.Negated
			replacement.With = expr.With
			*expr = *replacement
		}
		return false
	})
}
