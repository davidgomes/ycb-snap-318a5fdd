package astwalk

import (
	"go/ast"
	"go/doc/comment"
	"go/token"
	"strings"
)

type docLinkWalker struct {
	visitor DocLinkVisitor
	parser  comment.Parser
}

func newDocLinkWalker(v DocLinkVisitor) *docLinkWalker {
	return &docLinkWalker{
		visitor: v,
		// Accept every syntactically valid link: resolution is up to the visitor.
		// Qualifiers with a slash are validated as import paths by the parser itself.
		parser: comment.Parser{
			LookupPackage: func(name string) (string, bool) { return name, token.IsIdentifier(name) },
			LookupSym: func(recv, name string) bool {
				return token.IsIdentifier(name) && (recv == "" || token.IsIdentifier(recv))
			},
		},
	}
}

func (w *docLinkWalker) WalkFile(f *ast.File) {
	if !w.visitor.EnterFile(f) {
		return
	}

	for _, decl := range f.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			w.walkDoc(decl.Doc, decl)
		case *ast.GenDecl:
			w.walkDoc(decl.Doc, decl)
			for _, spec := range decl.Specs {
				switch spec := spec.(type) {
				case *ast.ImportSpec:
					w.walkDoc(spec.Doc, spec)
				case *ast.ValueSpec:
					w.walkDoc(spec.Doc, spec)
				case *ast.TypeSpec:
					w.walkDoc(spec.Doc, spec)
					ast.Inspect(spec.Type, func(n ast.Node) bool {
						if n, ok := n.(*ast.Field); ok {
							w.walkDoc(n.Doc, n)
						}
						return true
					})
				}
			}
		}
	}
}

func (w *docLinkWalker) walkDoc(cg *ast.CommentGroup, node ast.Node) {
	if cg == nil {
		return
	}
	doc := w.parser.Parse(cg.Text())
	w.walkBlocks(doc.Content, node)
}

func (w *docLinkWalker) walkBlocks(blocks []comment.Block, node ast.Node) {
	for _, b := range blocks {
		switch b := b.(type) {
		case *comment.Paragraph:
			w.walkText(b.Text, node)
		case *comment.Heading:
			w.walkText(b.Text, node)
		case *comment.List:
			for _, item := range b.Items {
				w.walkBlocks(item.Content, node)
			}
		}
	}
}

func (w *docLinkWalker) walkText(text []comment.Text, node ast.Node) {
	for _, t := range text {
		if link, ok := t.(*comment.DocLink); ok && isWellFormedDocLink(link) {
			w.visitor.VisitDocLink(link, node)
		}
	}
}

// isWellFormedDocLink rejects links the parser accepts leniently, like [.Name].
func isWellFormedDocLink(link *comment.DocLink) bool {
	var text strings.Builder
	for _, t := range link.Text {
		plain, ok := t.(comment.Plain)
		if !ok {
			return false
		}
		text.WriteString(string(plain))
	}
	parts := make([]string, 0, 3)
	for _, p := range []string{link.ImportPath, link.Recv, link.Name} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.TrimPrefix(text.String(), "*") == strings.Join(parts, ".")
}
