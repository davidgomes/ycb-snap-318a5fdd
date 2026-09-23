package astwalk

import (
	"go/ast"
)

type docCommentWalker struct {
	visitor DocCommentVisitor
}

func (w *docCommentWalker) WalkFile(f *ast.File) {
	walkDocComments(f, func(_ ast.Node, doc *ast.CommentGroup) {
		w.visitor.VisitDocComment(doc)
	})
}

// walkDocComments calls visit for every doc-comment inside f
// along with the node it documents.
func walkDocComments(f *ast.File, visit func(decl ast.Node, doc *ast.CommentGroup)) {
	for _, decl := range f.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			if decl.Doc != nil {
				visit(decl, decl.Doc)
			}
		case *ast.GenDecl:
			if decl.Doc != nil {
				visit(decl, decl.Doc)
			}
			for _, spec := range decl.Specs {
				switch spec := spec.(type) {
				case *ast.ImportSpec:
					if spec.Doc != nil {
						visit(spec, spec.Doc)
					}
				case *ast.ValueSpec:
					if spec.Doc != nil {
						visit(spec, spec.Doc)
					}
				case *ast.TypeSpec:
					if spec.Doc != nil {
						visit(spec, spec.Doc)
					}
					ast.Inspect(spec.Type, func(n ast.Node) bool {
						if n, ok := n.(*ast.Field); ok {
							if n.Doc != nil {
								visit(n, n.Doc)
							}
						}
						return true
					})
				}
			}
		}
	}
}
