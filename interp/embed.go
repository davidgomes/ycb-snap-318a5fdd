package interp

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"io"
	"io/fs"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

const embedNewFS = "_yaegiNewFS"

func embedSymbols() map[string]reflect.Value {
	return map[string]reflect.Value{
		"FS":       reflect.ValueOf((*embedFS)(nil)),
		embedNewFS: reflect.ValueOf(newEmbedFS),
	}
}

// embedFS is the interpreter implementation of embed.FS.
type embedFS struct {
	files *[]embedFile // sorted by name, including implicit directories
}

type embedFile struct {
	name string
	data string
	dir  bool
}

// newEmbedFS builds an embedFS from alternating name, content arguments.
func newEmbedFS(args ...string) embedFS {
	m := map[string]embedFile{}
	for i := 0; i+1 < len(args); i += 2 {
		name := args[i]
		m[name] = embedFile{name: name, data: args[i+1]}
		for d := path.Dir(name); d != "."; d = path.Dir(d) {
			m[d] = embedFile{name: d, dir: true}
		}
	}
	files := make([]embedFile, 0, len(m))
	for _, f := range m {
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	return embedFS{files: &files}
}

func (f *embedFile) Name() string { return path.Base(f.name) }
func (f *embedFile) Size() int64  { return int64(len(f.data)) }
func (f *embedFile) Mode() fs.FileMode {
	if f.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}
func (f *embedFile) ModTime() time.Time         { return time.Time{} }
func (f *embedFile) IsDir() bool                { return f.dir }
func (f *embedFile) Sys() interface{}           { return nil }
func (f *embedFile) Type() fs.FileMode          { return f.Mode().Type() }
func (f *embedFile) Info() (fs.FileInfo, error) { return f, nil }
func (f *embedFile) String() string             { return fs.FormatFileInfo(f) }

func (e embedFS) lookup(name string) *embedFile {
	if name == "." {
		return &embedFile{name: ".", dir: true}
	}
	if e.files == nil {
		return nil
	}
	files := *e.files
	i := sort.Search(len(files), func(i int) bool { return files[i].name >= name })
	if i < len(files) && files[i].name == name {
		return &files[i]
	}
	return nil
}

func (e embedFS) readDir(dir string) []fs.DirEntry {
	var list []fs.DirEntry
	if e.files == nil {
		return list
	}
	for i := range *e.files {
		f := &(*e.files)[i]
		if path.Dir(f.name) == dir {
			list = append(list, f)
		}
	}
	return list
}

// Open implements fs.FS.
func (e embedFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	f := e.lookup(name)
	if f == nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	if f.dir {
		return &embedOpenDir{f: f, entries: e.readDir(name)}, nil
	}
	return &embedOpenFile{f: f}, nil
}

// ReadDir implements fs.ReadDirFS.
func (e embedFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrInvalid}
	}
	f := e.lookup(name)
	if f == nil {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrNotExist}
	}
	if !f.dir {
		return nil, &fs.PathError{Op: "read", Path: name, Err: errors.New("not a directory")}
	}
	return e.readDir(name), nil
}

// ReadFile implements fs.ReadFileFS.
func (e embedFS) ReadFile(name string) ([]byte, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	f := e.lookup(name)
	if f == nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	if f.dir {
		return nil, &fs.PathError{Op: "read", Path: name, Err: errors.New("is a directory")}
	}
	return []byte(f.data), nil
}

type embedOpenFile struct {
	f      *embedFile
	offset int64
}

func (o *embedOpenFile) Close() error               { return nil }
func (o *embedOpenFile) Stat() (fs.FileInfo, error) { return o.f, nil }

func (o *embedOpenFile) Read(b []byte) (int, error) {
	if o.offset >= int64(len(o.f.data)) {
		return 0, io.EOF
	}
	if o.offset < 0 {
		return 0, &fs.PathError{Op: "read", Path: o.f.name, Err: fs.ErrInvalid}
	}
	n := copy(b, o.f.data[o.offset:])
	o.offset += int64(n)
	return n, nil
}

func (o *embedOpenFile) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekCurrent:
		offset += o.offset
	case io.SeekEnd:
		offset += int64(len(o.f.data))
	}
	if offset < 0 || offset > int64(len(o.f.data)) {
		return 0, &fs.PathError{Op: "seek", Path: o.f.name, Err: fs.ErrInvalid}
	}
	o.offset = offset
	return offset, nil
}

func (o *embedOpenFile) ReadAt(b []byte, offset int64) (int, error) {
	if offset < 0 || offset > int64(len(o.f.data)) {
		return 0, &fs.PathError{Op: "read", Path: o.f.name, Err: fs.ErrInvalid}
	}
	n := copy(b, o.f.data[offset:])
	if n < len(b) {
		return n, io.EOF
	}
	return n, nil
}

type embedOpenDir struct {
	f       *embedFile
	entries []fs.DirEntry
	offset  int
}

func (d *embedOpenDir) Close() error               { return nil }
func (d *embedOpenDir) Stat() (fs.FileInfo, error) { return d.f, nil }
func (d *embedOpenDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.f.name, Err: errors.New("is a directory")}
}

func (d *embedOpenDir) ReadDir(count int) ([]fs.DirEntry, error) {
	n := len(d.entries) - d.offset
	if n == 0 && count > 0 {
		return nil, io.EOF
	}
	if count > 0 && n > count {
		n = count
	}
	list := make([]fs.DirEntry, n)
	copy(list, d.entries[d.offset:d.offset+n])
	d.offset += n
	return list, nil
}

// processEmbed rewrites variables annotated with //go:embed directives
// so that their initial value is the embedded content.
func (interp *Interpreter) processEmbed(f *ast.File, name string) error {
	embedName := ""
	for _, imp := range f.Imports {
		if imp.Path.Value == `"embed"` {
			embedName = "embed"
			if imp.Name != nil {
				embedName = imp.Name.Name
			}
		}
	}
	dir := path.Dir(name)

	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			doc := vs.Doc
			if doc == nil && !gd.Lparen.IsValid() {
				doc = gd.Doc
			}
			patterns, err := embedPatterns(doc)
			if err != nil {
				return err
			}
			if len(patterns) == 0 {
				continue
			}
			pos := interp.fset.Position(vs.Pos())
			if embedName == "" {
				return fmt.Errorf("%s: go:embed only allowed in Go files that import \"embed\"", pos)
			}
			if len(vs.Names) != 1 || len(vs.Values) != 0 || vs.Type == nil {
				return fmt.Errorf("%s: go:embed requires a single variable with a type and no initializer", pos)
			}
			files, err := interp.embedResolve(dir, patterns)
			if err != nil {
				return fmt.Errorf("%s: %w", pos, err)
			}
			var value ast.Expr
			switch kind := embedKind(vs.Type, embedName); kind {
			case "string", "bytes":
				if len(files) != 1 {
					return fmt.Errorf("%s: invalid go:embed: multiple files for type %s", pos, kind)
				}
				var content string
				for _, c := range files {
					content = c
				}
				value = &ast.BasicLit{ValuePos: vs.Pos(), Kind: token.STRING, Value: strconv.Quote(content)}
				if kind == "bytes" {
					value = &ast.CallExpr{Fun: &ast.ArrayType{Lbrack: vs.Pos(), Elt: ast.NewIdent("byte")}, Args: []ast.Expr{value}}
				}
			case "fs":
				if embedName == "_" {
					return fmt.Errorf("%s: go:embed embed.FS requires a named import of \"embed\"", pos)
				}
				names := make([]string, 0, len(files))
				for n := range files {
					names = append(names, n)
				}
				sort.Strings(names)
				args := make([]ast.Expr, 0, 2*len(names))
				for _, n := range names {
					args = append(args,
						&ast.BasicLit{ValuePos: vs.Pos(), Kind: token.STRING, Value: strconv.Quote(n)},
						&ast.BasicLit{ValuePos: vs.Pos(), Kind: token.STRING, Value: strconv.Quote(files[n])})
				}
				var fun ast.Expr = &ast.Ident{NamePos: vs.Pos(), Name: embedNewFS}
				if embedName != "." {
					fun = &ast.SelectorExpr{X: &ast.Ident{NamePos: vs.Pos(), Name: embedName}, Sel: fun.(*ast.Ident)}
				}
				value = &ast.CallExpr{Fun: fun, Lparen: vs.Pos(), Args: args}
			default:
				return fmt.Errorf("%s: go:embed cannot apply to var of this type", pos)
			}
			vs.Values = []ast.Expr{value}
		}
	}
	return nil
}

func embedKind(t ast.Expr, embedName string) string {
	switch t := t.(type) {
	case *ast.Ident:
		switch {
		case t.Name == "string":
			return "string"
		case t.Name == "FS" && embedName == ".":
			return "fs"
		}
	case *ast.ArrayType:
		if id, ok := t.Elt.(*ast.Ident); ok && t.Len == nil && (id.Name == "byte" || id.Name == "uint8") {
			return "bytes"
		}
	case *ast.SelectorExpr:
		if id, ok := t.X.(*ast.Ident); ok && id.Name == embedName && t.Sel.Name == "FS" {
			return "fs"
		}
	}
	return ""
}

func embedPatterns(doc *ast.CommentGroup) ([]string, error) {
	if doc == nil {
		return nil, nil
	}
	var patterns []string
	for _, c := range doc.List {
		text, ok := strings.CutPrefix(c.Text, "//go:embed")
		if !ok || (text != "" && text[0] != ' ' && text[0] != '\t') {
			continue
		}
		fields, err := embedFields(text)
		if err != nil {
			return nil, err
		}
		if len(fields) == 0 {
			return nil, errors.New("go:embed: no patterns")
		}
		patterns = append(patterns, fields...)
	}
	return patterns, nil
}

func embedFields(s string) ([]string, error) {
	var fields []string
	for {
		s = strings.TrimLeft(s, " \t")
		if s == "" {
			return fields, nil
		}
		if s[0] == '"' || s[0] == '`' {
			q, err := strconv.QuotedPrefix(s)
			if err != nil {
				return nil, fmt.Errorf("go:embed: invalid quoted pattern: %s", s)
			}
			u, _ := strconv.Unquote(q)
			fields = append(fields, u)
			s = s[len(q):]
			continue
		}
		i := strings.IndexAny(s, " \t")
		if i < 0 {
			i = len(s)
		}
		fields = append(fields, s[:i])
		s = s[i:]
	}
}

func embedHidden(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// embedResolve returns the embedded file contents indexed by path relative to dir.
func (interp *Interpreter) embedResolve(dir string, patterns []string) (map[string]string, error) {
	fsys := interp.opt.filesystem
	files := map[string]string{}
	for _, pattern := range patterns {
		p, all := strings.CutPrefix(pattern, "all:")
		if _, err := path.Match(p, ""); err != nil || !fs.ValidPath(p) || p == "." {
			return nil, fmt.Errorf("pattern %s: invalid pattern syntax", pattern)
		}
		matches := []string{""}
		for _, elem := range strings.Split(p, "/") {
			var next []string
			for _, m := range matches {
				entries, err := fs.ReadDir(fsys, path.Join(dir, m))
				if err != nil {
					continue
				}
				for _, e := range entries {
					if ok, _ := path.Match(elem, e.Name()); ok {
						next = append(next, path.Join(m, e.Name()))
					}
				}
			}
			matches = next
		}
		count := 0
		for _, m := range matches {
			full := path.Join(dir, m)
			info, err := fs.Stat(fsys, full)
			if err != nil {
				return nil, err
			}
			if !info.IsDir() {
				b, err := fs.ReadFile(fsys, full)
				if err != nil {
					return nil, err
				}
				files[m] = string(b)
				count++
				continue
			}
			err = fs.WalkDir(fsys, full, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if p == full {
					return nil
				}
				if !all && embedHidden(d.Name()) {
					if d.IsDir() {
						return fs.SkipDir
					}
					return nil
				}
				if d.IsDir() {
					return nil
				}
				b, err := fs.ReadFile(fsys, p)
				if err != nil {
					return err
				}
				files[path.Join(m, strings.TrimPrefix(p, full+"/"))] = string(b)
				count++
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
		if count == 0 {
			return nil, fmt.Errorf("pattern %s: no matching files found", pattern)
		}
	}
	return files, nil
}
