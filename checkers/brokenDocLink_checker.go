package checkers

import (
	"go/ast"
	"go/doc/comment"
	"go/types"
	"strings"

	"github.com/go-critic/go-critic/checkers/internal/astwalk"
	"github.com/go-critic/go-critic/linter"
)

func init() {
	var info linter.CheckerInfo
	info.Name = "brokenDocLink"
	info.Tags = []string{linter.DiagnosticTag, linter.ExperimentalTag}
	info.Summary = "Detects doc-comment links to symbols that do not exist"
	info.Before = `
// ReadAll reads from r until EOF, see [io.Reder].
func ReadAll(r io.Reader) ([]byte, error)`
	info.After = `
// ReadAll reads from r until EOF, see [io.Reader].
func ReadAll(r io.Reader) ([]byte, error)`

	collection.AddChecker(&info, func(ctx *linter.CheckerContext) (linter.FileWalker, error) {
		ctx.Require.PkgObjects = true
		c := &brokenDocLinkChecker{
			ctx:  ctx,
			seen: make(map[string]bool),
		}
		return astwalk.WalkerForDocLink(c), nil
	})
}

type brokenDocLinkChecker struct {
	ctx *linter.CheckerContext

	// decl and seen make every broken link reported once per declaration.
	decl ast.Node
	seen map[string]bool
}

func (c *brokenDocLinkChecker) VisitDocLink(decl ast.Node, link *comment.DocLink) {
	if decl != c.decl {
		c.decl = decl
		clear(c.seen)
	}
	ref := docLinkText(link)
	if c.seen[ref] {
		return
	}
	c.seen[ref] = true

	if link.ImportPath == "" {
		c.checkLocalLink(decl, ref, link)
	} else {
		c.checkQualifiedLink(decl, ref, link)
	}
}

func (c *brokenDocLinkChecker) checkLocalLink(decl ast.Node, ref string, link *comment.DocLink) {
	if link.Recv == "" {
		if c.lookupLocal(link.Name) == nil {
			c.warnf(decl, ref, "unknown symbol %q in current package", link.Name)
		}
		return
	}
	recv := c.lookupLocal(link.Recv)
	if recv == nil {
		c.warnf(decl, ref, "type %q not found in current package", link.Recv)
		return
	}
	c.checkMember(decl, ref, recv, link.Name)
}

func (c *brokenDocLinkChecker) checkQualifiedLink(decl ast.Node, ref string, link *comment.DocLink) {
	pkgRef := link.ImportPath
	pkg := c.lookupPackage(pkgRef)
	if pkg == nil {
		// Lowercase names like [error] are parsed as package links.
		if obj := types.Universe.Lookup(pkgRef); obj != nil {
			if link.Recv == "" && link.Name != "" {
				c.checkMember(decl, ref, obj, link.Name)
			}
			return
		}
		c.warnf(decl, ref, "package %q is not imported", pkgRef)
		return
	}

	switch {
	case link.Name == "":
		return
	case link.Recv == "":
		if pkg.Scope().Lookup(link.Name) == nil {
			c.warnf(decl, ref, "%q not found in package %q", link.Name, pkgRef)
		}
	default:
		recv := pkg.Scope().Lookup(link.Recv)
		if recv == nil {
			c.warnf(decl, ref, "type %q not found in package %q", link.Recv, pkgRef)
			return
		}
		c.checkMember(decl, ref, recv, link.Name)
	}
}

func (c *brokenDocLinkChecker) checkMember(decl ast.Node, ref string, recv types.Object, name string) {
	if _, ok := recv.(*types.TypeName); !ok {
		c.warnf(decl, ref, "%q is not a type", recv.Name())
		return
	}
	if obj, _, _ := types.LookupFieldOrMethod(recv.Type(), true, recv.Pkg(), name); obj == nil {
		c.warnf(decl, ref, "type %q has no method or field %q", recv.Name(), name)
	}
}

// lookupLocal finds a package-level symbol of the current package,
// including the ones that are dot-imported into the current file.
func (c *brokenDocLinkChecker) lookupLocal(name string) types.Object {
	if obj := c.ctx.Pkg.Scope().Lookup(name); obj != nil {
		return obj
	}
	for pkgName, localName := range c.ctx.PkgObjects {
		if localName != "." {
			continue
		}
		if obj := pkgName.Imported().Scope().Lookup(name); obj != nil && obj.Exported() {
			return obj
		}
	}
	return nil
}

// lookupPackage resolves the package part of a qualified link,
// which is either a local package name or a full import path.
func (c *brokenDocLinkChecker) lookupPackage(pkgRef string) *types.Package {
	isPath := strings.Contains(pkgRef, "/")
	for pkgName, localName := range c.ctx.PkgObjects {
		imported := pkgName.Imported()
		if localName == pkgRef || (isPath && imported.Path() == pkgRef) {
			return imported
		}
	}
	if pkgRef == c.ctx.Pkg.Name() || pkgRef == c.ctx.Pkg.Path() {
		return c.ctx.Pkg
	}
	return nil
}

func (c *brokenDocLinkChecker) warnf(decl ast.Node, ref, format string, args ...interface{}) {
	c.ctx.Warn(decl, "[%s]: "+format, append([]interface{}{ref}, args...)...)
}

// docLinkText returns the link text as it is written inside the brackets.
func docLinkText(link *comment.DocLink) string {
	var sb strings.Builder
	for _, text := range link.Text {
		switch text := text.(type) {
		case comment.Plain:
			sb.WriteString(string(text))
		case comment.Italic:
			sb.WriteString(string(text))
		}
	}
	return sb.String()
}
