package checkers

import (
	"go/ast"
	"go/doc/comment"
	"go/token"
	"go/types"
	"strings"
	"unicode"

	"github.com/go-critic/go-critic/checkers/internal/astwalk"
	"github.com/go-critic/go-critic/linter"
)

func init() {
	var info linter.CheckerInfo
	info.Name = "brokenDocLink"
	info.Tags = []string{linter.DiagnosticTag, linter.ExperimentalTag}
	info.Summary = "Detects doc comment symbol links that refer to missing symbols"
	info.Before = `
// See [Missing] for details.
func F() {}`
	info.After = `
// See [F] for details.
func F() {}`

	collection.AddChecker(&info, func(ctx *linter.CheckerContext) (linter.FileWalker, error) {
		return astwalk.WalkerForDocLink(&brokenDocLinkChecker{ctx: ctx}), nil
	})
}

type brokenDocLinkChecker struct {
	astwalk.WalkHandler
	ctx *linter.CheckerContext
}

func (c *brokenDocLinkChecker) VisitDocLink(f *ast.File, decl ast.Node, doc *ast.CommentGroup) {
	var parser comment.Parser
	parser.LookupPackage = func(name string) (string, bool) {
		// Accept any identifier so the parser yields the link.
		// Whether the package is actually imported is checked later.
		if !token.IsIdentifier(name) {
			return "", false
		}
		return name, true
	}
	parser.LookupSym = func(recv, name string) bool {
		if !token.IsExported(name) || !token.IsIdentifier(name) {
			return false
		}
		return recv == "" || token.IsIdentifier(recv)
	}

	parsed := parser.Parse(doc.Text())
	for _, link := range docLinks(parsed) {
		ref := docLinkText(link)
		if ref == "" || !validDocLinkText(ref) {
			continue
		}
		if reason := c.checkLink(f, link); reason != "" {
			c.ctx.Warn(decl, "[%s]: %s", ref, reason)
		}
	}
}

func (c *brokenDocLinkChecker) checkLink(f *ast.File, link *comment.DocLink) string {
	if link.ImportPath != "" {
		return c.checkQualified(f, link)
	}
	if link.Recv != "" {
		return c.checkMember(f, nil, "", link.Recv, link.Name)
	}
	return c.checkLocalSymbol(f, link.Name)
}

func (c *brokenDocLinkChecker) checkLocalSymbol(f *ast.File, name string) string {
	if _, ok := c.lookupLocal(f, name); ok {
		return ""
	}
	return `unknown symbol "` + name + `" in current package`
}

func (c *brokenDocLinkChecker) checkQualified(f *ast.File, link *comment.DocLink) string {
	pkgName := link.ImportPath
	if isPredeclared(pkgName) {
		return ""
	}
	pkg, ok := c.lookupImport(f, pkgName)
	if !ok {
		return `package "` + pkgName + `" is not imported`
	}
	if link.Name == "" {
		return ""
	}
	if link.Recv != "" {
		return c.checkMember(f, pkg, pkgName, link.Recv, link.Name)
	}
	if isPredeclared(link.Name) {
		return ""
	}
	if pkg.Scope().Lookup(link.Name) == nil {
		return `"` + link.Name + `" not found in package "` + pkgName + `"`
	}
	return ""
}

func (c *brokenDocLinkChecker) checkMember(f *ast.File, pkg *types.Package, pkgName, recv, member string) string {
	if isPredeclared(recv) || isPredeclared(member) {
		return ""
	}
	var obj types.Object
	if pkg == nil {
		var found bool
		obj, found = c.lookupLocal(f, recv)
		if !found {
			return `type "` + recv + `" not found in current package`
		}
		// lookupLocal reports predeclared names as found with a nil object.
		if obj == nil {
			return ""
		}
	} else {
		obj = pkg.Scope().Lookup(recv)
		if obj == nil {
			return `type "` + recv + `" not found in package "` + pkgName + `"`
		}
	}
	tn, ok := obj.(*types.TypeName)
	if !ok || tn.Type() == nil {
		return `"` + recv + `" is not a type`
	}
	found, _, _ := types.LookupFieldOrMethod(tn.Type(), true, tn.Pkg(), member)
	if found == nil {
		return `type "` + recv + `" has no method or field "` + member + `"`
	}
	return ""
}

// lookupLocal resolves name in the current package, including symbols
// introduced by dot imports. A predeclared identifier counts as resolved.
func (c *brokenDocLinkChecker) lookupLocal(f *ast.File, name string) (types.Object, bool) {
	if f != nil && c.ctx.TypesInfo != nil {
		if scope := c.ctx.TypesInfo.Scopes[f]; scope != nil {
			if obj := scope.Lookup(name); obj != nil {
				return obj, true
			}
		}
	}
	if c.ctx.Pkg != nil {
		if obj := c.ctx.Pkg.Scope().Lookup(name); obj != nil {
			return obj, true
		}
	}
	if isPredeclared(name) {
		return nil, true
	}
	return nil, false
}

// lookupImport resolves a doc-link package qualifier against the file's
// imports. name is either a local import name (the alias, when renamed)
// or a full import path. Dot-imported packages do not add a qualifier.
func (c *brokenDocLinkChecker) lookupImport(f *ast.File, name string) (*types.Package, bool) {
	if f == nil || c.ctx.TypesInfo == nil {
		return nil, false
	}
	byPath := strings.Contains(name, "/")
	for _, spec := range f.Imports {
		pn := pkgNameOf(c.ctx.TypesInfo, spec)
		if pn == nil || pn.Imported() == nil {
			continue
		}
		local := pn.Name()
		if byPath && pn.Imported().Path() == name {
			return pn.Imported(), true
		}
		if local == "_" || local == "." {
			continue
		}
		if !byPath && local == name {
			return pn.Imported(), true
		}
	}
	return nil, false
}

func pkgNameOf(info *types.Info, spec *ast.ImportSpec) *types.PkgName {
	if info == nil {
		return nil
	}
	if obj := info.Implicits[spec]; obj != nil {
		if pn, ok := obj.(*types.PkgName); ok {
			return pn
		}
	}
	if spec.Name != nil {
		if obj := info.Defs[spec.Name]; obj != nil {
			if pn, ok := obj.(*types.PkgName); ok {
				return pn
			}
		}
	}
	return nil
}

func isPredeclared(name string) bool {
	return name != "" && (isBuiltin(name) || types.Universe.Lookup(name) != nil)
}

// validDocLinkText reports whether ref can be a bracket-notation symbol link.
// Text containing spaces or characters outside identifiers, dots, slashes,
// and a leading pointer star is not a symbol reference.
func validDocLinkText(ref string) bool {
	ref = strings.TrimPrefix(ref, "*")
	if ref == "" || strings.ContainsAny(ref, " \t\n\r") {
		return false
	}
	for _, r := range ref {
		if r == '.' || r == '/' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		return false
	}
	return true
}

func docLinks(doc *comment.Doc) []*comment.DocLink {
	if doc == nil {
		return nil
	}
	var links []*comment.DocLink
	var walkBlock func(comment.Block)
	var walkText func([]comment.Text)
	walkText = func(text []comment.Text) {
		for _, t := range text {
			switch t := t.(type) {
			case *comment.DocLink:
				links = append(links, t)
			case *comment.Link:
				walkText(t.Text)
			}
		}
	}
	walkBlock = func(b comment.Block) {
		switch b := b.(type) {
		case *comment.Paragraph:
			walkText(b.Text)
		case *comment.Heading:
			walkText(b.Text)
		case *comment.List:
			for _, item := range b.Items {
				for _, c := range item.Content {
					walkBlock(c)
				}
			}
		}
	}
	for _, b := range doc.Content {
		walkBlock(b)
	}
	return links
}

func docLinkText(link *comment.DocLink) string {
	var b strings.Builder
	var walk func([]comment.Text)
	walk = func(text []comment.Text) {
		for _, t := range text {
			switch t := t.(type) {
			case comment.Plain:
				b.WriteString(string(t))
			case comment.Italic:
				b.WriteString(string(t))
			case *comment.Link:
				walk(t.Text)
			}
		}
	}
	walk(link.Text)
	return b.String()
}
