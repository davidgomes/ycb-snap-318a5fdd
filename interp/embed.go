package interp

import (
	"fmt"
	"go/ast"
	"go/token"
	"io/fs"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	yembed "github.com/traefik/yaegi/stdlib/embed"
)

// embedFSType is the reflect type of the interpreter's embed.FS.
var embedFSType = reflect.TypeOf(yembed.FS{})

const (
	embedUnknown = iota
	embedString
	embedBytes
	embedFiles
)

// registerEmbed exposes embed.FS to interpreted code.
func (interp *Interpreter) registerEmbed() {
	if interp.binPkg["embed"] == nil {
		interp.binPkg["embed"] = map[string]reflect.Value{}
	}
	interp.binPkg["embed"]["FS"] = reflect.ValueOf((*yembed.FS)(nil))
	if interp.pkgNames["embed"] == "" {
		interp.pkgNames["embed"] = "embed"
	}
}

// embedSlot is one variable declaration that can receive //go:embed patterns.
type embedSlot struct {
	spec      *ast.ValueSpec
	decl      *ast.GenDecl
	fileLevel bool
}

// embedSpecs collects //go:embed patterns attached to variables.
// Lead comments stay attached across blank lines, matching the compiler:
// only a non-comment token ends the directive group. A directive that is not
// on its own line, or that is not immediately before a var, is rejected.
func (interp *Interpreter) embedSpecs(f *ast.File) (map[*ast.ValueSpec][]string, error) {
	if f == nil {
		return nil, nil
	}
	importsEmbed := fileImportsEmbed(f)
	top := map[ast.Decl]bool{}
	for _, d := range f.Decls {
		top[d] = true
	}
	trailing := trailingComments(f)

	var slots []embedSlot
	ast.Inspect(f, func(n ast.Node) bool {
		d, ok := n.(*ast.GenDecl)
		if !ok || d.Tok != token.VAR {
			return true
		}
		for _, spec := range d.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			slots = append(slots, embedSlot{spec: vs, decl: d, fileLevel: top[d]})
		}
		return true
	})

	lead := map[*ast.CommentGroup]*ast.ValueSpec{}
	for _, g := range f.Comments {
		if g == nil || trailing[g] || !commentOnOwnLine(interp, f, g) {
			continue
		}
		for _, s := range slots {
			if commentLeadsSpec(f, g, s.spec, s.decl) {
				lead[g] = s.spec
				break
			}
		}
	}

	bySpec := map[*ast.ValueSpec][]*ast.CommentGroup{}
	for _, g := range f.Comments {
		if spec := lead[g]; spec != nil {
			bySpec[spec] = append(bySpec[spec], g)
		}
	}

	type posErr struct {
		pos token.Pos
		err error
	}
	var errs []posErr
	add := func(pos token.Pos, err error) {
		if err != nil {
			errs = append(errs, posErr{pos, err})
		}
	}

	out := map[*ast.ValueSpec][]string{}
	for _, s := range slots {
		var pats []string
		var pos token.Pos
		bad := false
		for _, g := range bySpec[s.spec] {
			gp, gpos, err := directivePatterns(g)
			if err != nil {
				add(gpos, interp.errorAt(gpos, "%s", err.Error()))
				bad = true
				break
			}
			if len(gp) == 0 {
				continue
			}
			if !pos.IsValid() {
				pos = gpos
			}
			pats = append(pats, gp...)
		}
		if bad || len(pats) == 0 {
			continue
		}
		if err := interp.checkEmbedVar(pos, s.spec, s.fileLevel, importsEmbed); err != nil {
			add(pos, err)
			continue
		}
		out[s.spec] = pats
	}

	for _, g := range f.Comments {
		if g == nil || lead[g] != nil {
			continue
		}
		pats, pos, err := directivePatterns(g)
		if err != nil {
			if !commentOnOwnLine(interp, f, g) || trailing[g] {
				add(pos, interp.errorAt(pos, "misplaced compiler directive"))
				continue
			}
			add(pos, interp.errorAt(pos, "%s", err.Error()))
			continue
		}
		if len(pats) == 0 {
			continue
		}
		if !commentOnOwnLine(interp, f, g) || trailing[g] {
			add(pos, interp.errorAt(pos, "misplaced compiler directive"))
			continue
		}
		add(pos, interp.errorAt(pos, "misplaced go:embed directive"))
	}

	if len(errs) == 0 {
		return out, nil
	}
	best := errs[0]
	for _, e := range errs[1:] {
		if e.pos < best.pos {
			best = e
		}
	}
	return nil, best.err
}

// commentLeadsSpec reports whether g is a lead comment of spec.
// Grouped specs only see comments inside the parentheses. Anything between
// the previous declaration and the spec counts, including across blank lines.
func commentLeadsSpec(f *ast.File, g *ast.CommentGroup, spec *ast.ValueSpec, decl *ast.GenDecl) bool {
	if g.Pos() >= spec.Pos() || g.End() > spec.Pos() {
		return false
	}
	if decl.Lparen.IsValid() && g.Pos() <= decl.Lparen {
		return false
	}
	return gapOnlyComments(f, g.End(), spec.Pos())
}

// gapOnlyComments reports whether [from, to) contains no non-comment AST nodes.
func gapOnlyComments(f *ast.File, from, to token.Pos) bool {
	if from > to {
		return false
	}
	empty := true
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil || !empty {
			return false
		}
		switch n.(type) {
		case *ast.File, *ast.CommentGroup, *ast.Comment:
			return true
		}
		if n.Pos() >= from && n.End() <= to {
			empty = false
			return false
		}
		return true
	})
	return empty
}

// commentOnOwnLine reports whether the comment group starts on a line that has
// no source tokens before it. //go:embed must occupy the line by itself.
func commentOnOwnLine(interp *Interpreter, f *ast.File, g *ast.CommentGroup) bool {
	if g == nil || len(g.List) == 0 || interp == nil || interp.fset == nil {
		return false
	}
	c := g.List[0]
	if !strings.HasPrefix(c.Text, "//") {
		return false
	}
	pos := interp.fset.Position(c.Pos())
	own := true
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil || !own {
			return false
		}
		switch n.(type) {
		case *ast.File, *ast.CommentGroup, *ast.Comment:
			return true
		}
		np := interp.fset.Position(n.Pos())
		if np.Filename == pos.Filename && np.Line == pos.Line && np.Column < pos.Column {
			own = false
			return false
		}
		return true
	})
	return own
}

// checkEmbedVar matches the compiler's restrictions on where //go:embed may appear.
func (interp *Interpreter) checkEmbedVar(commentPos token.Pos, vs *ast.ValueSpec, fileLevel, importsEmbed bool) error {
	switch {
	case !importsEmbed:
		return interp.errorAt(commentPos, "go:embed only allowed in Go files that import \"embed\"")
	case len(vs.Names) != 1:
		return interp.errorAt(commentPos, "go:embed cannot apply to multiple vars")
	case vs.Values != nil:
		return interp.errorAt(commentPos, "go:embed cannot apply to var with initializer")
	case vs.Type == nil:
		return interp.errorAt(commentPos, "go:embed cannot apply to var without type")
	case !fileLevel:
		return interp.errorAt(commentPos, "go:embed cannot apply to var inside func")
	default:
		return nil
	}
}

func (interp *Interpreter) errorAt(pos token.Pos, format string, args ...interface{}) error {
	n := &node{interp: interp, pos: pos}
	return n.cfgErrorf(format, args...)
}

func fileImportsEmbed(f *ast.File) bool {
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.IMPORT {
			continue
		}
		for _, spec := range gd.Specs {
			im, ok := spec.(*ast.ImportSpec)
			if !ok || im.Path == nil {
				continue
			}
			p, err := strconv.Unquote(im.Path.Value)
			if err == nil && p == "embed" {
				return true
			}
		}
	}
	return false
}

func trailingComments(f *ast.File) map[*ast.CommentGroup]bool {
	m := map[*ast.CommentGroup]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Field:
			if x.Comment != nil {
				m[x.Comment] = true
			}
		case *ast.ImportSpec:
			if x.Comment != nil {
				m[x.Comment] = true
			}
		case *ast.TypeSpec:
			if x.Comment != nil {
				m[x.Comment] = true
			}
		case *ast.ValueSpec:
			if x.Comment != nil {
				m[x.Comment] = true
			}
		}
		return true
	})
	return m
}

// directivePatterns returns //go:embed patterns from a lead comment group.
// The returned position is the first directive in the group.
func directivePatterns(g *ast.CommentGroup) ([]string, token.Pos, error) {
	if g == nil {
		return nil, token.NoPos, nil
	}
	var out []string
	var pos token.Pos
	for _, c := range g.List {
		body, ok := embedBody(c.Text)
		if !ok {
			continue
		}
		if !pos.IsValid() {
			pos = c.Pos()
		}
		pats, err := parseGoEmbed(body)
		if err != nil {
			return nil, c.Pos(), err
		}
		if len(pats) == 0 {
			return nil, c.Pos(), fmt.Errorf("usage: //go:embed pattern...")
		}
		out = append(out, pats...)
	}
	return out, pos, nil
}

// embedBody returns the text after "//go:embed" when c is a directive line.
func embedBody(text string) (string, bool) {
	if !strings.HasPrefix(text, "//") {
		return "", false
	}
	body := text[2:]
	const prefix = "go:embed"
	if body == prefix {
		return "", true
	}
	if !strings.HasPrefix(body, prefix) {
		return "", false
	}
	rest := body[len(prefix):]
	r, _ := utf8.DecodeRuneInString(rest)
	if !unicode.IsSpace(r) {
		return "", false
	}
	return rest, true
}

// parseGoEmbed parses the text following "//go:embed". Patterns may be bare
// words or Go double-quoted or raw string literals, so names can contain spaces.
func parseGoEmbed(args string) ([]string, error) {
	var list []string
	for args = strings.TrimSpace(args); args != ""; args = strings.TrimSpace(args) {
		var pattern string
	Switch:
		switch args[0] {
		default:
			i := len(args)
			for j, c := range args {
				if unicode.IsSpace(c) {
					i = j
					break
				}
			}
			pattern = args[:i]
			args = args[i:]

		case '`':
			i := strings.Index(args[1:], "`")
			if i < 0 {
				return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args)
			}
			pattern = args[1 : 1+i]
			args = args[1+i+1:]

		case '"':
			i := 1
			for ; i < len(args); i++ {
				if args[i] == '\\' {
					i++
					continue
				}
				if args[i] == '"' {
					q, err := strconv.Unquote(args[:i+1])
					if err != nil {
						return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args[:i+1])
					}
					pattern = q
					args = args[i+1:]
					break Switch
				}
			}
			if i >= len(args) {
				return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args)
			}
		}

		if args != "" {
			r, _ := utf8.DecodeRuneInString(args)
			if !unicode.IsSpace(r) {
				return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args)
			}
		}
		list = append(list, pattern)
	}
	return list, nil
}

// resolveEmbeds fills embedVal on every variable that carries //go:embed patterns.
func (interp *Interpreter) resolveEmbeds(root *node) error {
	if root == nil {
		return nil
	}
	var err error
	root.Walk(func(n *node) bool {
		if err != nil {
			return false
		}
		if len(n.embedPatterns) == 0 {
			return true
		}
		err = interp.resolveOneEmbed(n)
		return err == nil
	}, nil)
	return err
}

func (interp *Interpreter) resolveOneEmbed(n *node) error {
	kind := embedKindOf(n.typ)
	if kind == embedUnknown {
		return n.cfgErrorf("go:embed cannot apply to var of type %s", embedTypeLabel(n))
	}

	dir := embedDir(interp, n)
	files, err := resolveEmbedList(interp.filesystem, dir, n.embedPatterns)
	if err != nil {
		return n.cfgErrorf("%s", err.Error())
	}
	if kind != embedFiles && len(files) != 1 {
		return n.cfgErrorf("invalid go:embed: multiple files for type %s", embedTypeLabel(n))
	}

	switch kind {
	case embedString:
		b, err := fs.ReadFile(interp.filesystem, sourcePath(dir, files[0]))
		if err != nil {
			return n.cfgErrorf("embed %s: %v", files[0], err)
		}
		n.embedVal = reflect.ValueOf(string(b))
	case embedBytes:
		b, err := fs.ReadFile(interp.filesystem, sourcePath(dir, files[0]))
		if err != nil {
			return n.cfgErrorf("embed %s: %v", files[0], err)
		}
		cp := make([]byte, len(b))
		copy(cp, b)
		n.embedVal = reflect.ValueOf(cp)
	case embedFiles:
		contents := make(map[string]string, len(files))
		for _, rel := range files {
			b, err := fs.ReadFile(interp.filesystem, sourcePath(dir, rel))
			if err != nil {
				return n.cfgErrorf("embed %s: %v", rel, err)
			}
			contents[rel] = string(b)
		}
		n.embedVal = reflect.ValueOf(yembed.NewFS(contents))
	}
	return nil
}

// embedTypeLabel is the type name used in go:embed errors. It follows the
// spelling in source, so []byte stays []byte rather than []uint8.
func embedTypeLabel(n *node) string {
	if n != nil && len(n.child) > 0 {
		if label := typeExprLabel(n.lastChild()); label != "" {
			return label
		}
	}
	if n != nil && n.typ != nil {
		if n.typ.name != "" {
			return n.typ.name
		}
		if n.typ.str != "" {
			return n.typ.str
		}
	}
	return "<nil>"
}

func typeExprLabel(n *node) string {
	if n == nil {
		return ""
	}
	switch n.kind {
	case identExpr:
		return n.ident
	case selectorExpr:
		if len(n.child) == 2 {
			left, right := typeExprLabel(n.child[0]), typeExprLabel(n.child[1])
			if left != "" && right != "" {
				return left + "." + right
			}
		}
	case arrayType:
		if len(n.child) == 1 {
			if elem := typeExprLabel(n.child[0]); elem != "" {
				return "[]" + elem
			}
		}
	case starExpr:
		if len(n.child) == 1 {
			if elem := typeExprLabel(n.child[0]); elem != "" {
				return "*" + elem
			}
		}
	case parenExpr:
		if len(n.child) == 1 {
			return typeExprLabel(n.child[0])
		}
	}
	return ""
}

func embedKindOf(t *itype) int {
	if t == nil {
		return embedUnknown
	}
	// Aliases of embed.FS keep the original value type. A defined type that
	// wraps embed.FS is a linkedT and is not a valid target.
	if t.cat == valueT && t.rtype == embedFSType {
		return embedFiles
	}
	if isEmbedString(t) {
		return embedString
	}
	if isEmbedByteSlice(t) {
		return embedBytes
	}
	return embedUnknown
}

func isEmbedString(t *itype) bool {
	t = underlying(t)
	return t != nil && (t.cat == stringT || (t.cat == valueT && t.rtype != nil && t.rtype.Kind() == reflect.String))
}

func isEmbedByteSlice(t *itype) bool {
	t = underlying(t)
	if t == nil {
		return false
	}
	switch t.cat {
	case sliceT, variadicT:
		elem := underlying(t.val)
		return isUint8Type(elem)
	case valueT:
		return t.rtype != nil && t.rtype.Kind() == reflect.Slice && t.rtype.Elem().Kind() == reflect.Uint8
	default:
		return false
	}
}

func isUint8Type(t *itype) bool {
	t = underlying(t)
	return t != nil && (t.cat == uint8T || (t.cat == valueT && t.rtype != nil && t.rtype.Kind() == reflect.Uint8))
}

func underlying(t *itype) *itype {
	for t != nil && t.cat == linkedT {
		t = t.val
	}
	return t
}

func embedDir(interp *Interpreter, n *node) string {
	filename := filepath.ToSlash(interp.fset.Position(n.pos).Filename)
	if filename == "" || filename == DefaultSourceName {
		return "."
	}
	dir := path.Dir(filename)
	if dir == "" || dir == "." {
		return "."
	}
	return dir
}

func sourcePath(dir, rel string) string {
	if dir == "" || dir == "." {
		return rel
	}
	return path.Join(dir, rel)
}

func relToDir(dir, p string) string {
	if dir == "" || dir == "." {
		return p
	}
	prefix := dir + "/"
	if strings.HasPrefix(p, prefix) {
		return p[len(prefix):]
	}
	if p == dir {
		return "."
	}
	return p
}

func validEmbedPattern(pattern string) bool {
	return pattern != "." && fs.ValidPath(pattern)
}

// resolveEmbedList resolves patterns against fsys relative to dir.
// Each pattern must match at least one file. Results are unique and sorted.
func resolveEmbedList(fsys fs.FS, dir string, patterns []string) ([]string, error) {
	have := map[string]struct{}{}
	var files []string
	for _, pattern := range patterns {
		list, err := matchEmbedPattern(fsys, dir, pattern)
		if err != nil {
			return nil, fmt.Errorf("pattern %s: %v", pattern, err)
		}
		for _, name := range list {
			if _, ok := have[name]; ok {
				continue
			}
			have[name] = struct{}{}
			files = append(files, name)
		}
	}
	sort.Strings(files)
	return files, nil
}

func matchEmbedPattern(fsys fs.FS, dir, pattern string) ([]string, error) {
	all := strings.HasPrefix(pattern, "all:")
	glob := pattern
	if all {
		glob = pattern[len("all:"):]
	}
	if _, err := path.Match(glob, ""); err != nil || !validEmbedPattern(glob) {
		return nil, fmt.Errorf("invalid pattern syntax")
	}

	full := glob
	if dir != "" && dir != "." {
		full = path.Join(dir, glob)
	}
	matches, err := fs.Glob(fsys, full)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern syntax")
	}

	var list []string
	seen := map[string]bool{}
	for _, match := range matches {
		rels, err := embedMatchFiles(fsys, dir, match, all)
		if err != nil {
			return nil, err
		}
		for _, rel := range rels {
			if seen[rel] {
				continue
			}
			seen[rel] = true
			list = append(list, rel)
		}
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("no matching files found")
	}
	sort.Strings(list)
	return list, nil
}

// embedMatchFiles returns embeddable files for one glob hit.
// A directory contributes its tree. Names starting with '.' or '_' are skipped
// while walking a directory unless all is set. A file matched directly is kept
// even when its name starts with '.' or '_'.
func embedMatchFiles(fsys fs.FS, dir, full string, all bool) ([]string, error) {
	info, err := fs.Stat(fsys, full)
	if err != nil {
		return nil, err
	}
	rel := relToDir(dir, full)
	// Prefer the directory entry so symlinks are not followed. A direct match of
	// a symlink or other non-regular file is an error, matching the go command.
	mode := info.Mode()
	if typ, ok := embedEntryType(fsys, full); ok {
		if typ&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("cannot embed irregular file %s", rel)
		}
		if typ&fs.ModeDir != 0 {
			mode = typ | 0555
		} else if typ.IsRegular() || typ == 0 {
			mode = typ
		} else {
			return nil, fmt.Errorf("cannot embed irregular file %s", rel)
		}
	}
	isDir := mode.IsDir() || info.IsDir()
	if err := checkEmbedRel(rel, isDir); err != nil {
		return nil, err
	}
	if !isDir {
		if !mode.IsRegular() && mode != 0 {
			return nil, fmt.Errorf("cannot embed irregular file %s", rel)
		}
		return []string{rel}, nil
	}

	var list []string
	err = fs.WalkDir(fsys, full, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		// Bad names and, unless all: is set, names starting with '.' or '_' are
		// left out of a directory tree. The matched directory itself is kept.
		if p != full && skipEmbedEntry(name, all) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		list = append(list, relToDir(dir, p))
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("cannot embed directory %s: contains no embeddable files", rel)
	}
	return list, nil
}

// embedEntryType reports the type bits of full from its parent directory listing.
// Directory listings do not follow symlinks, unlike fs.Stat.
func embedEntryType(fsys fs.FS, full string) (fs.FileMode, bool) {
	parent, name := path.Split(full)
	parent = strings.TrimSuffix(parent, "/")
	if parent == "" {
		parent = "."
	}
	entries, err := fs.ReadDir(fsys, parent)
	if err != nil {
		return 0, false
	}
	for _, e := range entries {
		if e.Name() == name {
			return e.Type(), true
		}
	}
	return 0, false
}

// checkEmbedRel rejects names the go command refuses to embed.
// The deepest bad element wins, so a bad file name is reported ahead of a bad parent.
func checkEmbedRel(rel string, isDir bool) error {
	if rel == "" || rel == "." {
		return nil
	}
	what := "file"
	if isDir {
		what = "directory"
	}
	parts := strings.Split(rel, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if !isBadEmbedName(parts[i]) {
			continue
		}
		if i == len(parts)-1 {
			return fmt.Errorf("cannot embed %s %s: invalid name %s", what, rel, parts[i])
		}
		return fmt.Errorf("cannot embed %s %s: in invalid directory %s", what, rel, parts[i])
	}
	return nil
}

// skipEmbedEntry reports whether a directory walk should ignore name.
func skipEmbedEntry(name string, all bool) bool {
	if name == "" || isBadEmbedName(name) {
		return true
	}
	if !all && (name[0] == '.' || name[0] == '_') {
		return true
	}
	return false
}

// isBadEmbedName reports whether name cannot appear in a module and therefore
// is not embeddable. It follows the go command: module file-name rules, plus
// version-control directories.
func isBadEmbedName(name string) bool {
	switch name {
	case "", ".", "..", ".bzr", ".hg", ".git", ".svn":
		return true
	}
	if !utf8.ValidString(name) || strings.Count(name, ".") == len(name) || name[len(name)-1] == '.' {
		return true
	}
	for _, r := range name {
		if !embedFileNameOK(r) {
			return true
		}
	}
	short := name
	if i := strings.IndexByte(short, '.'); i >= 0 {
		short = short[:i]
	}
	switch strings.ToUpper(short) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	}
	return false
}

// embedFileNameOK matches the characters module.CheckFilePath allows in one element.
func embedFileNameOK(r rune) bool {
	if r < utf8.RuneSelf {
		const allowed = "!#$%&()+,-.=@[]^_{}~ "
		if '0' <= r && r <= '9' || 'A' <= r && r <= 'Z' || 'a' <= r && r <= 'z' {
			return true
		}
		return strings.ContainsRune(allowed, r)
	}
	return unicode.IsLetter(r)
}
