package checkers

import (
	"go/ast"
	"go/doc/comment"
	"go/types"
	"strconv"
	"strings"

	"github.com/go-critic/go-critic/checkers/internal/astwalk"
	"github.com/go-critic/go-critic/linter"
)

func init() {
	var info linter.CheckerInfo
	info.Name = "brokenDocLink"
	info.Tags = []string{linter.DiagnosticTag, linter.ExperimentalTag}
	info.Summary = "Detects doc-comment symbol links that refer to unknown symbols"
	info.Before = `
// Parse is like [ParseFile], but reads from [strings.Readr].
func Parse(r io.Reader) {}`
	info.After = `
// Parse is like [ParseFiles], but reads from [strings.Reader].
func Parse(r io.Reader) {}`

	collection.AddChecker(&info, func(ctx *linter.CheckerContext) (linter.FileWalker, error) {
		return astwalk.WalkerForDocLink(&brokenDocLinkChecker{ctx: ctx}), nil
	})
}

type docLinkImport struct {
	pkg  *types.Package
	name string
}

type brokenDocLinkChecker struct {
	astwalk.WalkHandler
	ctx *linter.CheckerContext

	importsByName map[string]docLinkImport
	importsByPath map[string]docLinkImport
	dotImports    []*types.Package

	reported map[ast.Node]map[string]bool
}

func (c *brokenDocLinkChecker) EnterFile(f *ast.File) bool {
	if c.ctx.Pkg == nil {
		return false
	}

	c.importsByName = make(map[string]docLinkImport)
	c.importsByPath = make(map[string]docLinkImport)
	c.dotImports = c.dotImports[:0]
	c.reported = make(map[ast.Node]map[string]bool)

	imported := make(map[string]*types.Package)
	for _, pkg := range c.ctx.Pkg.Imports() {
		imported[pkg.Path()] = pkg
	}

	for _, spec := range f.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		pkg := imported[path]
		if pkg == nil {
			continue
		}
		name := pkg.Name()
		if spec.Name != nil {
			name = spec.Name.Name
		}
		switch name {
		case "_":
			continue
		case ".":
			c.dotImports = append(c.dotImports, pkg)
			continue
		}
		imp := docLinkImport{pkg: pkg, name: name}
		c.importsByName[name] = imp
		c.importsByPath[path] = imp
	}

	return true
}

func (c *brokenDocLinkChecker) VisitDocLink(link *comment.DocLink, node ast.Node) {
	ref := docLinkText(link)

	if link.ImportPath == "" {
		if link.Recv == "" {
			if c.lookupLocal(link.Name) == nil {
				c.warn(node, ref, "unknown symbol %q in current package", link.Name)
			}
			return
		}
		c.checkMember(node, ref, c.lookupLocal(link.Recv), link.Recv, link.Name, "")
		return
	}

	qualifier := link.ImportPath
	isPath := strings.Contains(qualifier, "/")

	if link.Name == "" {
		// Link to a package or, since unexported names can't form
		// symbol links, to a lower-case local symbol or builtin.
		if isPath || c.lookupImport(qualifier) != nil || c.lookupLocal(qualifier) != nil {
			return
		}
		if _, ok := comment.DefaultLookupPackage(qualifier); ok {
			return
		}
		c.warn(node, ref, "unknown symbol %q in current package", qualifier)
		return
	}

	imp := c.lookupImport(qualifier)
	if imp == nil {
		// Lower-case local types and builtins are parsed as package names,
		// like in [myType.Method] or [error.Error].
		if !isPath && link.Recv == "" {
			if obj := c.lookupLocal(qualifier); obj != nil {
				c.checkMember(node, ref, obj, qualifier, link.Name, "")
				return
			}
		}
		c.warn(node, ref, "package %q is not imported", qualifier)
		return
	}

	scope := imp.pkg.Scope()
	if link.Recv == "" {
		if scope.Lookup(link.Name) == nil {
			c.warn(node, ref, "%q not found in package %q", link.Name, qualifier)
		}
		return
	}
	c.checkMember(node, ref, scope.Lookup(link.Recv), link.Recv, link.Name, qualifier)
}

func (c *brokenDocLinkChecker) checkMember(node ast.Node, ref string, obj types.Object, recv, name, pkgName string) {
	if obj == nil {
		if pkgName == "" {
			c.warn(node, ref, "type %q not found in current package", recv)
		} else {
			c.warn(node, ref, "type %q not found in package %q", recv, pkgName)
		}
		return
	}
	typeName, ok := obj.(*types.TypeName)
	if !ok {
		c.warn(node, ref, "%q is not a type", recv)
		return
	}
	member, _, _ := types.LookupFieldOrMethod(typeName.Type(), true, typeName.Pkg(), name)
	if member == nil {
		c.warn(node, ref, "type %q has no method or field %q", recv, name)
	}
}

func (c *brokenDocLinkChecker) lookupImport(qualifier string) *docLinkImport {
	if imp, ok := c.importsByName[qualifier]; ok {
		return &imp
	}
	if strings.Contains(qualifier, "/") {
		if imp, ok := c.importsByPath[qualifier]; ok {
			return &imp
		}
	}
	if qualifier == c.ctx.Pkg.Name() || qualifier == c.ctx.Pkg.Path() {
		return &docLinkImport{pkg: c.ctx.Pkg, name: qualifier}
	}
	return nil
}

// lookupLocal finds a symbol that can be referenced without a package qualifier.
func (c *brokenDocLinkChecker) lookupLocal(name string) types.Object {
	if obj := c.ctx.Pkg.Scope().Lookup(name); obj != nil {
		return obj
	}
	for _, pkg := range c.dotImports {
		if obj := pkg.Scope().Lookup(name); obj != nil && obj.Exported() {
			return obj
		}
	}
	return types.Universe.Lookup(name)
}

func (c *brokenDocLinkChecker) warn(node ast.Node, ref, format string, args ...interface{}) {
	seen := c.reported[node]
	if seen == nil {
		seen = make(map[string]bool)
		c.reported[node] = seen
	}
	if seen[ref] {
		return
	}
	seen[ref] = true
	c.ctx.Warn(node, "[%s]: "+format, append([]interface{}{ref}, args...)...)
}

func docLinkText(link *comment.DocLink) string {
	var sb strings.Builder
	for _, t := range link.Text {
		if s, ok := t.(comment.Plain); ok {
			sb.WriteString(string(s))
		}
	}
	return sb.String()
}
