package checkers

import (
	"fmt"
	"go/ast"
	"go/doc/comment"
	"go/token"
	"go/types"
	"regexp"
	"strings"

	"github.com/go-critic/go-critic/checkers/internal/astwalk"
	"github.com/go-critic/go-critic/linter"
)

func init() {
	var info linter.CheckerInfo
	info.Name = "brokenDocLink"
	info.Tags = []string{linter.DiagnosticTag, linter.ExperimentalTag}
	info.Summary = "Detects doc comment symbol links that do not resolve"
	info.Before = `
// See [Unknown].
func F() {}`
	info.After = `
// See [Known].
func Known() {}
func F() {}`

	collection.AddChecker(&info, func(ctx *linter.CheckerContext) (linter.FileWalker, error) {
		ctx.Require.PkgObjects = true
		c := &brokenDocLinkChecker{ctx: ctx}
		c.parser.LookupPackage = func(name string) (string, bool) {
			if !token.IsIdentifier(name) {
				return "", false
			}
			// Accept every identifier so broken links are still parsed.
			// Existence is checked afterwards against type information.
			return name, true
		}
		c.parser.LookupSym = func(recv, name string) bool {
			if !token.IsIdentifier(name) || !token.IsExported(name) {
				return false
			}
			if recv != "" && (!token.IsIdentifier(recv) || !token.IsExported(recv)) {
				return false
			}
			return true
		}
		return astwalk.WalkerForDocLink(c), nil
	})
}

type brokenDocLinkChecker struct {
	astwalk.WalkHandler
	ctx    *linter.CheckerContext
	parser comment.Parser
}

func (c *brokenDocLinkChecker) VisitDocLink(decl ast.Node, doc *ast.CommentGroup) {
	text := docCommentText(doc)
	if strings.TrimSpace(text) == "" {
		return
	}
	parsed := c.parser.Parse(text)
	seen := make(map[string]struct{})
	for _, link := range docLinks(parsed) {
		ref := docLinkRef(link)
		reason := c.checkRef(ref)
		if reason == "" {
			continue
		}
		msg := "[" + ref + "]: " + reason
		if _, ok := seen[msg]; ok {
			continue
		}
		seen[msg] = struct{}{}
		c.ctx.Warn(decl, "%s", msg)
	}
}

// docCommentText returns comment text with lint-test directives removed.
// Those directives sit between the doc comment and the declaration, so
// they belong to the same comment group, but they are not part of the doc.
// lintTestDirective matches a single-line warning marker used by linttest,
// for example `/*! [Name]: reason */`. It is attached to the doc comment
// group but is not part of the documented text.
var lintTestDirective = regexp.MustCompile(`^/\*! .+ \*/$`)

func docCommentText(doc *ast.CommentGroup) string {
	if doc == nil {
		return ""
	}
	list := make([]*ast.Comment, 0, len(doc.List))
	for _, comm := range doc.List {
		if lintTestDirective.MatchString(comm.Text) {
			continue
		}
		list = append(list, comm)
	}
	if len(list) == 0 {
		return ""
	}
	return (&ast.CommentGroup{List: list}).Text()
}

func docLinks(doc *comment.Doc) []*comment.DocLink {
	var links []*comment.DocLink
	if doc == nil {
		return links
	}
	for _, block := range doc.Content {
		collectDocLinks(block, &links)
	}
	return links
}

func collectDocLinks(block comment.Block, links *[]*comment.DocLink) {
	switch block := block.(type) {
	case *comment.Paragraph:
		collectTextLinks(block.Text, links)
	case *comment.Heading:
		collectTextLinks(block.Text, links)
	case *comment.List:
		for _, item := range block.Items {
			for _, child := range item.Content {
				collectDocLinks(child, links)
			}
		}
	}
}

func collectTextLinks(texts []comment.Text, links *[]*comment.DocLink) {
	for _, text := range texts {
		if link, ok := text.(*comment.DocLink); ok {
			*links = append(*links, link)
		}
	}
}

func docLinkRef(link *comment.DocLink) string {
	var b strings.Builder
	for _, text := range link.Text {
		switch text := text.(type) {
		case comment.Plain:
			b.WriteString(string(text))
		case comment.Italic:
			b.WriteString(string(text))
		}
	}
	return b.String()
}

// checkRef validates ref, the text inside a doc-link bracket pair.
// An empty result means the reference is valid or not a symbol link.
func (c *brokenDocLinkChecker) checkRef(ref string) string {
	body := ref
	if strings.HasPrefix(body, "*") {
		body = body[1:]
	}
	if body == "" || strings.Contains(body, "*") {
		return ""
	}
	parts := strings.Split(body, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return ""
	}
	for _, part := range parts {
		if !token.IsIdentifier(part) {
			return ""
		}
	}

	switch len(parts) {
	case 1:
		return c.checkName(parts[0])
	case 2:
		return c.checkSelector(parts[0], parts[1])
	default:
		return c.checkQualifiedMember(parts[0], parts[1], parts[2])
	}
}

func (c *brokenDocLinkChecker) checkName(name string) string {
	if c.lookupLocal(name) != nil || isBuiltin(name) {
		return ""
	}
	// Bare unexported names are package links or prose, not symbols.
	// A missing package is reported from a qualified link instead.
	if !token.IsExported(name) {
		return ""
	}
	return fmt.Sprintf("unknown symbol %q in current package", name)
}

func (c *brokenDocLinkChecker) checkSelector(qual, name string) string {
	if pkg, ok := c.importByName(qual); ok {
		if pkg.Scope().Lookup(name) == nil {
			return fmt.Sprintf("%q not found in package %q", name, qual)
		}
		return ""
	}
	if token.IsExported(qual) {
		return c.checkLocalMember(qual, name)
	}
	// A package-scoped declaration shadows a builtin of the same name.
	if c.ctx.Pkg != nil {
		if obj := c.ctx.Pkg.Scope().Lookup(qual); obj != nil {
			return c.checkMemberObject(obj, qual, name)
		}
	}
	if isBuiltin(qual) {
		return c.checkMemberObject(types.Universe.Lookup(qual), qual, name)
	}
	if c.isCurrentPackage(qual) {
		if c.ctx.Pkg.Scope().Lookup(name) == nil {
			return fmt.Sprintf("%q not found in package %q", name, qual)
		}
		return ""
	}
	return fmt.Sprintf("package %q is not imported", qual)
}

func (c *brokenDocLinkChecker) checkQualifiedMember(qual, typeName, member string) string {
	pkg, ok := c.importByName(qual)
	if !ok && c.isCurrentPackage(qual) {
		pkg = c.ctx.Pkg
		ok = pkg != nil
	}
	if !ok {
		return fmt.Sprintf("package %q is not imported", qual)
	}
	obj := pkg.Scope().Lookup(typeName)
	if obj == nil {
		return fmt.Sprintf("type %q not found in package %q", typeName, qual)
	}
	tn, isType := obj.(*types.TypeName)
	if !isType {
		return fmt.Sprintf("%q is not a type", typeName)
	}
	if !hasMethodOrField(tn, member) {
		return fmt.Sprintf("type %q has no method or field %q", typeName, member)
	}
	return ""
}

func (c *brokenDocLinkChecker) checkLocalMember(typeName, member string) string {
	obj := c.lookupLocal(typeName)
	if obj == nil {
		return fmt.Sprintf("type %q not found in current package", typeName)
	}
	return c.checkMemberObject(obj, typeName, member)
}

func (c *brokenDocLinkChecker) checkMemberObject(obj types.Object, typeName, member string) string {
	if obj == nil {
		if isBuiltin(typeName) {
			obj = types.Universe.Lookup(typeName)
		}
	}
	if obj == nil {
		return fmt.Sprintf("type %q not found in current package", typeName)
	}
	tn, isType := obj.(*types.TypeName)
	if !isType {
		return fmt.Sprintf("%q is not a type", typeName)
	}
	if !hasMethodOrField(tn, member) {
		return fmt.Sprintf("type %q has no method or field %q", typeName, member)
	}
	return ""
}

func hasMethodOrField(tn *types.TypeName, member string) bool {
	typ := tn.Type()
	if typ == nil {
		return false
	}
	obj, _, _ := types.LookupFieldOrMethod(typ, true, tn.Pkg(), member)
	return obj != nil
}

func (c *brokenDocLinkChecker) lookupLocal(name string) types.Object {
	if c.ctx.Pkg != nil {
		if obj := c.ctx.Pkg.Scope().Lookup(name); obj != nil {
			return obj
		}
	}
	for pkgName, local := range c.ctx.PkgObjects {
		if local != "." || pkgName == nil {
			continue
		}
		imported := pkgName.Imported()
		if imported == nil {
			continue
		}
		if obj := imported.Scope().Lookup(name); obj != nil && obj.Exported() {
			return obj
		}
	}
	return types.Universe.Lookup(name)
}

func (c *brokenDocLinkChecker) importByName(name string) (*types.Package, bool) {
	if name == "" || name == "." || name == "_" {
		return nil, false
	}
	for pkgName, local := range c.ctx.PkgObjects {
		if local != name || pkgName == nil {
			continue
		}
		imported := pkgName.Imported()
		if imported == nil {
			continue
		}
		return imported, true
	}
	return nil, false
}

func (c *brokenDocLinkChecker) isCurrentPackage(name string) bool {
	return c.ctx.Pkg != nil && c.ctx.Pkg.Name() == name
}
