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
	info.Summary = "Detects doc comment symbol links that do not resolve"
	info.Details = "Parses bracket links such as [Name], [pkg.Name], and [pkg.Type.Method] with go/doc/comment and checks them against the current package and the file's imports."
	info.Before = `// F delegates to [Missing].
func F() {}`
	info.After = `// F delegates to [Helper].
func F() {}`

	collection.AddChecker(&info, func(ctx *linter.CheckerContext) (linter.FileWalker, error) {
		c := &brokenDocLinkChecker{ctx: ctx}
		c.parser.LookupPackage = acceptDocPackage
		c.parser.LookupSym = func(_, _ string) bool { return true }
		return astwalk.WalkerForDocLink(c), nil
	})
}

// acceptDocPackage reports whether name can appear as a package
// qualifier inside a doc link. Names that are not identifiers — including
// text with spaces or other non-identifier characters — are rejected so
// the parser will not treat them as links.
func acceptDocPackage(name string) (string, bool) {
	if name == "_" || !token.IsIdentifier(name) {
		return "", false
	}
	return name, true
}

type brokenDocLinkChecker struct {
	astwalk.WalkHandler
	ctx    *linter.CheckerContext
	parser comment.Parser

	file   *ast.File
	byName map[string]*pkgRef
	byPath map[string][]*pkgRef
	dots   []*types.Package
	seen   map[string]struct{}
}

// pkgRef is a package imported by the current file.
// label is the name used in diagnostics: the local alias for a renamed
// import, or the package's own name for a dot import.
type pkgRef struct {
	pkg   *types.Package
	label string
}

func (c *brokenDocLinkChecker) VisitDocLink(f *ast.File, decl ast.Node, doc *ast.CommentGroup) {
	if f != c.file {
		c.file = f
		c.collectImports(f)
	}
	c.seen = make(map[string]struct{})

	parsed := c.parser.Parse(doc.Text())
	eachDocLink(parsed, func(link *comment.DocLink) {
		c.checkLink(decl, link)
	})
}

func (c *brokenDocLinkChecker) collectImports(f *ast.File) {
	c.byName = make(map[string]*pkgRef)
	c.byPath = make(map[string][]*pkgRef)
	c.dots = c.dots[:0]
	if f == nil || c.ctx.TypesInfo == nil {
		return
	}
	for _, spec := range f.Imports {
		pn := c.pkgName(spec)
		if pn == nil || pn.Imported() == nil {
			continue
		}
		local := pn.Name()
		if local == "_" {
			continue
		}
		pkg := pn.Imported()
		if local == "." {
			c.dots = append(c.dots, pkg)
			ref := &pkgRef{pkg: pkg, label: pkg.Name()}
			if _, exists := c.byName[pkg.Name()]; !exists {
				c.byName[pkg.Name()] = ref
			}
			c.byPath[pkg.Path()] = append(c.byPath[pkg.Path()], ref)
			continue
		}
		ref := &pkgRef{pkg: pkg, label: local}
		c.byName[local] = ref
		c.byPath[pkg.Path()] = append(c.byPath[pkg.Path()], ref)
	}
}

func (c *brokenDocLinkChecker) pkgName(spec *ast.ImportSpec) *types.PkgName {
	var obj types.Object
	if spec.Name != nil {
		obj = c.ctx.TypesInfo.ObjectOf(spec.Name)
	} else {
		obj = c.ctx.TypesInfo.Implicits[spec]
	}
	pn, _ := obj.(*types.PkgName)
	return pn
}

func (c *brokenDocLinkChecker) checkLink(decl ast.Node, link *comment.DocLink) {
	ref := docLinkText(link)
	if ref == "" {
		return
	}

	if link.ImportPath != "" {
		c.checkQualified(decl, ref, link)
		return
	}
	if link.Recv != "" {
		c.checkMember(decl, ref, c.lookupLocal(link.Recv), "", link.Recv, link.Name, true)
		return
	}
	if c.lookupLocal(link.Name) == nil {
		c.warn(decl, ref, "unknown symbol %q in current package", link.Name)
	}
}

func (c *brokenDocLinkChecker) checkQualified(decl ast.Node, ref string, link *comment.DocLink) {
	pkg, label, ok := c.resolvePkg(link.ImportPath)
	if !ok {
		// [int] and [len.X] refer to predeclared identifiers, not packages.
		if types.Universe.Lookup(link.ImportPath) != nil {
			return
		}
		// Bare [note] is ordinary prose. A slash or a known standard
		// library name is an actual package reference.
		if link.Name == "" && link.Recv == "" && !unambiguousPackageRef(link.ImportPath) {
			return
		}
		c.warn(decl, ref, "package %q is not imported", link.ImportPath)
		return
	}
	if link.Name == "" {
		return
	}
	if link.Recv != "" {
		var obj types.Object
		if pkg != nil {
			obj = pkg.Scope().Lookup(link.Recv)
		}
		c.checkMember(decl, ref, obj, label, link.Recv, link.Name, false)
		return
	}
	if pkg == nil || pkg.Scope().Lookup(link.Name) == nil {
		c.warn(decl, ref, "%q not found in package %q", link.Name, label)
	}
}

func (c *brokenDocLinkChecker) checkMember(decl ast.Node, ref string, obj types.Object, label, recv, name string, local bool) {
	if obj == nil {
		if local {
			c.warn(decl, ref, "type %q not found in current package", recv)
		} else {
			c.warn(decl, ref, "type %q not found in package %q", recv, label)
		}
		return
	}
	if types.Universe.Lookup(recv) == obj {
		return
	}
	tn, ok := obj.(*types.TypeName)
	if !ok {
		c.warn(decl, ref, "%q is not a type", recv)
		return
	}
	if !hasMethodOrField(tn.Type(), name) {
		c.warn(decl, ref, "type %q has no method or field %q", recv, name)
	}
}

// lookupLocal resolves an unqualified name in the current package,
// then in dot-imported packages, then in the universe (builtins).
func (c *brokenDocLinkChecker) lookupLocal(name string) types.Object {
	if c.ctx.Pkg != nil {
		if obj := c.ctx.Pkg.Scope().Lookup(name); obj != nil {
			return obj
		}
	}
	for _, pkg := range c.dots {
		if pkg == nil {
			continue
		}
		if obj := pkg.Scope().Lookup(name); obj != nil {
			return obj
		}
	}
	return types.Universe.Lookup(name)
}

// resolvePkg maps a doc-link qualifier to an imported package.
// qualifier is either a local import name or a full import path.
// The current package is used when the qualifier names that package.
func (c *brokenDocLinkChecker) resolvePkg(qualifier string) (pkg *types.Package, label string, ok bool) {
	if ref, found := c.byName[qualifier]; found {
		return ref.pkg, ref.label, true
	}
	if refs := c.byPath[qualifier]; len(refs) > 0 {
		ref := refs[0]
		for _, cand := range refs {
			// Prefer the import whose local name is the package's own name
			// when several imports share a path. A renamed import is used
			// when it is the only one, so diagnostics show the alias.
			if cand.label == cand.pkg.Name() {
				ref = cand
				break
			}
		}
		return ref.pkg, ref.label, true
	}
	if c.ctx.Pkg != nil && (qualifier == c.ctx.Pkg.Name() || qualifier == c.ctx.Pkg.Path()) {
		return c.ctx.Pkg, c.ctx.Pkg.Name(), true
	}
	return nil, "", false
}

func (c *brokenDocLinkChecker) warn(decl ast.Node, ref, format string, args ...any) {
	msg := fmt.Sprintf("[%s]: %s", ref, fmt.Sprintf(format, args...))
	if _, ok := c.seen[msg]; ok {
		return
	}
	c.seen[msg] = struct{}{}
	c.ctx.Warn(decl, "%s", msg)
}

// hasMethodOrField reports whether typ has an exported method or field name,
// including members promoted through embedded fields.
// addressable is set so pointer-receiver methods match [T.M] links.
func hasMethodOrField(typ types.Type, name string) bool {
	if typ == nil || name == "" {
		return false
	}
	obj, _, _ := types.LookupFieldOrMethod(typ, true, nil, name)
	return obj != nil
}

// unambiguousPackageRef reports whether a bare [pkg] link is clearly a
// package reference rather than prose such as [note].
func unambiguousPackageRef(path string) bool {
	if strings.Contains(path, "/") {
		return true
	}
	_, ok := comment.DefaultLookupPackage(path)
	return ok
}

func docLinkText(link *comment.DocLink) string {
	var b strings.Builder
	for _, t := range link.Text {
		switch t := t.(type) {
		case comment.Plain:
			b.WriteString(string(t))
		case comment.Italic:
			b.WriteString(string(t))
		}
	}
	return b.String()
}

func eachDocLink(doc *comment.Doc, fn func(*comment.DocLink)) {
	if doc == nil {
		return
	}
	var walkText func([]comment.Text)
	var walkBlock func(comment.Block)
	walkText = func(texts []comment.Text) {
		for _, t := range texts {
			if link, ok := t.(*comment.DocLink); ok {
				fn(link)
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
				for _, block := range item.Content {
					walkBlock(block)
				}
			}
		}
	}
	for _, b := range doc.Content {
		walkBlock(b)
	}
}
