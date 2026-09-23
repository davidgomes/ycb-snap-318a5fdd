package interp

import (
	"errors"
	"fmt"
	"go/ast"
	"io"
	"io/fs"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

// embedPattern is one pattern from a //go:embed directive.
type embedPattern struct {
	pattern string
	all     bool // all: prefix keeps names that start with '.' or '_'
}

// embedFS is the concrete type exposed as embed.FS. It implements fs.FS,
// fs.ReadFileFS and fs.ReadDirFS.
type embedFS struct {
	files map[string][]byte
}

var embedFSType = reflect.TypeOf(embedFS{})

var (
	_ fs.FS          = embedFS{}
	_ fs.ReadFileFS  = embedFS{}
	_ fs.ReadDirFS   = embedFS{}
	_ fs.ReadDirFile = (*embedDir)(nil)
)

func (f embedFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if name == "." {
		return newEmbedDir(f, "."), nil
	}
	if data, ok := f.files[name]; ok {
		b := make([]byte, len(data))
		copy(b, data)
		return &embedFile{name: name, data: b}, nil
	}
	if f.isDir(name) {
		return newEmbedDir(f, name), nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

func (f embedFS) ReadFile(name string) ([]byte, error) {
	if !fs.ValidPath(name) || name == "." {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrInvalid}
	}
	data, ok := f.files[name]
	if !ok {
		if f.isDir(name) {
			return nil, &fs.PathError{Op: "read", Path: name, Err: errors.New("is a directory")}
		}
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrNotExist}
	}
	b := make([]byte, len(data))
	copy(b, data)
	return b, nil
}

func (f embedFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	if name != "." && !f.isDir(name) {
		if _, ok := f.files[name]; ok {
			return nil, &fs.PathError{Op: "readdir", Path: name, Err: errors.New("not a directory")}
		}
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	return f.dirEntries(name), nil
}

func (f embedFS) isDir(name string) bool {
	prefix := name + "/"
	for p := range f.files {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

func (f embedFS) dirEntries(name string) []fs.DirEntry {
	prefix := ""
	if name != "." {
		prefix = name + "/"
	}
	seen := map[string]embedEntry{}
	for p, data := range f.files {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		elem, _, more := strings.Cut(rest, "/")
		if elem == "" {
			continue
		}
		if more {
			if _, ok := seen[elem]; !ok {
				seen[elem] = embedEntry{name: elem, isDir: true}
			}
			continue
		}
		seen[elem] = embedEntry{name: elem, data: data}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]fs.DirEntry, len(names))
	for i, n := range names {
		e := seen[n]
		out[i] = e
	}
	return out
}

type embedEntry struct {
	name  string
	data  []byte
	isDir bool
}

func (e embedEntry) Name() string { return e.name }
func (e embedEntry) IsDir() bool  { return e.isDir }
func (e embedEntry) Type() fs.FileMode {
	if e.isDir {
		return fs.ModeDir
	}
	return 0
}
func (e embedEntry) Info() (fs.FileInfo, error) { return embedInfo{e}, nil }

type embedInfo struct{ embedEntry }

func (i embedInfo) Name() string { return i.name }
func (i embedInfo) Size() int64 {
	if i.isDir {
		return 0
	}
	return int64(len(i.data))
}
func (i embedInfo) Mode() fs.FileMode {
	if i.isDir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}
func (i embedInfo) ModTime() time.Time { return time.Time{} }
func (i embedInfo) IsDir() bool        { return i.isDir }
func (i embedInfo) Sys() interface{}   { return nil }

// embedFile is a regular file opened from embedFS.
type embedFile struct {
	name   string
	data   []byte
	offset int
}

func (f *embedFile) Read(p []byte) (int, error) {
	if f.offset >= len(f.data) {
		return 0, io.EOF
	}
	n := copy(p, f.data[f.offset:])
	f.offset += n
	return n, nil
}

func (f *embedFile) Stat() (fs.FileInfo, error) {
	return embedInfo{embedEntry{name: path.Base(f.name), data: f.data}}, nil
}

func (f *embedFile) Close() error { return nil }

// embedDir is a directory opened from embedFS. It implements fs.ReadDirFile.
type embedDir struct {
	fsys    embedFS
	name    string
	entries []fs.DirEntry
	offset  int
}

func newEmbedDir(fsys embedFS, name string) *embedDir {
	return &embedDir{fsys: fsys, name: name, entries: fsys.dirEntries(name)}
}

func (d *embedDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.name, Err: errors.New("is a directory")}
}

func (d *embedDir) Stat() (fs.FileInfo, error) {
	return embedInfo{embedEntry{name: path.Base(d.name), isDir: true}}, nil
}

func (d *embedDir) Close() error { return nil }

func (d *embedDir) ReadDir(n int) ([]fs.DirEntry, error) {
	if d.offset >= len(d.entries) {
		if n <= 0 {
			return nil, nil
		}
		return nil, io.EOF
	}
	if n <= 0 {
		out := d.entries[d.offset:]
		d.offset = len(d.entries)
		return out, nil
	}
	end := d.offset + n
	if end > len(d.entries) {
		end = len(d.entries)
	}
	out := d.entries[d.offset:end]
	d.offset = end
	if d.offset >= len(d.entries) {
		return out, io.EOF
	}
	return out, nil
}

func embedPatternsFromDoc(doc *ast.CommentGroup) ([]embedPattern, error) {
	if doc == nil {
		return nil, nil
	}
	var out []embedPattern
	for _, c := range doc.List {
		text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
		line, ok := strings.CutPrefix(text, "go:embed")
		if !ok {
			continue
		}
		if line != "" && line[0] != ' ' && line[0] != '\t' {
			continue
		}
		line = strings.TrimSpace(line)
		toks, err := splitEmbedTokens(line)
		if err != nil {
			return nil, err
		}
		if len(toks) == 0 {
			return nil, fmt.Errorf("go:embed: no patterns")
		}
		for _, tok := range toks {
			p, err := parseEmbedPattern(tok)
			if err != nil {
				return nil, err
			}
			out = append(out, p)
		}
	}
	return out, nil
}

func splitEmbedTokens(s string) ([]string, error) {
	var out []string
	i := 0
	for i < len(s) {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			break
		}
		if s[i] == '"' || s[i] == '`' || strings.HasPrefix(s[i:], "all:\"") || strings.HasPrefix(s[i:], "all:`") {
			start := i
			if strings.HasPrefix(s[i:], "all:") {
				i += len("all:")
			}
			q := s[i]
			i++
			if q == '`' {
				for i < len(s) && s[i] != '`' {
					i++
				}
			} else {
				for i < len(s) && s[i] != '"' {
					if s[i] == '\\' && i+1 < len(s) {
						i += 2
						continue
					}
					i++
				}
			}
			if i >= len(s) {
				return nil, fmt.Errorf("go:embed: unterminated string")
			}
			i++
			out = append(out, s[start:i])
			continue
		}
		j := i
		for j < len(s) && s[j] != ' ' && s[j] != '\t' {
			j++
		}
		out = append(out, s[i:j])
		i = j
	}
	return out, nil
}

func parseEmbedPattern(tok string) (embedPattern, error) {
	all := false
	if strings.HasPrefix(tok, "all:") {
		all = true
		tok = tok[len("all:"):]
	}
	if len(tok) > 0 && (tok[0] == '"' || tok[0] == '`') {
		u, err := strconv.Unquote(tok)
		if err != nil {
			return embedPattern{}, fmt.Errorf("go:embed: %v", err)
		}
		tok = u
	}
	if err := validEmbedPattern(tok); err != nil {
		return embedPattern{}, err
	}
	return embedPattern{pattern: tok, all: all}, nil
}

func validEmbedPattern(pattern string) error {
	if pattern == "" || strings.HasPrefix(pattern, "/") || strings.HasSuffix(pattern, "/") {
		return fmt.Errorf("go:embed: invalid pattern %q", pattern)
	}
	for _, elem := range strings.Split(pattern, "/") {
		if elem == "" || elem == "." || elem == ".." {
			return fmt.Errorf("go:embed: invalid pattern %q", pattern)
		}
		if _, err := path.Match(elem, ""); err != nil && !errors.Is(err, path.ErrBadPattern) {
			return fmt.Errorf("go:embed: invalid pattern %q", pattern)
		}
		if _, err := path.Match(elem, "x"); err != nil {
			return fmt.Errorf("go:embed: invalid pattern %q: %v", pattern, err)
		}
	}
	return nil
}

func (interp *Interpreter) resolveEmbed(n *node) error {
	dir := path.Dir(path.Clean(interp.fset.Position(n.pos).Filename))
	if dir == "" {
		dir = "."
	}
	files := map[string][]byte{}
	for _, pat := range n.embedPatterns {
		matched, err := matchEmbed(interp.filesystem, dir, pat)
		if err != nil {
			return n.cfgErrorf("%v", err)
		}
		if len(matched) == 0 {
			return n.cfgErrorf("go:embed: no matching files for pattern %q", pat.pattern)
		}
		for name, data := range matched {
			if _, ok := files[name]; !ok {
				files[name] = data
			}
		}
	}
	val, err := embedValue(n.typ, files)
	if err != nil {
		return n.cfgErrorf("%v", err)
	}
	n.child[0].sym.embed = val
	return nil
}

func embedValue(typ *itype, files map[string][]byte) (reflect.Value, error) {
	ft := typ.frameType()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	switch {
	case ft == embedFSType:
		cp := make(map[string][]byte, len(files))
		for _, name := range names {
			b := make([]byte, len(files[name]))
			copy(b, files[name])
			cp[name] = b
		}
		return reflect.ValueOf(embedFS{files: cp}), nil
	case ft.Kind() == reflect.String:
		if len(names) != 1 {
			return reflect.Value{}, fmt.Errorf("go:embed: string variable requires exactly one file")
		}
		v := reflect.New(ft).Elem()
		v.SetString(string(files[names[0]]))
		return v, nil
	case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Uint8:
		if len(names) != 1 {
			return reflect.Value{}, fmt.Errorf("go:embed: []byte variable requires exactly one file")
		}
		b := make([]byte, len(files[names[0]]))
		copy(b, files[names[0]])
		v := reflect.New(ft).Elem()
		v.Set(reflect.ValueOf(b))
		return v, nil
	default:
		return reflect.Value{}, fmt.Errorf("go:embed cannot apply to var of type %s", typ.id())
	}
}

func matchEmbed(fsys fs.FS, dir string, pat embedPattern) (map[string][]byte, error) {
	out := map[string][]byte{}
	root := dir
	if root == "." {
		root = "."
	}
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := p
		if root != "." {
			if p == root {
				return nil
			}
			rel = strings.TrimPrefix(p, root+"/")
		}
		if rel == "." || rel == "" {
			return nil
		}
		base := path.Base(rel)
		hidden := base != "" && (base[0] == '.' || base[0] == '_')
		if d.IsDir() {
			if hidden && !pat.all {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if pathHidden(rel) && !pat.all {
			return nil
		}
		if !embedMatch(pat.pattern, rel) {
			return nil
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		out[rel] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// embedMatch reports whether rel is the pattern itself or a file inside a
// directory matched by the pattern.
func embedMatch(pattern, rel string) bool {
	if ok, _ := path.Match(pattern, rel); ok {
		return true
	}
	parent := rel
	for {
		next := path.Dir(parent)
		if next == parent || next == "." {
			return false
		}
		if ok, _ := path.Match(pattern, next); ok {
			return true
		}
		parent = next
	}
}

func pathHidden(rel string) bool {
	for _, elem := range strings.Split(rel, "/") {
		if elem == "" {
			continue
		}
		if elem[0] == '.' || elem[0] == '_' {
			return true
		}
	}
	return false
}

func (interp *Interpreter) applyEmbeds() {
	if interp.frame == nil {
		return
	}
	for _, sc := range interp.scopes {
		if sc == nil {
			continue
		}
		for _, sym := range sc.sym {
			if sym == nil || sym.kind != varSym || !sym.embed.IsValid() {
				continue
			}
			if sym.index < 0 || sym.index >= len(interp.frame.data) {
				continue
			}
			v := reflect.New(interp.frame.data[sym.index].Type()).Elem()
			ev := sym.embed
			if ev.Type() != v.Type() {
				ev = ev.Convert(v.Type())
			}
			v.Set(ev)
			interp.frame.data[sym.index] = v
		}
	}
}
