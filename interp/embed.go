package interp

import (
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

	"embed"
)

// embedDirective is one //go:embed line and the patterns it names.
type embedDirective struct {
	pos      token.Pos
	patterns []string
}

// collectEmbeds maps each package-level var spec to the //go:embed lines that
// apply to it. Directives that do not apply to a single var produce an error.
func collectEmbeds(fset *token.FileSet, f *ast.File) (map[*ast.ValueSpec][]embedDirective, error) {
	var dirs []embedDirective
	for _, g := range f.Comments {
		for _, c := range g.List {
			text := strings.TrimPrefix(c.Text, "//")
			if text != "go:embed" && !strings.HasPrefix(text, "go:embed ") {
				continue
			}
			d, err := parseEmbedDirective(c.Pos(), text)
			if err != nil {
				return nil, errorAt(fset, c.Pos(), "%s", err.Error())
			}
			dirs = append(dirs, d)
		}
	}
	if len(dirs) == 0 {
		return nil, nil
	}

	nodes := collectNodes(f)
	importsEmbed := fileImportsEmbed(f)
	out := map[*ast.ValueSpec][]embedDirective{}
	for _, d := range dirs {
		if !commentOnOwnLine(fset, d.pos, nodes) {
			return nil, errorAt(fset, d.pos, "misplaced compiler directive")
		}
		spec, inFunc, bad := findEmbedSpec(f, d.pos)
		if bad != "" {
			return nil, errorAt(fset, d.pos, "%s", bad)
		}
		if !importsEmbed {
			return nil, errorAt(fset, d.pos, "go:embed only allowed in Go files that import \"embed\"")
		}
		if msg := embedSpecProblem(spec, inFunc); msg != "" {
			return nil, errorAt(fset, d.pos, "%s", msg)
		}
		out[spec] = append(out[spec], d)
	}
	return out, nil
}

func parseEmbedDirective(pos token.Pos, text string) (embedDirective, error) {
	args := strings.TrimPrefix(text, "go:embed")
	patterns, err := parseGoEmbed(args)
	if err != nil {
		return embedDirective{}, err
	}
	if len(patterns) == 0 {
		return embedDirective{}, fmt.Errorf("usage: //go:embed pattern...")
	}
	return embedDirective{pos: pos, patterns: patterns}, nil
}

// parseGoEmbed splits the arguments of a //go:embed directive.
// Patterns are unquoted fields or double-quoted and raw string literals.
func parseGoEmbed(args string) ([]string, error) {
	var list []string
	for args = strings.TrimSpace(args); args != ""; args = strings.TrimSpace(args) {
		var pat string
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
			pat = args[:i]
			args = args[i:]
		case '`':
			i := strings.Index(args[1:], "`")
			if i < 0 {
				return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args)
			}
			pat = args[1 : 1+i]
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
					pat = q
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
		list = append(list, pat)
	}
	return list, nil
}

func embedSpecProblem(spec *ast.ValueSpec, inFunc bool) string {
	switch {
	case len(spec.Names) != 1:
		return "go:embed cannot apply to multiple vars"
	case len(spec.Values) != 0:
		return "go:embed cannot apply to var with initializer"
	case spec.Type == nil:
		return "go:embed cannot apply to var without type"
	case inFunc:
		return "go:embed cannot apply to var inside func"
	default:
		return ""
	}
}

func fileImportsEmbed(f *ast.File) bool {
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) == "embed" {
			return true
		}
	}
	return false
}

func collectNodes(f *ast.File) []ast.Node {
	var nodes []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n != nil {
			nodes = append(nodes, n)
		}
		return true
	})
	return nodes
}

func commentOnOwnLine(fset *token.FileSet, pos token.Pos, nodes []ast.Node) bool {
	cp := fset.Position(pos)
	for _, n := range nodes {
		if n.Pos() == pos {
			continue
		}
		np := fset.Position(n.Pos())
		if np.Filename == cp.Filename && np.Line == cp.Line && np.Column < cp.Column && n.Pos() < pos {
			return false
		}
		ep := fset.Position(n.End())
		if ep.Filename == cp.Filename && ep.Line == cp.Line && ep.Column <= cp.Column && n.End() <= pos {
			return false
		}
	}
	return true
}

// findEmbedSpec reports the var spec a directive applies to.
// A directive inside a parenthesized var group applies to the next spec in
// that group. Otherwise it applies to the next declaration when that
// declaration is a single-line var. A directive placed above "var (" is misplaced.
func findEmbedSpec(f *ast.File, pos token.Pos) (spec *ast.ValueSpec, inFunc bool, bad string) {
	if gd := enclosingVarGroup(f, pos); gd != nil {
		spec = nextSpec(gd, pos)
		if spec == nil {
			return nil, false, "misplaced go:embed directive"
		}
		return spec, posInFunc(f, spec.Pos()), ""
	}
	decl := nextDecl(f, pos)
	if decl == nil {
		return nil, false, "misplaced go:embed directive"
	}
	gd, ok := decl.(*ast.GenDecl)
	if !ok {
		if ds, ok := decl.(*ast.DeclStmt); ok {
			gd, _ = ds.Decl.(*ast.GenDecl)
		}
	}
	if gd == nil || gd.Tok != token.VAR || gd.Lparen.IsValid() || len(gd.Specs) != 1 {
		return nil, false, "misplaced go:embed directive"
	}
	spec = gd.Specs[0].(*ast.ValueSpec)
	return spec, posInFunc(f, spec.Pos()), ""
}

func enclosingVarGroup(f *ast.File, pos token.Pos) *ast.GenDecl {
	var found *ast.GenDecl
	ast.Inspect(f, func(n ast.Node) bool {
		gd, ok := n.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR || !gd.Lparen.IsValid() {
			return true
		}
		if pos > gd.Lparen && pos < gd.Rparen {
			found = gd
		}
		return true
	})
	return found
}

func nextSpec(gd *ast.GenDecl, pos token.Pos) *ast.ValueSpec {
	var spec *ast.ValueSpec
	for _, s := range gd.Specs {
		vs := s.(*ast.ValueSpec)
		if vs.Pos() <= pos {
			continue
		}
		if spec == nil || vs.Pos() < spec.Pos() {
			spec = vs
		}
	}
	return spec
}

func nextDecl(f *ast.File, pos token.Pos) ast.Node {
	var best ast.Node
	var bestPos token.Pos
	consider := func(n ast.Node) {
		if n == nil || !n.Pos().IsValid() || n.Pos() <= pos {
			return
		}
		if best != nil && n.Pos() >= bestPos {
			return
		}
		best, bestPos = n, n.Pos()
	}
	for _, d := range f.Decls {
		consider(d)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if ds, ok := n.(*ast.DeclStmt); ok {
			consider(ds)
		}
		return true
	})
	return best
}

func posInFunc(f *ast.File, pos token.Pos) bool {
	inside := false
	ast.Inspect(f, func(n ast.Node) bool {
		if inside || n == nil {
			return false
		}
		switch n := n.(type) {
		case *ast.FuncDecl:
			if n.Body != nil && pos > n.Body.Lbrace && pos < n.Body.Rbrace {
				inside = true
				return false
			}
		case *ast.FuncLit:
			if n.Body != nil && pos > n.Body.Lbrace && pos < n.Body.Rbrace {
				inside = true
				return false
			}
		}
		return true
	})
	return inside
}

func errorAt(fset *token.FileSet, pos token.Pos, format string, args ...any) error {
	p := fset.Position(pos)
	posString := p.String()
	if p.Filename == DefaultSourceName {
		posString = strings.TrimPrefix(posString, DefaultSourceName+":")
	}
	return fmt.Errorf("%s: "+format, append([]any{posString}, args...)...)
}

func (interp *Interpreter) materializeEmbed(n *node) error {
	if n.typ == nil {
		return errorAt(interp.fset, n.pos, "go:embed cannot apply to var without type")
	}
	rt := n.typ.frameType()
	kind, err := embedKindOf(rt)
	if err != nil {
		return errorAt(interp.fset, n.pos, "%s", err.Error())
	}
	var patterns []string
	var first token.Pos
	for _, d := range n.embeds {
		if !first.IsValid() {
			first = d.pos
		}
		patterns = append(patterns, d.patterns...)
	}
	dir := path.Dir(interp.fset.Position(n.pos).Filename)
	files, err := resolveEmbedPatterns(interp.filesystem, dir, patterns)
	if err != nil {
		return errorAt(interp.fset, first, "%s", err.Error())
	}
	switch kind {
	case embedStringKind, embedBytesKind:
		if len(files) != 1 {
			label := rt.String()
			if n.typ.str != "" {
				label = n.typ.str
			}
			return errorAt(interp.fset, n.pos, "invalid go:embed: multiple files for type %s", label)
		}
		data, err := readEmbedFile(interp.filesystem, dir, files[0])
		if err != nil {
			return errorAt(interp.fset, first, "%s", err.Error())
		}
		if kind == embedStringKind {
			n.embedValue = reflect.ValueOf(string(data)).Convert(rt)
			return nil
		}
		n.embedValue = bytesToSlice(rt, data)
		return nil
	default:
		fsys, err := buildEmbedFS(interp.filesystem, dir, files)
		if err != nil {
			return errorAt(interp.fset, first, "%s", err.Error())
		}
		n.embedValue = reflect.ValueOf(fsys).Convert(rt)
		return nil
	}
}

const (
	embedStringKind = iota
	embedBytesKind
	embedFSKind
)

func embedKindOf(rt reflect.Type) (int, error) {
	if rt == nil {
		return 0, fmt.Errorf("go:embed cannot apply to var without type")
	}
	switch {
	case rt.Kind() == reflect.String:
		return embedStringKind, nil
	case rt.Kind() == reflect.Slice && rt.Elem().Kind() == reflect.Uint8:
		return embedBytesKind, nil
	case isEmbedFSType(rt):
		return embedFSKind, nil
	default:
		return 0, fmt.Errorf("go:embed cannot apply to var of type %s", rt.String())
	}
}

func isEmbedFSType(rt reflect.Type) bool {
	fsType := reflect.TypeOf(embed.FS{})
	if rt == fsType {
		return true
	}
	return rt.Kind() == reflect.Struct && rt.ConvertibleTo(fsType)
}

func resolveEmbedPatterns(fsys fs.FS, dir string, patterns []string) ([]string, error) {
	have := map[string]bool{}
	var files []string
	for _, pattern := range patterns {
		matched, err := matchEmbedPattern(fsys, dir, pattern)
		if err != nil {
			return nil, err
		}
		if len(matched) == 0 {
			return nil, fmt.Errorf("pattern %s: no matching files found", pattern)
		}
		for _, m := range matched {
			if !have[m] {
				have[m] = true
				files = append(files, m)
			}
		}
	}
	sort.Strings(files)
	return files, nil
}

func matchEmbedPattern(fsys fs.FS, dir, pattern string) ([]string, error) {
	glob := pattern
	all := strings.HasPrefix(pattern, "all:")
	if all {
		glob = strings.TrimPrefix(pattern, "all:")
	}
	if _, err := path.Match(glob, ""); err != nil || !validEmbedPattern(glob) {
		return nil, fmt.Errorf("pattern %s: invalid pattern syntax", pattern)
	}
	full := glob
	if dir != "" && dir != "." {
		full = path.Join(dir, glob)
	}
	matches, err := fs.Glob(fsys, full)
	if err != nil {
		return nil, fmt.Errorf("pattern %s: invalid pattern syntax", pattern)
	}
	var list []string
	seen := map[string]bool{}
	add := func(rel string) {
		if rel == "" || seen[rel] {
			return
		}
		seen[rel] = true
		list = append(list, rel)
	}
	for _, m := range matches {
		info, err := fs.Stat(fsys, m)
		if err != nil {
			return nil, err
		}
		rel := trimEmbedDir(dir, m)
		if info.IsDir() {
			count := 0
			err = fs.WalkDir(fsys, m, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if p == m {
					return nil
				}
				base := path.Base(p)
				if !all && (strings.HasPrefix(base, ".") || strings.HasPrefix(base, "_")) {
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
				count++
				add(trimEmbedDir(dir, p))
				return nil
			})
			if err != nil {
				return nil, err
			}
			if count == 0 {
				return nil, fmt.Errorf("cannot embed directory %s: contains no embeddable files", rel)
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("cannot embed irregular file %s", rel)
		}
		add(rel)
	}
	sort.Strings(list)
	return list, nil
}

func validEmbedPattern(pattern string) bool {
	return pattern != "." && fs.ValidPath(pattern)
}

func trimEmbedDir(dir, full string) string {
	if dir == "" || dir == "." {
		return full
	}
	return strings.TrimPrefix(full, dir+"/")
}

func readEmbedFile(fsys fs.FS, dir, rel string) ([]byte, error) {
	name := rel
	if dir != "" && dir != "." {
		name = path.Join(dir, rel)
	}
	return fs.ReadFile(fsys, name)
}

func bytesToSlice(rt reflect.Type, data []byte) reflect.Value {
	s := reflect.MakeSlice(rt, len(data), len(data))
	if rt.Elem() == reflect.TypeOf(byte(0)) {
		reflect.Copy(s, reflect.ValueOf(data))
		return s
	}
	for i, b := range data {
		s.Index(i).SetUint(uint64(b))
	}
	return s
}

// buildEmbedFS constructs an embed.FS whose file list matches the layout the
// compiler emits. ReadFile, ReadDir, and Open then behave as in the standard
// library, including independent copies from ReadFile and ReadDirFile directories.
func buildEmbedFS(fsys fs.FS, dir string, files []string) (embed.FS, error) {
	have := map[string]bool{}
	list := make([]string, 0, len(files))
	data := map[string]string{}
	for _, file := range files {
		b, err := readEmbedFile(fsys, dir, file)
		if err != nil {
			return embed.FS{}, err
		}
		if !have[file] {
			have[file] = true
			list = append(list, file)
		}
		data[file] = string(b)
		for d := path.Dir(file); d != "." && d != "/" && !have[d]; d = path.Dir(d) {
			have[d] = true
			list = append(list, d+"/")
		}
	}
	sort.Slice(list, func(i, j int) bool { return embedFileLess(list[i], list[j]) })

	fsType := reflect.TypeOf(embed.FS{})
	field, ok := fsType.FieldByName("files")
	if !ok {
		return embed.FS{}, fmt.Errorf("embed.FS has no files field")
	}
	sliceType := field.Type.Elem()
	slice := reflect.MakeSlice(sliceType, len(list), len(list))
	for i, name := range list {
		elem := slice.Index(i)
		setUnexported(elem.FieldByName("name"), reflect.ValueOf(name))
		if !strings.HasSuffix(name, "/") {
			setUnexported(elem.FieldByName("data"), reflect.ValueOf(data[name]))
		}
	}
	slicePtr := reflect.New(sliceType)
	slicePtr.Elem().Set(slice)
	fsVal := reflect.New(fsType).Elem()
	setUnexported(fsVal.FieldByName("files"), slicePtr)
	return fsVal.Interface().(embed.FS), nil
}

func setUnexported(field, value reflect.Value) {
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(value)
}

func embedFileLess(x, y string) bool {
	xdir, xelem, _ := embedSplit(x)
	ydir, yelem, _ := embedSplit(y)
	return xdir < ydir || xdir == ydir && xelem < yelem
}

func embedSplit(name string) (dir, elem string, isDir bool) {
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
