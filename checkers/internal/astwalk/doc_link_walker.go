package astwalk

import (
	"go/ast"
)

type docLinkWalker struct {
	visitor DocLinkVisitor
}

func (w *docLinkWalker) WalkFile(f *ast.File) {
	for _, decl := range f.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			w.visit(f, decl, decl.Doc)
		case *ast.GenDecl:
			w.visit(f, decl, decl.Doc)
			for _, spec := range decl.Specs {
				switch spec := spec.(type) {
				case *ast.ImportSpec:
					w.visit(f, spec, spec.Doc)
				case *ast.ValueSpec:
					w.visit(f, spec, spec.Doc)
				case *ast.TypeSpec:
					w.visit(f, spec, spec.Doc)
					ast.Inspect(spec.Type, func(n ast.Node) bool {
						if field, ok := n.(*ast.Field); ok {
							w.visit(f, field, field.Doc)
						}
						return true
					})
				}
			}
		}
	}
}

func (w *docLinkWalker) visit(f *ast.File, decl ast.Node, doc *ast.CommentGroup) {
	if doc == nil {
		return
	}
	w.visitor.VisitDocLink(f, decl, doc)
}
