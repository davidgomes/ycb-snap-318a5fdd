package interp

import (
	"embed"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"io/fs"
	"math"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
	"unsafe"
)

const embedDirective = "//go:embed"

var embedFSType = reflect.TypeOf(embed.FS{})

// embedVar is a package level variable initialized from //go:embed directives.
type embedVar struct {
	spec     *node     // valueSpec node of the variable
	dir      string    // directory of the source file, in the interpreter source filesystem
	pos      token.Pos // position of the first directive
	patterns []string
	value    func() reflect.Value // produces the variable content, set by resolveEmbeds
}

func (interp *Interpreter) embedErrorf(pos token.Pos, format string, a ...interface{}) error {
	posString := interp.fset.Position(pos).String()
	posString = strings.TrimPrefix(posString, DefaultSourceName+":")
	return fmt.Errorf("%s: "+format, append([]interface{}{posString}, a...)...)
}

// embedDirectives returns the //go:embed directives of a file, indexed by the
// package level var specification they apply to.
func (interp *Interpreter) embedDirectives(f *ast.File) (map[*ast.ValueSpec]*embedVar, error) {
	comments := f.Comments
	if len(comments) == 0 {
		return nil, nil
	}
	dir := path.Dir(interp.fset.Position(f.Package).Filename)
	res := map[*ast.ValueSpec]*embedVar{}
	ci := 0

	skip := func(end token.Pos) {
		for ci < len(comments) && comments[ci].Pos() < end {
			ci++
		}
	}

	// collect gathers the directives located in the comments before end.
	collect := func(end token.Pos) (*embedVar, error) {
		var ev *embedVar
		for ; ci < len(comments) && comments[ci].Pos() < end; ci++ {
			for _, c := range comments[ci].List {
				args, ok := embedDirectiveArgs(c.Text)
				if !ok {
					continue
				}
				patterns, err := parseEmbedPatterns(args)
				if err != nil {
					return nil, interp.embedErrorf(c.Pos(), "%v", err)
				}
				if len(patterns) == 0 {
					return nil, interp.embedErrorf(c.Pos(), "usage: //go:embed pattern...")
				}
				if ev == nil {
					ev = &embedVar{dir: dir, pos: c.Pos()}
				}
				ev.patterns = append(ev.patterns, patterns...)
			}
		}
		return ev, nil
	}

	misplaced := func(ev *embedVar) error {
		return interp.embedErrorf(ev.pos, "misplaced go:embed directive")
	}

	skip(f.Name.End())
	for _, decl := range f.Decls {
		ev, err := collect(decl.Pos())
		if err != nil {
			return nil, err
		}
		gd, ok := decl.(*ast.GenDecl)
		switch {
		case !ok || gd.Tok != token.VAR:
			if ev != nil {
				return nil, misplaced(ev)
			}
		case !gd.Lparen.IsValid():
			if ev != nil {
				res[gd.Specs[0].(*ast.ValueSpec)] = ev
			}
		default:
			if ev != nil {
				return nil, misplaced(ev)
			}
			skip(gd.Lparen)
			for _, s := range gd.Specs {
				if ev, err = collect(s.Pos()); err != nil {
					return nil, err
				}
				if ev != nil {
					res[s.(*ast.ValueSpec)] = ev
				}
				skip(s.End())
			}
			if ev, err = collect(gd.Rparen); err != nil {
				return nil, err
			}
			if ev != nil {
				return nil, misplaced(ev)
			}
		}
		skip(decl.End())
	}
	ev, err := collect(token.Pos(math.MaxInt))
	if err != nil {
		return nil, err
	}
	if ev != nil {
		return nil, misplaced(ev)
	}
	return res, nil
}

// embedDirectiveArgs returns the arguments of a //go:embed comment line.
func embedDirectiveArgs(text string) (string, bool) {
	if !strings.HasPrefix(text, embedDirective) {
		return "", false
	}
	args := text[len(embedDirective):]
	if args != "" && args[0] != ' ' && args[0] != '\t' {
		return "", false
	}
	return args, true
}

// parseEmbedPatterns splits the arguments of a //go:embed directive in patterns.
// Patterns are separated by spaces and may be Go double-quoted or raw strings.
func parseEmbedPatterns(args string) ([]string, error) {
	var list []string
	for {
		args = strings.TrimLeftFunc(args, unicode.IsSpace)
		if args == "" {
			return list, nil
		}
		var pattern string
	Switch:
		switch args[0] {
		default:
			i := strings.IndexFunc(args, unicode.IsSpace)
			if i < 0 {
				i = len(args)
			}
			pattern, args = args[:i], args[i:]
		case '`':
			i := strings.Index(args[1:], "`")
			if i < 0 {
				return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args)
			}
			pattern, args = args[1:1+i], args[2+i:]
		case '"':
			for i := 1; i < len(args); i++ {
				if args[i] == '\\' {
					i++
					continue
				}
				if args[i] == '"' {
					q, err := strconv.Unquote(args[:i+1])
					if err != nil {
						return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args[:i+1])
					}
					pattern, args = q, args[i+1:]
					break Switch
				}
			}
			return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args)
		}
		if args != "" {
			if r, _ := utf8.DecodeRuneInString(args); !unicode.IsSpace(r) {
				return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args)
			}
		}
		list = append(list, pattern)
	}
}

// resolveEmbeds checks the types of the embed variables of root, and loads
// their content from the source filesystem.
func (interp *Interpreter) resolveEmbeds(root *node) error {
	for _, ev := range interp.embeds[root] {
		n := ev.spec
		t := n.typ.frameType()
		isFS := t == embedFSType
		if !isFS && t.Kind() != reflect.String && (t.Kind() != reflect.Slice || t.Elem().Kind() != reflect.Uint8) {
			return interp.embedErrorf(n.pos, "go:embed cannot apply to var of type %s", n.typ.id())
		}

		files, err := interp.embedFiles(ev, isFS)
		if err != nil {
			return err
		}

		if !isFS {
			if len(files) != 1 {
				return interp.embedErrorf(n.pos, "invalid go:embed: multiple files for type %s", n.typ.id())
			}
			b, err := fs.ReadFile(interp.opt.filesystem, path.Join(ev.dir, files[0]))
			if err != nil {
				return interp.embedErrorf(ev.pos, "%v", err)
			}
			data := string(b)
			ev.value = func() reflect.Value {
				v := reflect.New(t).Elem()
				if t.Kind() == reflect.String {
					v.SetString(data)
				} else {
					v.SetBytes([]byte(data))
				}
				return v
			}
		} else {
			fsys, err := interp.newEmbedFS(ev.dir, files)
			if err != nil {
				return interp.embedErrorf(ev.pos, "%v", err)
			}
			v := reflect.ValueOf(fsys)
			ev.value = func() reflect.Value { return v }
		}

		// The variable content is set before execution, it must not be reset
		// by the regular variable initialization.
		n.gen = nop
	}
	return nil
}

// initEmbeds stores the content of the embed variables of root in the global frame.
func (interp *Interpreter) initEmbeds(root *node) {
	for _, ev := range interp.embeds[root] {
		interp.frame.data[ev.spec.child[0].findex] = ev.value()
	}
}

// embedFiles returns the sorted list of file names matched by the embed
// patterns, relative to the source directory. If isFS is true, the parent
// directories of files are also returned, with a trailing slash.
func (interp *Interpreter) embedFiles(ev *embedVar, isFS bool) ([]string, error) {
	fsys := interp.opt.filesystem
	have := map[string]bool{}
	var list []string

	add := func(name string) {
		if have[name] {
			return
		}
		have[name] = true
		list = append(list, name)
		if !isFS {
			return
		}
		for dir := path.Dir(name); dir != "." && !have[dir+"/"]; dir = path.Dir(dir) {
			have[dir+"/"] = true
			list = append(list, dir+"/")
		}
	}

	for _, p := range ev.patterns {
		pattern, all := strings.CutPrefix(p, "all:")
		if _, err := path.Match(pattern, ""); err != nil || pattern == "." || !fs.ValidPath(pattern) {
			return nil, interp.embedErrorf(ev.pos, "pattern %s: invalid pattern syntax", p)
		}

		matches := embedGlob(fsys, ev.dir, pattern)
		if len(matches) == 0 {
			return nil, interp.embedErrorf(ev.pos, "pattern %s: no matching files found", p)
		}

		for _, m := range matches {
			info, err := fs.Stat(fsys, path.Join(ev.dir, m))
			if err != nil {
				return nil, interp.embedErrorf(ev.pos, "pattern %s: %v", p, err)
			}
			switch {
			case info.Mode().IsRegular():
				add(m)
			case info.IsDir():
				count := 0
				err := embedWalk(fsys, ev.dir, m, all, func(name string) {
					count++
					add(name)
				})
				if err != nil {
					return nil, interp.embedErrorf(ev.pos, "pattern %s: %v", p, err)
				}
				if count == 0 {
					return nil, interp.embedErrorf(ev.pos, "pattern %s: cannot embed directory %s: contains no embeddable files", p, m)
				}
			default:
				return nil, interp.embedErrorf(ev.pos, "pattern %s: cannot embed irregular file %s", p, m)
			}
		}
	}

	sort.Slice(list, func(i, j int) bool { return embedFileLess(list[i], list[j]) })
	return list, nil
}

// embedGlob returns the names, relative to dir, matching the pattern in fsys.
// Each element of the pattern is matched using path.Match.
func embedGlob(fsys fs.FS, dir, pattern string) []string {
	var matches []string
	var glob func(rel string, elems []string)
	glob = func(rel string, elems []string) {
		entries, err := fs.ReadDir(fsys, path.Join(dir, rel))
		if err != nil {
			return
		}
		for _, e := range entries {
			if ok, _ := path.Match(elems[0], e.Name()); !ok {
				continue
			}
			name := path.Join(rel, e.Name())
			if len(elems) == 1 {
				matches = append(matches, name)
				continue
			}
			glob(name, elems[1:])
		}
	}
	glob("", strings.Split(pattern, "/"))
	sort.Strings(matches)
	return matches
}

// embedWalk calls add for each embeddable file in the tree rooted at rel.
func embedWalk(fsys fs.FS, dir, rel string, all bool, add func(string)) error {
	entries, err := fs.ReadDir(fsys, path.Join(dir, rel))
	if err != nil {
		return err
	}
	for _, e := range entries {
		elem := e.Name()
		if isBadEmbedName(elem) || !all && (elem[0] == '.' || elem[0] == '_') {
			continue
		}
		name := path.Join(rel, elem)
		switch {
		case e.IsDir():
			if _, err := fs.Stat(fsys, path.Join(dir, name, "go.mod")); err == nil {
				// Do not cross module boundaries.
				continue
			}
			if err := embedWalk(fsys, dir, name, all, add); err != nil {
				return err
			}
		case e.Type().IsRegular():
			add(name)
		default:
			return fmt.Errorf("cannot embed irregular file %s", name)
		}
	}
	return nil
}

func isBadEmbedName(name string) bool {
	switch name {
	case "", ".bzr", ".hg", ".git", ".svn":
		return true
	}
	return false
}

// embedFileSplit splits a file name in dir and elem, as done for embed.FS
// file lists: a trailing slash denotes a directory.
func embedFileSplit(name string) (dir, elem string) {
	name = strings.TrimSuffix(name, "/")
	i := strings.LastIndexByte(name, '/')
	if i < 0 {
		return ".", name
	}
	return name[:i], name[i+1:]
}

// embedFileLess implements the file order expected by embed.FS: by
// directory first, then by element name.
func embedFileLess(x, y string) bool {
	xdir, xelem := embedFileSplit(x)
	ydir, yelem := embedFileSplit(y)
	return xdir < ydir || xdir == ydir && xelem < yelem
}

// newEmbedFS returns an embed.FS containing files, which must be sorted
// by embedFileLess. As embed.FS has no public constructor, its unexported
// fields are populated in the same way the compiler does.
func (interp *Interpreter) newEmbedFS(dir string, files []string) (embed.FS, error) {
	var fsys embed.FS
	errLayout := errors.New("go:embed: unsupported embed.FS implementation")

	filesField, ok := embedFSType.FieldByName("files")
	if !ok || filesField.Type.Kind() != reflect.Ptr || filesField.Type.Elem().Kind() != reflect.Slice {
		return fsys, errLayout
	}
	sliceType := filesField.Type.Elem()
	fileType := sliceType.Elem()
	nameField, ok1 := fileType.FieldByName("name")
	dataField, ok2 := fileType.FieldByName("data")
	if fileType.Kind() != reflect.Struct || !ok1 || !ok2 || nameField.Type.Kind() != reflect.String || dataField.Type.Kind() != reflect.String {
		return fsys, errLayout
	}

	list := reflect.New(sliceType)
	list.Elem().Set(reflect.MakeSlice(sliceType, len(files), len(files)))
	for i, name := range files {
		var data string
		if !strings.HasSuffix(name, "/") {
			b, err := fs.ReadFile(interp.opt.filesystem, path.Join(dir, name))
			if err != nil {
				return fsys, err
			}
			data = string(b)
		}
		f := list.Elem().Index(i)
		setUnexported(f.FieldByIndex(nameField.Index), reflect.ValueOf(name))
		setUnexported(f.FieldByIndex(dataField.Index), reflect.ValueOf(data))
	}

	v := reflect.ValueOf(&fsys).Elem()
	setUnexported(v.FieldByIndex(filesField.Index), list)
	return fsys, nil
}

// setUnexported sets the addressable, possibly unexported, field f to v.
func setUnexported(f, v reflect.Value) {
	reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Set(v.Convert(f.Type()))
}
