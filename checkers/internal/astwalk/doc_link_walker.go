package astwalk

import (
	"go/ast"
	"go/doc/comment"
	"go/token"
)

type docLinkWalker struct {
	visitor DocLinkVisitor
	parser  comment.Parser
}

func newDocLinkWalker(v DocLinkVisitor) *docLinkWalker {
	return &docLinkWalker{
		visitor: v,
		parser: comment.Parser{
			// Accept every syntactically valid reference, so the visitor
			// can report the broken ones. Package parts other than full
			// import paths (which the parser validates itself) must be
			// identifiers, otherwise text like [some text] becomes a link.
			LookupPackage: func(name string) (string, bool) {
				return name, name != "_" && token.IsIdentifier(name)
			},
			// Called with an empty name for [] and [*].
			LookupSym: func(_, name string) bool {
				return name != ""
			},
		},
	}
}

func (w *docLinkWalker) WalkFile(f *ast.File) {
	walkDocComments(f, func(decl ast.Node, doc *ast.CommentGroup) {
		for _, block := range w.parser.Parse(doc.Text()).Content {
			w.walkBlock(decl, block)
		}
	})
}

func (w *docLinkWalker) walkBlock(decl ast.Node, block comment.Block) {
	switch block := block.(type) {
	case *comment.Paragraph:
		for _, text := range block.Text {
			if link, ok := text.(*comment.DocLink); ok {
				w.visitor.VisitDocLink(decl, link)
			}
		}
	case *comment.List:
		for _, item := range block.Items {
			for _, b := range item.Content {
				w.walkBlock(decl, b)
			}
		}
	}
}
