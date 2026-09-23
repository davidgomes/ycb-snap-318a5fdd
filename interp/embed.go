package interp

import (
	"embed"
	"fmt"
	"go/ast"
	"go/token"
	"io/fs"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
	"unsafe"
)

// embedFile matches the unexported file record stored by embed.FS.
// Field order and types must stay aligned with that record so an embed.FS
// value built here is readable by the standard library methods.
type embedFile struct {
	name string
	data string
	hash [16]byte
}

const (
	embedUnknown = iota
	embedString
	embedBytes
	embedFiles
)

type embedComment struct {
	pos      token.Pos
	patterns []string
}

type posRange struct {
	lo, hi token.Pos
}

var embedFSType = reflect.TypeOf(embed.FS{})

// embedSpecs collects //go:embed patterns for package-level var specs.
// Patterns on a declaration apply to the following variable. Several
// directive lines before one variable are combined.
func (interp *Interpreter) embedSpecs(f *ast.File) (map[*ast.ValueSpec][]string, error) {
	comments, err := interp.parseEmbedComments(f)
	if err != nil || len(comments) == 0 {
		return nil, err
	}

	imported := fileImportsEmbed(f)
	inFunc, err := interp.embedInFunc(comments, f, imported)
	if err != nil {
		return nil, err
	}

	used := make([]bool, len(comments))
	out := map[*ast.ValueSpec][]string{}
	prev := f.Name.End()
	for _, d := range f.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.VAR {
			if err := interp.assignDeclEmbeds(gd, prev, comments, used, inFunc, imported, out); err != nil {
				return nil, err
			}
		}
		if d.End() > prev {
			prev = d.End()
		}
	}
	for i, c := range comments {
		if !used[i] {
			return nil, interp.posErr(c.pos, "misplaced go:embed directive")
		}
	}
	return out, nil
}

func (interp *Interpreter) parseEmbedComments(f *ast.File) ([]embedComment, error) {
	var out []embedComment
	for _, g := range f.Comments {
		for _, c := range g.List {
			text := c.Text
			if !strings.HasPrefix(text, "//go:embed") {
				continue
			}
			rest := text[len("//go:embed"):]
			if rest != "" {
				r, _ := utf8.DecodeRuneInString(rest)
				if !unicode.IsSpace(r) {
					continue
				}
			}
			pats, err := parseGoEmbed(rest)
			if err != nil {
				return nil, interp.posErr(c.Pos(), "%v", err)
			}
			if len(pats) == 0 {
				return nil, interp.posErr(c.Pos(), "usage: //go:embed pattern...")
			}
			out = append(out, embedComment{pos: c.Pos(), patterns: pats})
		}
	}
	return out, nil
}

func fileImportsEmbed(f *ast.File) bool {
	for _, imp := range f.Imports {
		if imp.Path != nil && imp.Path.Value == `"embed"` {
			return true
		}
	}
	return false
}

func (interp *Interpreter) embedInFunc(comments []embedComment, f *ast.File, imported bool) ([]posRange, error) {
	var ranges []posRange
	ast.Inspect(f, func(n ast.Node) bool {
		var body *ast.BlockStmt
		switch n := n.(type) {
		case *ast.FuncDecl:
			body = n.Body
		case *ast.FuncLit:
			body = n.Body
		}
		if body != nil {
			ranges = append(ranges, posRange{body.Lbrace, body.Rbrace})
		}
		return true
	})
	for _, c := range comments {
		for _, r := range ranges {
			if c.pos > r.lo && c.pos < r.hi {
				if !imported {
					return nil, interp.posErr(c.pos, "go:embed only allowed in Go files that import \"embed\"")
				}
				return nil, interp.posErr(c.pos, "go:embed cannot apply to var inside func")
			}
		}
	}
	return ranges, nil
}

func (interp *Interpreter) assignDeclEmbeds(gd *ast.GenDecl, prev token.Pos, comments []embedComment, used []bool, inFunc []posRange, imported bool, out map[*ast.ValueSpec][]string) error {
	var specs []*ast.ValueSpec
	for _, s := range gd.Specs {
		vs, ok := s.(*ast.ValueSpec)
		if ok {
			specs = append(specs, vs)
		}
	}
	if len(specs) == 0 {
		return nil
	}

	end := specs[0].Pos()
	if gd.Lparen.IsValid() {
		end = gd.Lparen
	}
	groupPats, err := interp.takeEmbed(comments, used, inFunc, prev, end)
	if err != nil {
		return err
	}
	if len(groupPats) > 0 {
		if !imported {
			return interp.posErr(gd.Pos(), "go:embed only allowed in Go files that import \"embed\"")
		}
		if len(specs) != 1 || len(specs[0].Names) != 1 {
			return interp.posErr(gd.Pos(), "go:embed cannot apply to multiple vars")
		}
		if err := addSpecEmbed(specs[0], groupPats, out); err != nil {
			return interp.posErr(specs[0].Pos(), "%v", err)
		}
	}

	if !gd.Lparen.IsValid() {
		return nil
	}
	start := gd.Lparen
	for _, vs := range specs {
		pats, err := interp.takeEmbed(comments, used, inFunc, start, vs.Pos())
		if err != nil {
			return err
		}
		if len(pats) > 0 {
			if !imported {
				return interp.posErr(vs.Pos(), "go:embed only allowed in Go files that import \"embed\"")
			}
			if len(vs.Names) != 1 {
				return interp.posErr(vs.Pos(), "go:embed cannot apply to multiple vars")
			}
			if err := addSpecEmbed(vs, pats, out); err != nil {
				return interp.posErr(vs.Pos(), "%v", err)
			}
		}
		start = vs.End()
	}
	return nil
}

func addSpecEmbed(vs *ast.ValueSpec, pats []string, out map[*ast.ValueSpec][]string) error {
	if len(vs.Names) != 1 {
		return fmt.Errorf("go:embed cannot apply to multiple vars")
	}
	if vs.Values != nil {
		return fmt.Errorf("go:embed cannot apply to var with initializer")
	}
	if vs.Type == nil {
		return fmt.Errorf("go:embed cannot apply to var without type")
	}
	out[vs] = append(out[vs], pats...)
	return nil
}

// takeEmbed returns directive patterns that sit on their own lines after
// start and before end. A comment sharing a line with other code is left
// unused so it is reported as misplaced.
func (interp *Interpreter) takeEmbed(comments []embedComment, used []bool, inFunc []posRange, start, end token.Pos) ([]string, error) {
	if !start.IsValid() || !end.IsValid() || start >= end {
		return nil, nil
	}
	startPos := interp.fset.Position(start)
	endPos := interp.fset.Position(end)
	var pats []string
	for i, c := range comments {
		if used[i] || c.pos <= start || c.pos >= end {
			continue
		}
		if inRange(c.pos, inFunc) {
			continue
		}
		cp := interp.fset.Position(c.pos)
		if cp.Filename != endPos.Filename || cp.Line <= startPos.Line || cp.Line >= endPos.Line {
			continue
		}
		used[i] = true
		pats = append(pats, c.patterns...)
	}
	return pats, nil
}

func inRange(pos token.Pos, ranges []posRange) bool {
	for _, r := range ranges {
		if pos > r.lo && pos < r.hi {
			return true
		}
	}
	return false
}

func (interp *Interpreter) posErr(pos token.Pos, format string, args ...interface{}) error {
	return fmt.Errorf("%s: "+format, append([]interface{}{interp.fset.Position(pos)}, args...)...)
}

func parseGoEmbed(args string) ([]string, error) {
	var list []string
	args = strings.TrimSpace(args)
	for args != "" {
		pattern, rest, err := nextEmbedPattern(args)
		if err != nil {
			return nil, err
		}
		list = append(list, pattern)
		args = strings.TrimSpace(rest)
	}
	return list, nil
}

func nextEmbedPattern(args string) (pattern, rest string, err error) {
	switch args[0] {
	case '`':
		i := strings.Index(args[1:], "`")
		if i < 0 {
			return "", "", fmt.Errorf("invalid quoted string in //go:embed: %s", args)
		}
		pattern, rest = args[1:1+i], args[1+i+1:]
	case '"':
		found := false
		i := 1
		for ; i < len(args); i++ {
			if args[i] == '\\' {
				i++
				continue
			}
			if args[i] == '"' {
				pattern, err = strconv.Unquote(args[:i+1])
				if err != nil {
					return "", "", fmt.Errorf("invalid quoted string in //go:embed: %s", args[:i+1])
				}
				rest = args[i+1:]
				found = true
				break
			}
		}
		if !found {
			return "", "", fmt.Errorf("invalid quoted string in //go:embed: %s", args)
		}
	default:
		i := 0
		for i < len(args) {
			r, size := utf8.DecodeRuneInString(args[i:])
			if unicode.IsSpace(r) {
				break
			}
			i += size
		}
		pattern, rest = args[:i], args[i:]
	}
	if rest != "" {
		r, _ := utf8.DecodeRuneInString(rest)
		if !unicode.IsSpace(r) {
			return "", "", fmt.Errorf("invalid quoted string in //go:embed: %s", args)
		}
	}
	return pattern, rest, nil
}

func (interp *Interpreter) embedVar(n *node) error {
	if len(n.child) != 2 {
		return n.cfgErrorf("go:embed cannot apply to multiple vars")
	}
	kind := embedKindOf(n.typ)
	if kind == embedUnknown {
		return n.cfgErrorf("go:embed cannot apply to var of type %s", embedTypeString(n.typ))
	}

	dir := path.Dir(interp.fset.Position(n.pos).Filename)
	fsys, err := packageFS(interp.opt.filesystem, dir)
	if err != nil {
		return n.cfgErrorf("go:embed: %v", err)
	}
	files, err := resolveEmbedPatterns(fsys, n.embedPatterns)
	if err != nil {
		return n.cfgErrorf("%v", err)
	}
	if (kind == embedString || kind == embedBytes) && len(files) != 1 {
		return n.cfgErrorf("invalid go:embed: multiple files for type %s", embedTypeString(n.typ))
	}
	val, err := embedValue(fsys, files, kind)
	if err != nil {
		return n.cfgErrorf("%v", err)
	}
	n.embedValue = val
	return nil
}

func embedKindOf(t *itype) int {
	if t == nil {
		return embedUnknown
	}
	if t.cat == valueT && t.rtype == embedFSType {
		return embedFiles
	}
	base := underlying(t)
	if base == nil {
		return embedUnknown
	}
	switch base.cat {
	case stringT:
		return embedString
	case valueT:
		if base.rtype != nil && base.rtype.Kind() == reflect.String {
			return embedString
		}
	case sliceT:
		if isByteType(base.val) {
			return embedBytes
		}
	}
	return embedUnknown
}

func underlying(t *itype) *itype {
	seen := map[*itype]bool{}
	for t != nil && t.cat == linkedT && !seen[t] {
		seen[t] = true
		t = t.val
	}
	return t
}

func isByteType(t *itype) bool {
	t = underlying(t)
	if t == nil {
		return false
	}
	if t.cat == uint8T {
		return true
	}
	return t.cat == valueT && t.rtype != nil && t.rtype.Kind() == reflect.Uint8
}

func embedTypeString(t *itype) string {
	if t == nil {
		return "<nil>"
	}
	if t.str != "" {
		return t.str
	}
	return t.cat.String()
}

// packageFS resolves embed patterns relative to the source file directory.
type relFS struct {
	base fs.FS
	dir  string
}

func packageFS(base fs.FS, dir string) (fs.FS, error) {
	if base == nil {
		base = realFS{}
	}
	if dir == "" || dir == "." {
		return base, nil
	}
	return relFS{base: base, dir: dir}, nil
}

func (r relFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	full := r.dir
	if name != "." {
		full = path.Join(r.dir, name)
	}
	return r.base.Open(full)
}

func resolveEmbedPatterns(fsys fs.FS, patterns []string) ([]string, error) {
	seen := map[string]struct{}{}
	var files []string
	for _, pattern := range patterns {
		glob := pattern
		all := strings.HasPrefix(pattern, "all:")
		if all {
			glob = pattern[len("all:"):]
		}
		if _, err := path.Match(glob, ""); err != nil || !validEmbedPattern(glob) {
			return nil, fmt.Errorf("pattern %s: invalid pattern syntax", pattern)
		}
		matches, err := fs.Glob(fsys, glob)
		if err != nil {
			return nil, fmt.Errorf("pattern %s: %v", pattern, err)
		}
		var matched []string
		for _, m := range matches {
			if err := checkEmbedPath(m); err != nil {
				return nil, fmt.Errorf("pattern %s: %v", pattern, err)
			}
			info, err := fs.Stat(fsys, m)
			if err != nil {
				return nil, fmt.Errorf("pattern %s: %v", pattern, err)
			}
			if info.IsDir() {
				nested, err := embedWalk(fsys, m, all)
				if err != nil {
					return nil, fmt.Errorf("pattern %s: %v", pattern, err)
				}
				if len(nested) == 0 {
					return nil, fmt.Errorf("pattern %s: cannot embed directory %s: contains no embeddable files", pattern, m)
				}
				matched = append(matched, nested...)
				continue
			}
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("pattern %s: cannot embed irregular file %s", pattern, m)
			}
			matched = append(matched, m)
		}
		if len(matched) == 0 {
			return nil, fmt.Errorf("pattern %s: no matching files found", pattern)
		}
		for _, m := range matched {
			if _, ok := seen[m]; ok {
				continue
			}
			seen[m] = struct{}{}
			files = append(files, m)
		}
	}
	sort.Strings(files)
	return files, nil
}

func validEmbedPattern(pattern string) bool {
	return pattern != "." && fs.ValidPath(pattern)
}

func checkEmbedPath(name string) error {
	for _, elem := range strings.Split(name, "/") {
		if isBadEmbedName(elem) {
			return fmt.Errorf("cannot embed file %s: invalid name %s", name, elem)
		}
	}
	return nil
}

func isBadEmbedName(name string) bool {
	switch name {
	case "", ".bzr", ".hg", ".git", ".svn":
		return true
	default:
		return false
	}
}

func embedWalk(fsys fs.FS, dir string, all bool) ([]string, error) {
	var list []string
	err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != dir && (isBadEmbedName(d.Name()) || embedNameExcluded(d.Name(), all)) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if p == dir {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		list = append(list, p)
		return nil
	})
	return list, err
}

func embedNameExcluded(name string, all bool) bool {
	if name == "" || name == "." || name == ".." {
		return true
	}
	if all {
		return false
	}
	return name[0] == '.' || name[0] == '_'
}

func embedValue(fsys fs.FS, files []string, kind int) (reflect.Value, error) {
	switch kind {
	case embedString, embedBytes:
		data, err := fs.ReadFile(fsys, files[0])
		if err != nil {
			return reflect.Value{}, err
		}
		if kind == embedString {
			return reflect.ValueOf(string(data)), nil
		}
		buf := make([]byte, len(data))
		copy(buf, data)
		return reflect.ValueOf(buf), nil
	case embedFiles:
		fsysVal, err := buildEmbedFS(fsys, files)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(fsysVal), nil
	default:
		return reflect.Value{}, fmt.Errorf("unsupported go:embed type")
	}
}

func buildEmbedFS(fsys fs.FS, files []string) (embed.FS, error) {
	have := make(map[string]struct{}, len(files))
	names := make([]string, 0, len(files))
	for _, file := range files {
		if _, ok := have[file]; ok {
			continue
		}
		have[file] = struct{}{}
		names = append(names, file)
		for dir := path.Dir(file); dir != "." && dir != "/"; dir = path.Dir(dir) {
			dirName := dir + "/"
			if _, ok := have[dirName]; ok {
				break
			}
			have[dirName] = struct{}{}
			names = append(names, dirName)
		}
	}
	sort.Slice(names, func(i, j int) bool { return embedFileLess(names[i], names[j]) })

	entries := make([]embedFile, len(names))
	for i, name := range names {
		entries[i].name = name
		if strings.HasSuffix(name, "/") {
			continue
		}
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return embed.FS{}, err
		}
		entries[i].data = string(b)
	}
	return newEmbedFS(entries), nil
}

// embedFileLess reports whether x sorts before y, using the same directory
// ordering as the standard embed package.
func embedFileLess(x, y string) bool {
	xdir, xelem, _ := embedFileNameSplit(x)
	ydir, yelem, _ := embedFileNameSplit(y)
	return xdir < ydir || xdir == ydir && xelem < yelem
}

func embedFileNameSplit(name string) (dir, elem string, isDir bool) {
	if name[len(name)-1] == '/' {
		isDir = true
		name = name[:len(name)-1]
	}
	i := len(name) - 1
	for i >= 0 && name[i] != '/' {
		i--
	}
	if i < 0 {
		return ".", name, isDir
	}
	return name[:i], name[i+1:], isDir
}

func newEmbedFS(files []embedFile) embed.FS {
	// embed.FS holds an unexported pointer to its file list. The concrete
	// record layout matches embedFile, so the standard methods can read it.
	list := files
	holder := &list
	return reflect.NewAt(embedFSType, unsafe.Pointer(&holder)).Elem().Interface().(embed.FS)
}

// applyEmbeds writes embedded values into the global frame before execution.
func (interp *Interpreter) applyEmbeds(roots ...*node) {
	for _, root := range roots {
		if root == nil {
			continue
		}
		root.Walk(func(n *node) bool {
			if n.kind != valueSpec || !n.embedValue.IsValid() || len(n.child) == 0 {
				return true
			}
			interp.installEmbed(n.child[0].findex, n.embedValue)
			return true
		}, nil)
	}
}

func (interp *Interpreter) installEmbed(index int, v reflect.Value) {
	if index < 0 || !v.IsValid() {
		return
	}
	if index >= len(interp.frame.data) {
		next := make([]reflect.Value, index+1)
		copy(next, interp.frame.data)
		interp.frame.data = next
	}
	dst := interp.frame.data[index]
	if dst.IsValid() && dst.CanSet() && dst.Type() == v.Type() {
		dst.Set(v)
		return
	}
	slot := reflect.New(v.Type()).Elem()
	slot.Set(v)
	interp.frame.data[index] = slot
}
