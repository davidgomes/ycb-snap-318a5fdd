package astwalk

import (
	"go/ast"
)

type docLinkWalker struct {
	visitor DocLinkVisitor
}

func (w *docLinkWalker) WalkFile(f *ast.File) {
	if wf, ok := w.visitor.(walkerEvents); ok {
		if !wf.EnterFile(f) {
			return
		}
	}

	for _, decl := range f.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			if decl.Doc != nil {
				w.visitor.VisitDocLink(decl, decl.Doc)
			}
		case *ast.GenDecl:
			if decl.Doc != nil {
				w.visitor.VisitDocLink(decl, decl.Doc)
			}
			for _, spec := range decl.Specs {
				switch spec := spec.(type) {
				case *ast.ImportSpec:
					if spec.Doc != nil {
						w.visitor.VisitDocLink(spec, spec.Doc)
					}
				case *ast.ValueSpec:
					if spec.Doc != nil {
						w.visitor.VisitDocLink(spec, spec.Doc)
					}
				case *ast.TypeSpec:
					if spec.Doc != nil {
						w.visitor.VisitDocLink(spec, spec.Doc)
					}
					ast.Inspect(spec.Type, func(n ast.Node) bool {
						if n, ok := n.(*ast.Field); ok {
							if n.Doc != nil {
								w.visitor.VisitDocLink(n, n.Doc)
							}
						}
						return true
					})
				}
			}
		}
	}
}
