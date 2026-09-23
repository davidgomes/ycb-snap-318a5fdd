package checkers

import (
	"fmt"
	"go/ast"
	"go/doc/comment"
	"go/token"
	"go/types"
	"strings"

	"github.com/go-critic/go-critic/checkers/internal/astwalk"
	"github.com/go-critic/go-critic/linter"
)

func init() {
	var info linter.CheckerInfo
	info.Name = "brokenDocLink"
	info.Tags = []string{linter.DiagnosticTag, linter.ExperimentalTag}
	info.Summary = "Detects broken symbol links in doc comments"
	info.Before = `
// See [Missing].
func F() {}`
	info.After = `
// See [F].
func F() {}`

	collection.AddChecker(&info, func(ctx *linter.CheckerContext) (linter.FileWalker, error) {
		ctx.Require.PkgObjects = true
		return astwalk.WalkerForDocLink(&brokenDocLinkChecker{ctx: ctx}), nil
	})
}

type brokenDocLinkChecker struct {
	astwalk.WalkHandler
	ctx *linter.CheckerContext
}

func (c *brokenDocLinkChecker) VisitDocLink(decl ast.Node, doc *ast.CommentGroup) {
	if doc == nil || decl == nil {
		return
	}

	var parser comment.Parser
	parser.LookupSym = func(recv, name string) bool { return recv != "" || name != "" }
	parser.LookupPackage = func(name string) (string, bool) {
		if !token.IsIdentifier(name) {
			return "", false
		}
		// Imported names win over builtins so a package that shadows a
		// predeclared identifier is still resolved.
		if c.importedByName(name) {
			return name, true
		}
		if isBuiltin(name) {
			return "", false
		}
		// Accept other identifiers so unimported package links are reported.
		return name, true
	}

	parsed := parser.Parse(doc.Text())
	seen := make(map[string]struct{})
	for _, link := range docLinksIn(parsed) {
		ref := linkRef(link)
		if ref == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		if reason := c.checkLink(link); reason != "" {
			c.ctx.Warn(decl, "[%s]: %s", ref, reason)
		}
	}
}

func (c *brokenDocLinkChecker) checkLink(link *comment.DocLink) string {
	if link.ImportPath != "" {
		return c.checkQualified(link)
	}
	return c.checkLocal(link)
}

func (c *brokenDocLinkChecker) checkQualified(link *comment.DocLink) string {
	qual := link.ImportPath
	// A builtin name is not a package reference unless the file imports it.
	if isBuiltin(qual) && !c.importedByName(qual) {
		return ""
	}

	pkg, label, ok := c.resolvePkg(qual)
	if !ok {
		return fmt.Sprintf("package %q is not imported", qual)
	}
	if link.Name == "" {
		return ""
	}
	if link.Recv == "" {
		if pkg.Scope().Lookup(link.Name) == nil {
			return fmt.Sprintf("%q not found in package %q", link.Name, label)
		}
		return ""
	}

	obj := pkg.Scope().Lookup(link.Recv)
	if obj == nil {
		return fmt.Sprintf("type %q not found in package %q", link.Recv, label)
	}
	tn, isType := obj.(*types.TypeName)
	if !isType {
		return fmt.Sprintf("%q is not a type", link.Recv)
	}
	if !hasMethodOrField(tn, link.Name) {
		return fmt.Sprintf("type %q has no method or field %q", link.Recv, link.Name)
	}
	return ""
}

func (c *brokenDocLinkChecker) checkLocal(link *comment.DocLink) string {
	if link.Name == "" {
		return ""
	}
	if isBuiltin(link.Name) && link.Recv == "" {
		return ""
	}

	if link.Recv == "" {
		if c.lookupLocal(link.Name) == nil {
			return fmt.Sprintf("unknown symbol %q in current package", link.Name)
		}
		return ""
	}

	if isBuiltin(link.Recv) && !c.declaredLocally(link.Recv) {
		return ""
	}

	obj := c.lookupLocal(link.Recv)
	if obj == nil {
		return fmt.Sprintf("type %q not found in current package", link.Recv)
	}
	tn, isType := obj.(*types.TypeName)
	if !isType {
		return fmt.Sprintf("%q is not a type", link.Recv)
	}
	if !hasMethodOrField(tn, link.Name) {
		return fmt.Sprintf("type %q has no method or field %q", link.Recv, link.Name)
	}
	return ""
}

// lookupLocal resolves a name in the current package, the universe,
// and packages brought into file scope by a dot import.
func (c *brokenDocLinkChecker) lookupLocal(name string) types.Object {
	if obj := c.ctx.Pkg.Scope().Lookup(name); obj != nil {
		return obj
	}
	if obj := types.Universe.Lookup(name); obj != nil {
		return obj
	}
	for pkgName, local := range c.ctx.PkgObjects {
		if local != "." {
			continue
		}
		obj := pkgName.Imported().Scope().Lookup(name)
		if obj != nil && obj.Exported() {
			return obj
		}
	}
	return nil
}

func (c *brokenDocLinkChecker) declaredLocally(name string) bool {
	return c.ctx.Pkg.Scope().Lookup(name) != nil
}

func (c *brokenDocLinkChecker) importedByName(name string) bool {
	for _, local := range c.ctx.PkgObjects {
		if local == name {
			return true
		}
	}
	return false
}

// resolvePkg finds the imported package referred to by qual.
// qual is either a local import name or a full import path.
// For a renamed import, label is the local alias.
func (c *brokenDocLinkChecker) resolvePkg(qual string) (pkg *types.Package, label string, ok bool) {
	fullPath := strings.Contains(qual, "/")
	var localName string
	for pkgName, local := range c.ctx.PkgObjects {
		imp := pkgName.Imported()
		match := false
		if fullPath {
			match = imp.Path() == qual
		} else {
			match = local == qual
		}
		if !match {
			continue
		}
		if ok && !preferLocalName(local, localName) {
			continue
		}
		pkg = imp
		localName = local
		ok = true
	}
	if !ok {
		return nil, "", false
	}
	// Prefer the name the file uses for the package. A renamed import
	// contributes its local alias; otherwise this is the package name.
	label = qual
	if usableImportName(localName) {
		label = localName
	}
	return pkg, label, true
}

// preferLocalName reports whether candidate should replace current
// when several imports match the same path. A usable alias is preferred
// over a blank or dot import.
func preferLocalName(candidate, current string) bool {
	return usableImportName(candidate) && !usableImportName(current)
}

func usableImportName(name string) bool {
	return name != "" && name != "." && name != "_"
}

func hasMethodOrField(tn *types.TypeName, name string) bool {
	obj, _, _ := types.LookupFieldOrMethod(tn.Type(), true, tn.Pkg(), name)
	return obj != nil
}

func docLinksIn(doc *comment.Doc) []*comment.DocLink {
	if doc == nil {
		return nil
	}
	var links []*comment.DocLink
	for _, block := range doc.Content {
		collectBlockLinks(block, &links)
	}
	return links
}

func collectBlockLinks(block comment.Block, links *[]*comment.DocLink) {
	switch block := block.(type) {
	case *comment.Paragraph:
		collectTextLinks(block.Text, links)
	case *comment.Heading:
		collectTextLinks(block.Text, links)
	case *comment.List:
		for _, item := range block.Items {
			for _, inner := range item.Content {
				collectBlockLinks(inner, links)
			}
		}
	}
}

func collectTextLinks(texts []comment.Text, links *[]*comment.DocLink) {
	for _, text := range texts {
		switch text := text.(type) {
		case *comment.DocLink:
			*links = append(*links, text)
		case *comment.Link:
			collectTextLinks(text.Text, links)
		}
	}
}

func linkRef(link *comment.DocLink) string {
	ref := flattenText(link.Text)
	if ref != "" {
		return ref
	}
	// Fall back to the parsed target if the display text is empty.
	switch {
	case link.ImportPath != "" && link.Recv != "" && link.Name != "":
		return link.ImportPath + "." + link.Recv + "." + link.Name
	case link.ImportPath != "" && link.Name != "":
		return link.ImportPath + "." + link.Name
	case link.ImportPath != "":
		return link.ImportPath
	case link.Recv != "" && link.Name != "":
		return link.Recv + "." + link.Name
	default:
		return link.Name
	}
}

func flattenText(texts []comment.Text) string {
	var b strings.Builder
	for _, text := range texts {
		switch text := text.(type) {
		case comment.Plain:
			b.WriteString(string(text))
		case comment.Italic:
			b.WriteString(string(text))
		case *comment.Link:
			b.WriteString(flattenText(text.Text))
		case *comment.DocLink:
			b.WriteString(flattenText(text.Text))
		}
	}
	return b.String()
}
