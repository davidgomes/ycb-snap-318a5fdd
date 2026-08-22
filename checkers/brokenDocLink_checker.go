package checkers

import (
	"go/ast"
	"go/doc/comment"
	"go/types"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-critic/go-critic/checkers/internal/astwalk"
	"github.com/go-critic/go-critic/linter"
)

func init() {
	var info linter.CheckerInfo
	info.Name = "brokenDocLink"
	info.Tags = []string{linter.DiagnosticTag}
	info.Summary = "Detects doc comment symbol links that refer to missing symbols"
	info.Before = `
// See [MissingType] for details.
type MyType int`
	info.After = `
// See [ExistingType] for details.
type MyType int`

	collection.AddChecker(&info, func(ctx *linter.CheckerContext) (linter.FileWalker, error) {
		c := &brokenDocLinkChecker{ctx: ctx}
		return astwalk.WalkerForDocLink(c), nil
	})
}

type brokenDocLinkChecker struct {
	astwalk.WalkHandler
	ctx *linter.CheckerContext

	importsByName map[string]*types.Package
	dotImports    []*types.Package
}

func (c *brokenDocLinkChecker) EnterFile(f *ast.File) bool {
	c.importsByName = make(map[string]*types.Package)
	c.dotImports = c.dotImports[:0]

	for _, spec := range f.Imports {
		var localName string
		var pkgName *types.PkgName
		if spec.Name != nil {
			localName = spec.Name.Name
			pkgName = c.ctx.TypesInfo.ObjectOf(spec.Name).(*types.PkgName)
		} else {
			pkgName = c.ctx.TypesInfo.Implicits[spec].(*types.PkgName)
			localName = pkgName.Name()
		}

		switch localName {
		case "_":
			continue
		case ".":
			c.dotImports = append(c.dotImports, pkgName.Imported())
		default:
			c.importsByName[localName] = pkgName.Imported()
		}
	}

	return true
}

func (c *brokenDocLinkChecker) VisitDocLink(decl ast.Node, doc *ast.CommentGroup) {
	text := docCommentText(doc)
	if text == "" {
		return
	}

	parser := &comment.Parser{
		LookupPackage: c.lookupPackage,
		LookupSym:     c.lookupSym,
	}
	parsed := parser.Parse(text)
	for _, link := range collectDocLinks(parsed) {
		c.validateLink(decl, linkRef(link), link)
	}
}

func (c *brokenDocLinkChecker) lookupPackage(name string) (importPath string, ok bool) {
	if pkg, found := c.importsByName[name]; found {
		return pkg.Path(), true
	}
	if path, ok := comment.DefaultLookupPackage(name); ok {
		return path, true
	}
	if isDocLinkPkgName(name) {
		return name, true
	}
	return "", false
}

func (c *brokenDocLinkChecker) lookupSym(recv, name string) bool {
	if name == "" {
		return false
	}
	return isDocLinkName(name)
}

func (c *brokenDocLinkChecker) validateLink(decl ast.Node, ref string, link *comment.DocLink) {
	switch {
	case link.ImportPath != "":
		c.validateQualified(decl, ref, link)
	case link.Recv != "":
		c.validateMember(decl, ref, c.ctx.Pkg, link.Recv, link.Name, true, "")
	case link.Name != "":
		c.validateLocalSymbol(decl, ref, link.Name)
	}
}

func (c *brokenDocLinkChecker) validateQualified(decl ast.Node, ref string, link *comment.DocLink) {
	pkgLocalName := pkgLocalNameFromRef(ref)
	if link.Name == "" && isBuiltin(pkgLocalName) {
		return
	}

	pkg, alias, imported := c.resolveImportedPackage(link.ImportPath, pkgLocalName)
	if !imported {
		c.warn(decl, ref, `package "%s" is not imported`, alias)
		return
	}

	if link.Name == "" {
		return
	}

	if pkg == nil {
		return
	}

	if link.Recv != "" {
		c.validateMember(decl, ref, pkg, link.Recv, link.Name, false, alias)
		return
	}

	if lookupPackageSymbol(pkg, link.Name) == nil {
		c.warn(decl, ref, `"%s" not found in package "%s"`, link.Name, alias)
	}
}

func (c *brokenDocLinkChecker) validateLocalSymbol(decl ast.Node, ref, name string) {
	if isBuiltin(name) {
		return
	}
	if lookupLocalSymbol(c.ctx.Pkg, c.dotImports, name) != nil {
		return
	}
	c.warn(decl, ref, `unknown symbol "%s" in current package`, name)
}

func (c *brokenDocLinkChecker) validateMember(
	decl ast.Node,
	ref string,
	pkg *types.Package,
	recv, member string,
	inCurrentPkg bool,
	pkgAlias string,
) {
	var obj types.Object
	if inCurrentPkg {
		obj = lookupLocalSymbol(c.ctx.Pkg, c.dotImports, recv)
	} else {
		obj = pkg.Scope().Lookup(recv)
	}

	if obj == nil {
		if inCurrentPkg {
			c.warn(decl, ref, `type "%s" not found in current package`, recv)
		} else {
			c.warn(decl, ref, `type "%s" not found in package "%s"`, recv, pkgAlias)
		}
		return
	}

	typeName, ok := obj.(*types.TypeName)
	if !ok {
		c.warn(decl, ref, `"%s" is not a type`, recv)
		return
	}

	if lookupTypeMember(typeName.Type(), member) != nil {
		return
	}

	c.warn(decl, ref, `type "%s" has no method or field "%s"`, recv, member)
}

func (c *brokenDocLinkChecker) resolveImportedPackage(importPath, localName string) (*types.Package, string, bool) {
	if pkg, ok := c.importsByName[localName]; ok && pkg.Path() == importPath {
		return pkg, localName, true
	}
	for name, pkg := range c.importsByName {
		if pkg.Path() == importPath {
			return pkg, name, true
		}
	}
	if isStdlibPkgPath(importPath) {
		if pkg := importedPackage(c.ctx.Pkg, importPath); pkg != nil {
			return pkg, localName, true
		}
		return nil, localName, true
	}
	return nil, localName, false
}

func isStdlibPkgPath(path string) bool {
	return goStdlib[path]
}

func importedPackage(pkg *types.Package, path string) *types.Package {
	for _, imp := range pkg.Imports() {
		if imp.Path() == path {
			return imp
		}
	}
	return nil
}

func (c *brokenDocLinkChecker) warn(decl ast.Node, ref, format string, args ...interface{}) {
	c.ctx.Warn(decl, "[%s]: "+format, append([]interface{}{ref}, args...)...)
}

func lookupLocalSymbol(pkg *types.Package, dotImports []*types.Package, name string) types.Object {
	if obj := pkg.Scope().Lookup(name); obj != nil {
		return obj
	}
	for _, imp := range dotImports {
		if obj := imp.Scope().Lookup(name); obj != nil {
			return obj
		}
	}
	return nil
}

func lookupPackageSymbol(pkg *types.Package, name string) types.Object {
	if pkg == nil {
		return nil
	}
	return pkg.Scope().Lookup(name)
}

func lookupTypeMember(typ types.Type, name string) types.Object {
	mset := types.NewMethodSet(typ)
	for i := 0; i < mset.Len(); i++ {
		if mset.At(i).Obj().Name() == name {
			return mset.At(i).Obj()
		}
	}
	if field := lookupEmbeddedField(typ, name); field != nil {
		return field
	}
	return nil
}

func lookupEmbeddedField(typ types.Type, name string) *types.Var {
	typ = derefType(typ)
	st, ok := typ.Underlying().(*types.Struct)
	if !ok {
		return nil
	}
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if f.Name() == name {
			return f
		}
		if f.Anonymous() {
			if embedded := lookupEmbeddedField(f.Type(), name); embedded != nil {
				return embedded
			}
		}
	}
	return nil
}

func derefType(typ types.Type) types.Type {
	if p, ok := typ.(*types.Pointer); ok {
		return p.Elem()
	}
	return typ
}

func docCommentText(doc *ast.CommentGroup) string {
	var b strings.Builder
	first := true
	for _, c := range doc.List {
		text := c.Text
		if strings.HasPrefix(text, "/*") {
			continue
		}
		if strings.HasPrefix(text, "//") {
			text = strings.TrimPrefix(text, "//")
			if len(text) > 0 && text[0] == ' ' {
				text = text[1:]
			}
		}
		if !first {
			b.WriteByte('\n')
		}
		first = false
		b.WriteString(text)
	}
	return b.String()
}

func collectDocLinks(doc *comment.Doc) []*comment.DocLink {
	var links []*comment.DocLink
	var walkText func([]comment.Text)
	walkText = func(texts []comment.Text) {
		for _, t := range texts {
			switch t := t.(type) {
			case *comment.DocLink:
				links = append(links, t)
			case *comment.Link:
				walkText(t.Text)
			}
		}
	}
	var walkBlock func(comment.Block)
	walkBlock = func(b comment.Block) {
		switch b := b.(type) {
		case *comment.Paragraph:
			walkText(b.Text)
		case *comment.List:
			for _, item := range b.Items {
				for _, content := range item.Content {
					walkBlock(content)
				}
			}
		case *comment.Heading:
			walkText(b.Text)
		}
	}
	for _, b := range doc.Content {
		walkBlock(b)
	}
	return links
}

func linkRef(link *comment.DocLink) string {
	var b strings.Builder
	var write func([]comment.Text)
	write = func(texts []comment.Text) {
		for _, t := range texts {
			switch t := t.(type) {
			case comment.Plain:
				b.WriteString(string(t))
			case comment.Italic:
				b.WriteString(string(t))
			case *comment.Link:
				write(t.Text)
			case *comment.DocLink:
				write(t.Text)
			}
		}
	}
	write(link.Text)
	return b.String()
}

func pkgLocalNameFromRef(ref string) string {
	if i := strings.Index(ref, "."); i >= 0 {
		return ref[:i]
	}
	return ref
}

func isDocLinkName(s string) bool {
	if s == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsUpper(r) && isDocLinkIdent(s)
}

func isDocLinkPkgName(s string) bool {
	return s != "" && isDocLinkIdent(s)
}

func isDocLinkIdent(s string) bool {
	for i, r := range s {
		if i == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
