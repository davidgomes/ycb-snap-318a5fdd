package interp

import (
	"errors"
	"fmt"
	"go/ast"
	"io/fs"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"

	iembed "github.com/traefik/yaegi/internal/embed"
)

const embedDirective = "//go:embed"

var (
	embedFSType    = reflect.TypeOf(iembed.FS{})
	embedBytesType = reflect.TypeOf([]byte(nil))
)

// embedSpec holds the state of a //go:embed directive attached to a var declaration.
type embedSpec struct {
	patterns []string
	value    reflect.Value // embedded content, computed during CFG
}

// embedPatterns returns the patterns of all //go:embed directives in the given comment groups.
func embedPatterns(groups ...*ast.CommentGroup) ([]string, error) {
	var patterns []string
	for _, g := range groups {
		if g == nil {
			continue
		}
		for _, c := range g.List {
			if !strings.HasPrefix(c.Text, embedDirective) {
				continue
			}
			args := c.Text[len(embedDirective):]
			if args != "" && args[0] != ' ' && args[0] != '\t' {
				continue // e.g. //go:embedded
			}
			p, err := parseEmbedArgs(args)
			if err != nil {
				return nil, err
			}
			if len(p) == 0 {
				return nil, errors.New("usage: //go:embed pattern...")
			}
			patterns = append(patterns, p...)
		}
	}
	return patterns, nil
}

// parseEmbedArgs splits a directive argument list into patterns. Patterns
// are separated by spaces and may be quoted using Go string syntax.
func parseEmbedArgs(args string) ([]string, error) {
	var list []string
	for args = strings.TrimSpace(args); args != ""; args = strings.TrimSpace(args) {
		var p string
		switch args[0] {
		case '"', '`':
			end := 1
			for ; end < len(args); end++ {
				if args[end] == '\\' && args[0] == '"' {
					end++
					continue
				}
				if args[end] == args[0] {
					break
				}
			}
			if end >= len(args) {
				return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args)
			}
			var err error
			if p, err = strconv.Unquote(args[:end+1]); err != nil {
				return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args[:end+1])
			}
			args = args[end+1:]
			if args != "" && !unicode.IsSpace(rune(args[0])) {
				return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args)
			}
		default:
			i := strings.IndexFunc(args, unicode.IsSpace)
			if i < 0 {
				i = len(args)
			}
			p, args = args[:i], args[i:]
		}
		list = append(list, p)
	}
	return list, nil
}

// embedFiles resolves patterns relative to dir in the interpreter source
// filesystem. It returns the matched files content, indexed by path relative
// to dir, and whether a pattern matched a directory.
func (interp *Interpreter) embedFiles(dir string, patterns []string) (map[string][]byte, bool, error) {
	fsys := interp.opt.filesystem
	files := map[string][]byte{}
	hasDir := false

	for _, pattern := range patterns {
		all := false
		p := pattern
		if strings.HasPrefix(p, "all:") {
			all, p = true, p[len("all:"):]
		}
		if _, err := path.Match(p, ""); err != nil || p == "." || !fs.ValidPath(p) {
			return nil, false, fmt.Errorf("pattern %s: invalid pattern syntax", pattern)
		}

		matches := globFS(fsys, dir, p)
		if len(matches) == 0 {
			return nil, false, fmt.Errorf("pattern %s: no matching files found", pattern)
		}

		for _, m := range matches {
			full := path.Join(dir, m)
			info, err := fs.Stat(fsys, full)
			if err != nil {
				return nil, false, fmt.Errorf("pattern %s: %w", pattern, err)
			}
			if !info.IsDir() {
				if !info.Mode().IsRegular() {
					return nil, false, fmt.Errorf("pattern %s: cannot embed irregular file %s", pattern, m)
				}
				b, err := fs.ReadFile(fsys, full)
				if err != nil {
					return nil, false, fmt.Errorf("pattern %s: %w", pattern, err)
				}
				files[m] = b
				continue
			}

			hasDir = true
			n := len(files)
			if err := walkEmbedDir(fsys, dir, m, all, files); err != nil {
				return nil, false, fmt.Errorf("pattern %s: %w", pattern, err)
			}
			if len(files) == n {
				return nil, false, fmt.Errorf("pattern %s: cannot embed directory %s: contains no embeddable files", pattern, m)
			}
		}
	}
	return files, hasDir, nil
}

// walkEmbedDir adds to files all the embeddable files in the tree rooted at rel.
func walkEmbedDir(fsys fs.FS, dir, rel string, all bool, files map[string][]byte) error {
	entries, err := fs.ReadDir(fsys, path.Join(dir, rel))
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if !all && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
			continue
		}
		p := path.Join(rel, name)
		if e.IsDir() {
			if err := walkEmbedDir(fsys, dir, p, all, files); err != nil {
				return err
			}
			continue
		}
		if !e.Type().IsRegular() {
			continue
		}
		b, err := fs.ReadFile(fsys, path.Join(dir, p))
		if err != nil {
			return err
		}
		files[p] = b
	}
	return nil
}

// globFS returns the paths relative to dir matching the slash-separated
// pattern, using path.Match syntax for each element.
func globFS(fsys fs.FS, dir, pattern string) []string {
	matches := []string{""}
	for _, elem := range strings.Split(pattern, "/") {
		var next []string
		for _, m := range matches {
			if !strings.ContainsAny(elem, `*?[\`) {
				p := path.Join(m, elem)
				if _, err := fs.Stat(fsys, path.Join(dir, p)); err == nil {
					next = append(next, p)
				}
				continue
			}
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
	sort.Strings(matches)
	return matches
}

// setEmbedValue computes the embedded content of the var declared by the
// valueSpec node n, according to its type.
func (interp *Interpreter) setEmbedValue(n *node) error {
	dir := path.Dir(interp.fset.Position(n.pos).Filename)
	files, hasDir, err := interp.embedFiles(dir, n.embed.patterns)
	if err != nil {
		return n.cfgErrorf("%v", err)
	}

	rtype := n.typ.TypeOf()
	switch {
	case rtype == embedFSType:
		n.embed.value = reflect.ValueOf(iembed.New(files))
		return nil
	case n.typ.cat == stringT, rtype == embedBytesType:
	default:
		return n.cfgErrorf("go:embed cannot apply to var of type %s", n.typ.id())
	}

	if hasDir || len(files) > 1 {
		return n.cfgErrorf("invalid go:embed: multiple files for type %s", n.typ.id())
	}
	for _, b := range files {
		if rtype == embedBytesType {
			n.embed.value = reflect.ValueOf(b)
		} else {
			n.embed.value = reflect.ValueOf(string(b)).Convert(rtype)
		}
	}
	return nil
}

// initEmbeds stores the embedded content of global vars in the interpreter
// frame, before any code of the given roots is executed.
func (interp *Interpreter) initEmbeds(roots ...*node) {
	for _, root := range roots {
		for _, decl := range root.child {
			if decl.kind != varDecl {
				continue
			}
			for _, spec := range decl.child {
				if spec.embed == nil || !spec.embed.value.IsValid() {
					continue
				}
				dest := interp.frame.data[spec.child[0].findex]
				if dest.CanSet() {
					dest.Set(spec.embed.value)
				} else {
					interp.frame.data[spec.child[0].findex] = spec.embed.value
				}
			}
		}
	}
}

// declDoc returns the doc comment of a declaration. If not set by the parser,
// as in REPL mode where the comment follows the inserted package clause on the
// same line, it is the comment group ending on the line preceding the declaration.
func (interp *Interpreter) declDoc(d *ast.GenDecl, comments []*ast.CommentGroup) *ast.CommentGroup {
	if d.Doc != nil {
		return d.Doc
	}
	line := interp.fset.Position(d.Pos()).Line
	for _, g := range comments {
		if g.End() < d.Pos() && interp.fset.Position(g.End()).Line == line-1 {
			return g
		}
	}
	return nil
}
