package interp

import (
	"embed"
	"fmt"
	"go/ast"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unsafe"
)

const goEmbedPrefix = "//go:embed"

func registerEmbedPackage(interp *Interpreter) {
	if interp.binPkg["embed"] == nil {
		interp.binPkg["embed"] = map[string]reflect.Value{}
		interp.pkgNames["embed"] = "embed"
	}
	interp.binPkg["embed"]["FS"] = reflect.ValueOf((*embed.FS)(nil))
}

// embedPatternsFromSpec collects //go:embed patterns attached to a var spec.
// Standalone `var x T` uses the GenDecl doc comments; grouped `var ( ... )`
// uses the ValueSpec doc comments. Multiple directive lines are combined.
func embedPatternsFromSpec(spec *ast.ValueSpec, parent ast.Node) []string {
	var pats []string
	if gd, ok := parent.(*ast.GenDecl); ok && gd.Tok == token.VAR && gd.Lparen == token.NoPos {
		pats = append(pats, parseCommentEmbeds(gd.Doc)...)
	}
	pats = append(pats, parseCommentEmbeds(spec.Doc)...)
	return pats
}

func parseCommentEmbeds(g *ast.CommentGroup) []string {
	if g == nil {
		return nil
	}
	var pats []string
	for _, c := range g.List {
		args, ok := trimGoEmbed(c.Text)
		if !ok {
			continue
		}
		list, err := parseGoEmbed(args)
		if err != nil || len(list) == 0 {
			// Keep an empty marker so applyEmbeds can report a useful error.
			if len(list) == 0 {
				pats = append(pats, "")
			}
			continue
		}
		pats = append(pats, list...)
	}
	return pats
}

func trimGoEmbed(text string) (string, bool) {
	text = strings.TrimSuffix(text, "\n")
	if strings.HasPrefix(text, "/*") && strings.HasSuffix(text, "*/") {
		text = strings.TrimSpace(text[2 : len(text)-2])
	}
	if !strings.HasPrefix(text, goEmbedPrefix) {
		return "", false
	}
	rest := text[len(goEmbedPrefix):]
	if rest != "" {
		r := rune(rest[0])
		if !unicode.IsSpace(r) && r != '"' && r != '`' {
			return "", false
		}
	}
	return strings.TrimSpace(rest), true
}

// parseGoEmbed parses the text following //go:embed to extract glob patterns.
// It accepts unquoted space-separated patterns as well as quoted string literals.
func parseGoEmbed(args string) ([]string, error) {
	var list []string
	for args = strings.TrimSpace(args); args != ""; args = strings.TrimSpace(args) {
		var p string
		switch args[0] {
		default:
			i := len(args)
			for j, c := range args {
				if unicode.IsSpace(c) {
					i = j
					break
				}
			}
			p = args[:i]
			args = args[i:]
		case '`':
			i := strings.Index(args[1:], "`")
			if i < 0 {
				return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args)
			}
			p = args[1 : 1+i]
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
					p = q
					args = args[i+1:]
					goto next
				}
			}
			return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args)
		}
	next:
		if args != "" && !unicode.IsSpace(rune(args[0])) {
			return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args)
		}
		list = append(list, p)
	}
	return list, nil
}

func (interp *Interpreter) applyEmbeds(roots []*node) error {
	for _, root := range roots {
		if root == nil {
			continue
		}
		var err error
		root.Walk(func(n *node) bool {
			if err != nil {
				return false
			}
			if len(n.embed) == 0 {
				return true
			}
			switch n.kind {
			case valueSpec, defineStmt, defineXStmt:
				err = interp.initEmbedVar(n)
			}
			return true
		}, nil)
		if err != nil {
			return err
		}
	}
	return nil
}

func (interp *Interpreter) initEmbedVar(n *node) error {
	if n.nleft != 1 {
		return n.cfgErrorf("go:embed cannot apply to multiple vars")
	}
	if n.scope == nil || len(n.child) == 0 {
		return n.cfgErrorf("go:embed: missing variable")
	}
	ident := n.child[0].ident
	sym, _, ok := n.scope.lookup(ident)
	if !ok || sym == nil {
		return n.cfgErrorf("go:embed: unknown variable %s", ident)
	}

	patterns := n.embed
	for _, p := range patterns {
		if p == "" {
			return n.cfgErrorf("invalid go:embed: missing pattern")
		}
	}

	srcFile := interp.fset.Position(n.pos).Filename
	srcDir := path.Dir(filepath.ToSlash(srcFile))

	files, err := interp.resolveEmbed(srcDir, patterns)
	if err != nil {
		return n.cfgErrorf("%v", err)
	}

	kind := embedVarKind(sym.typ)
	switch kind {
	case embedKindUnknown:
		return n.cfgErrorf("go:embed cannot apply to var of type %s", embedTypeName(sym.typ))
	case embedKindString, embedKindBytes:
		if len(files) != 1 {
			return n.cfgErrorf("invalid go:embed: multiple files for type %s", embedTypeName(sym.typ))
		}
		var data []byte
		for _, v := range files {
			data = v
		}
		if err := setEmbedScalar(interp, sym, kind, data); err != nil {
			return n.cfgErrorf("%v", err)
		}
	case embedKindFS:
		fsys := makeEmbedFS(files)
		if sym.index < 0 || sym.index >= len(interp.frame.data) {
			return n.cfgErrorf("go:embed: uninitialized frame for %s", ident)
		}
		interp.frame.data[sym.index] = reflect.ValueOf(fsys)
	}
	return nil
}

const (
	embedKindUnknown = iota
	embedKindString
	embedKindBytes
	embedKindFS
)

func embedVarKind(t *itype) int {
	if t == nil {
		return embedKindUnknown
	}
	if isOfficialEmbedFS(t) {
		return embedKindFS
	}
	if t.cat == stringT {
		return embedKindString
	}
	if t.cat == sliceT && t.val != nil && t.val.cat == uint8T {
		return embedKindBytes
	}
	if t.cat == linkedT {
		return embedVarKind(t.val)
	}
	rt := t.TypeOf()
	if rt == nil {
		return embedKindUnknown
	}
	if rt == reflect.TypeOf(embed.FS{}) {
		return embedKindFS
	}
	switch rt.Kind() {
	case reflect.String:
		return embedKindString
	case reflect.Slice:
		if rt.Elem().Kind() == reflect.Uint8 {
			return embedKindBytes
		}
	}
	return embedKindUnknown
}

func isOfficialEmbedFS(t *itype) bool {
	if t == nil {
		return false
	}
	if t.cat == valueT && t.rtype != nil {
		return t.rtype == reflect.TypeOf(embed.FS{}) || t.rtype == reflect.TypeOf((*embed.FS)(nil)).Elem()
	}
	rt := t.TypeOf()
	return rt != nil && rt == reflect.TypeOf(embed.FS{})
}

func embedTypeName(t *itype) string {
	if t == nil {
		return "<nil>"
	}
	if t.str != "" {
		return t.str
	}
	if rt := t.TypeOf(); rt != nil {
		return rt.String()
	}
	return t.cat.String()
}

func setEmbedScalar(interp *Interpreter, sym *symbol, kind int, data []byte) error {
	if sym.index < 0 || sym.index >= len(interp.frame.data) {
		return fmt.Errorf("go:embed: uninitialized frame")
	}
	rt := interp.frame.data[sym.index].Type()
	if rt == nil && sym.typ != nil {
		rt = sym.typ.frameType()
	}
	switch kind {
	case embedKindString:
		v := reflect.New(rt).Elem()
		v.SetString(string(data))
		interp.frame.data[sym.index] = v
	case embedKindBytes:
		v := reflect.New(rt).Elem()
		// Copy so later mutation of the slice cannot alias interpreter internals
		// unexpectedly when converting from string storage.
		cp := append([]byte(nil), data...)
		v.SetBytes(cp)
		interp.frame.data[sym.index] = v
	}
	return nil
}

func (interp *Interpreter) resolveEmbed(srcDir string, patterns []string) (map[string][]byte, error) {
	if _, ok := interp.filesystem.(*realFS); ok {
		return resolveEmbedOS(srcDir, patterns)
	}
	return resolveEmbedFS(interp.filesystem, srcDir, patterns)
}

func validEmbedPattern(pattern string) bool {
	return pattern != "." && fs.ValidPath(pattern)
}

func resolveEmbedFS(fsys fs.FS, srcDir string, patterns []string) (map[string][]byte, error) {
	files := map[string][]byte{}
	for _, pattern := range patterns {
		matched, err := matchEmbedPattern(func(glob string) ([]string, error) {
			return fs.Glob(fsys, joinSrc(srcDir, glob))
		}, func(name string) (fs.FileInfo, error) {
			return fs.Stat(fsys, name)
		}, func(root string, all bool, out map[string][]byte) (int, error) {
			return walkEmbedFS(fsys, root, srcDir, all, out)
		}, func(name string) ([]byte, error) {
			return fs.ReadFile(fsys, name)
		}, srcDir, pattern, files)
		if err != nil {
			return nil, err
		}
		if matched == 0 {
			return nil, fmt.Errorf("pattern %s: no matching files found", pattern)
		}
	}
	return files, nil
}

func resolveEmbedOS(srcDir string, patterns []string) (map[string][]byte, error) {
	dir := filepath.FromSlash(srcDir)
	files := map[string][]byte{}
	for _, pattern := range patterns {
		matched, err := matchEmbedPattern(func(glob string) ([]string, error) {
			return filepath.Glob(filepath.Join(dir, filepath.FromSlash(glob)))
		}, func(name string) (fs.FileInfo, error) {
			return os.Lstat(name)
		}, func(root string, all bool, out map[string][]byte) (int, error) {
			return walkEmbedOS(root, dir, all, out)
		}, func(name string) ([]byte, error) {
			return os.ReadFile(name)
		}, srcDir, pattern, files)
		if err != nil {
			return nil, err
		}
		if matched == 0 {
			return nil, fmt.Errorf("pattern %s: no matching files found", pattern)
		}
	}
	return files, nil
}

func matchEmbedPattern(
	globFn func(string) ([]string, error),
	statFn func(string) (fs.FileInfo, error),
	walkFn func(string, bool, map[string][]byte) (int, error),
	readFn func(string) ([]byte, error),
	srcDir, pattern string,
	files map[string][]byte,
) (int, error) {
	all := false
	glob := pattern
	if strings.HasPrefix(pattern, "all:") {
		all = true
		glob = pattern[len("all:"):]
	}
	if _, err := path.Match(glob, ""); err != nil || !validEmbedPattern(glob) {
		return 0, fmt.Errorf("pattern %s: invalid pattern syntax", pattern)
	}

	matches, err := globFn(glob)
	if err != nil {
		return 0, fmt.Errorf("pattern %s: %v", pattern, err)
	}

	matched := 0
	for _, match := range matches {
		info, err := statFn(match)
		if err != nil {
			return 0, fmt.Errorf("pattern %s: %v", pattern, err)
		}
		if info.IsDir() {
			n, err := walkFn(match, all, files)
			if err != nil {
				return 0, err
			}
			if n == 0 {
				rel := relToSrc(srcDir, toSlash(match))
				return 0, fmt.Errorf("pattern %s: cannot embed directory %s: contains no embeddable files", pattern, rel)
			}
			matched += n
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		data, err := readFn(match)
		if err != nil {
			return 0, fmt.Errorf("pattern %s: %v", pattern, err)
		}
		rel := relToSrc(srcDir, toSlash(match))
		files[rel] = data
		matched++
	}
	return matched, nil
}

func walkEmbedFS(fsys fs.FS, root, srcDir string, all bool, files map[string][]byte) (int, error) {
	count := 0
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		if hiddenEmbedName(d.Name()) && !all {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		files[relToSrc(srcDir, p)] = data
		count++
		return nil
	})
	return count, err
}

func walkEmbedOS(root, srcDir string, all bool, files map[string][]byte) (int, error) {
	count := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		if hiddenEmbedName(d.Name()) && !all {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[relToSrc(filepath.ToSlash(srcDir), filepath.ToSlash(p))] = data
		count++
		return nil
	})
	return count, err
}

func hiddenEmbedName(name string) bool {
	return name != "" && (name[0] == '.' || name[0] == '_')
}

func joinSrc(srcDir, glob string) string {
	if srcDir == "" || srcDir == "." {
		return glob
	}
	return path.Join(srcDir, glob)
}

func relToSrc(srcDir, name string) string {
	name = path.Clean(toSlash(name))
	srcDir = path.Clean(toSlash(srcDir))
	if srcDir == "." || srcDir == "" {
		return name
	}
	if rel, ok := strings.CutPrefix(name, srcDir+"/"); ok {
		return rel
	}
	return name
}

func toSlash(p string) string {
	return filepath.ToSlash(p)
}

// embedFile is the compiler-known layout of embed.file.
type embedFile struct {
	name string
	data string
	hash [16]byte
}

// makeEmbedFS builds an embed.FS from a name→contents map.
// Intermediate directories are added so Open/ReadDir work on parent paths.
func makeEmbedFS(files map[string][]byte) embed.FS {
	have := map[string]bool{}
	var list []embedFile
	for name, data := range files {
		name = path.Clean(name)
		if name == "." || name == "" {
			continue
		}
		if !have[name] {
			have[name] = true
			list = append(list, embedFile{name: name, data: string(data)})
		}
		for dir := path.Dir(name); dir != "." && !have[dir]; dir = path.Dir(dir) {
			have[dir] = true
			list = append(list, embedFile{name: dir + "/"})
		}
	}
	sort.Slice(list, func(i, j int) bool {
		return embedFileLess(list[i].name, list[j].name)
	})

	var fsys embed.FS
	type efs struct {
		files *[]embedFile
	}
	// The compiler-known embed.FS layout is a single *[]file field.
	sl := list
	(*efs)(unsafe.Pointer(&fsys)).files = &sl
	return fsys
}

func embedFileNameSplit(name string) (dir, elem string, isDir bool) {
	if name != "" && name[len(name)-1] == '/' {
		isDir = true
		name = name[:len(name)-1]
	}
	i := strings.LastIndexByte(name, '/')
	if i < 0 {
		return ".", name, isDir
	}
	return name[:i], name[i+1:], isDir
}

func embedFileLess(x, y string) bool {
	xdir, xelem, _ := embedFileNameSplit(x)
	ydir, yelem, _ := embedFileNameSplit(y)
	return xdir < ydir || xdir == ydir && xelem < yelem
}
