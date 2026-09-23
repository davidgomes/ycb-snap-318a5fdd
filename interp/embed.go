package interp

import (
	"errors"
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
)

const (
	embedPkg    = "embed" // import path and name of the embed package
	embedPrefix = "//go:embed"
)

var embedFSType = reflect.TypeOf(embedFS{})

// embedDirective holds the patterns of the //go:embed directives of a variable.
type embedDirective struct {
	pos      token.Pos // position of the first directive
	patterns []string
}

func hasEmbedDirective(src string) bool {
	return strings.Contains(src, embedPrefix)
}

func isEmbedComment(text string) bool {
	args, ok := strings.CutPrefix(text, embedPrefix)
	return ok && (args == "" || args[0] == ' ' || args[0] == '\t')
}

// embedDirectives returns the //go:embed directives of file f, indexed by the
// package level var specs they apply to. A directive applies to the var spec
// which follows it, and must not be separated from it by another declaration.
func embedDirectives(fset *token.FileSet, f *ast.File) (map[*ast.ValueSpec]*embedDirective, error) {
	var comments []*ast.Comment
	for _, g := range f.Comments {
		for _, c := range g.List {
			if isEmbedComment(c.Text) {
				comments = append(comments, c)
			}
		}
	}
	if len(comments) == 0 {
		return nil, nil
	}

	errorf := func(pos token.Pos, format string, a ...interface{}) error {
		return fmt.Errorf("%s: "+format, append([]interface{}{fset.Position(pos)}, a...)...)
	}

	directives := map[*ast.ValueSpec]*embedDirective{}

	// attach consumes the directives located before end and applies them to spec.
	// A nil spec denotes a location where directives are not allowed.
	attach := func(spec *ast.ValueSpec, end token.Pos) error {
		for ; len(comments) > 0 && comments[0].Pos() < end; comments = comments[1:] {
			c := comments[0]
			switch {
			case spec == nil:
				return errorf(c.Pos(), "misplaced go:embed directive")
			case len(spec.Names) > 1:
				return errorf(c.Pos(), "go:embed cannot apply to multiple vars")
			case len(spec.Values) > 0:
				return errorf(c.Pos(), "go:embed cannot apply to var with initializer")
			}
			patterns, err := parseEmbedPatterns(strings.TrimPrefix(c.Text, embedPrefix))
			if err != nil {
				return errorf(c.Pos(), "%v", err)
			}
			d := directives[spec]
			if d == nil {
				d = &embedDirective{pos: c.Pos()}
				directives[spec] = d
			}
			d.patterns = append(d.patterns, patterns...)
		}
		return nil
	}

	for _, decl := range f.Decls {
		if g, ok := decl.(*ast.GenDecl); ok && g.Tok == token.VAR {
			if g.Lparen.IsValid() {
				if err := attach(nil, g.Lparen); err != nil {
					return nil, err
				}
				for _, s := range g.Specs {
					if err := attach(s.(*ast.ValueSpec), s.Pos()); err != nil {
						return nil, err
					}
					if err := attach(nil, s.End()); err != nil {
						return nil, err
					}
				}
			} else if err := attach(g.Specs[0].(*ast.ValueSpec), g.Pos()); err != nil {
				return nil, err
			}
		}
		if err := attach(nil, decl.End()); err != nil {
			return nil, err
		}
	}
	if len(comments) > 0 {
		return nil, errorf(comments[0].Pos(), "misplaced go:embed directive")
	}
	return directives, nil
}

// parseEmbedPatterns returns the space separated patterns of a //go:embed directive.
// As in Go, patterns containing spaces can be written as Go string literals.
func parseEmbedPatterns(args string) ([]string, error) {
	var patterns []string
	for args = strings.TrimLeftFunc(args, unicode.IsSpace); args != ""; args = strings.TrimLeftFunc(args, unicode.IsSpace) {
		var pattern string
		switch args[0] {
		case '`', '"':
			i := 1
			for ; i < len(args) && args[i] != args[0]; i++ {
				if args[0] == '"' && args[i] == '\\' {
					i++
				}
			}
			if i >= len(args) {
				return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args)
			}
			var err error
			if pattern, err = strconv.Unquote(args[:i+1]); err != nil {
				return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args[:i+1])
			}
			if args = args[i+1:]; args != "" && !unicode.IsSpace(rune(args[0])) {
				return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args)
			}
		default:
			i := strings.IndexFunc(args, unicode.IsSpace)
			if i < 0 {
				i = len(args)
			}
			pattern, args = args[:i], args[i:]
		}
		patterns = append(patterns, pattern)
	}
	if len(patterns) == 0 {
		return nil, errors.New("missing pattern in //go:embed directive")
	}
	return patterns, nil
}

// isEmbedVar returns true if n is a var spec initialized by a //go:embed directive.
func isEmbedVar(n *node) bool {
	_, ok := n.meta.(*embedDirective)
	return ok
}

// embedValue returns the content to assign to the variable declared by the
// var spec n, as specified by its //go:embed directive d.
func (interp *Interpreter) embedValue(n *node, d *embedDirective) (reflect.Value, error) {
	typ := n.typ.frameType()
	isFS := typ == embedFSType
	if !isFS && typ.Kind() != reflect.String && (typ.Kind() != reflect.Slice || typ.Elem().Kind() != reflect.Uint8) {
		return reflect.Value{}, n.cfgErrorf("go:embed cannot apply to var of type %s", n.typ.id())
	}

	fsys := interp.opt.filesystem
	dir := path.Dir(interp.fset.Position(d.pos).Filename)
	names, err := embedFiles(fsys, dir, d.patterns)
	if err != nil {
		return reflect.Value{}, n.cfgErrorf("%v", err)
	}
	if !isFS && len(names) > 1 {
		return reflect.Value{}, n.cfgErrorf("invalid go:embed: multiple files for type %s", n.typ.id())
	}

	files := make(map[string]string, len(names))
	for _, name := range names {
		b, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			return reflect.Value{}, n.cfgErrorf("%v", err)
		}
		files[name] = string(b)
	}

	v := reflect.New(typ).Elem()
	switch {
	case isFS:
		v.Set(reflect.ValueOf(newEmbedFS(files)))
	case typ.Kind() == reflect.String:
		v.SetString(files[names[0]])
	default:
		v.SetBytes([]byte(files[names[0]]))
	}
	return v, nil
}

// embedFiles returns the sorted paths, relative to dir, of the files matching
// the embed patterns. A pattern matching a directory selects all the files of
// its tree, except those starting with '.' or '_', unless the pattern has the
// "all:" prefix.
func embedFiles(fsys fs.FS, dir string, patterns []string) ([]string, error) {
	seen := map[string]bool{}
	var files []string
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			files = append(files, name)
		}
	}

	for _, pattern := range patterns {
		glob, all := strings.CutPrefix(pattern, "all:")
		if _, err := path.Match(glob, ""); err != nil || glob == "." || !fs.ValidPath(glob) {
			return nil, fmt.Errorf("pattern %s: invalid pattern syntax", pattern)
		}

		count := 0
		for _, match := range embedGlob(fsys, dir, glob) {
			name := path.Join(dir, match)
			info, err := fs.Stat(fsys, name)
			if err != nil {
				return nil, fmt.Errorf("pattern %s: %w", pattern, err)
			}
			switch {
			case info.Mode().IsRegular():
				count++
				add(match)
			case info.IsDir():
				n := 0
				err := fs.WalkDir(fsys, name, func(p string, e fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if p != name && !all && (e.Name()[0] == '.' || e.Name()[0] == '_') {
						if e.IsDir() {
							return fs.SkipDir
						}
						return nil
					}
					if e.Type().IsRegular() {
						n++
						add(match + p[len(name):])
					}
					return nil
				})
				if err != nil {
					return nil, fmt.Errorf("pattern %s: %w", pattern, err)
				}
				if n == 0 {
					return nil, fmt.Errorf("pattern %s: cannot embed directory %s: contains no embeddable files", pattern, match)
				}
				count += n
			default:
				return nil, fmt.Errorf("pattern %s: cannot embed irregular file %s", pattern, match)
			}
		}
		if count == 0 {
			return nil, fmt.Errorf("pattern %s: no matching files found", pattern)
		}
	}

	sort.Strings(files)
	return files, nil
}

// embedGlob returns the paths, relative to dir, matching the glob pattern.
// Contrary to fs.Glob, dir is not interpreted as a pattern, and is not
// required to be a valid fs.FS path, as the source file system may accept
// relative paths such as "../foo".
func embedGlob(fsys fs.FS, dir, pattern string) []string {
	matches := []string{""}
	for _, elem := range strings.Split(pattern, "/") {
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
	return matches
}
