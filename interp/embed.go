package interp

import (
	"fmt"
	"go/ast"
	"io/fs"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	yaegiembed "github.com/traefik/yaegi/embed"
)

const embedDirective = "go:embed"

func parseEmbedDirectives(doc *ast.CommentGroup) ([]string, error) {
	if doc == nil {
		return nil, nil
	}
	var patterns []string
	for _, c := range doc.List {
		text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
		if !strings.HasPrefix(text, embedDirective) {
			continue
		}
		args := strings.TrimSpace(strings.TrimPrefix(text, embedDirective))
		if args == "" {
			return nil, fmt.Errorf("invalid go:embed directive: empty pattern list")
		}
		pats, err := splitEmbedPatterns(args)
		if err != nil {
			return nil, err
		}
		patterns = append(patterns, pats...)
	}
	return patterns, nil
}

func splitEmbedPatterns(s string) ([]string, error) {
	var patterns []string
	for len(s) > 0 {
		s = strings.TrimLeft(s, " \t")
		if s == "" {
			break
		}
		switch s[0] {
		case '"':
			unquoted, err := strconv.Unquote(s)
			if err != nil {
				return nil, fmt.Errorf("invalid quoted pattern in go:embed: %w", err)
			}
			patterns = append(patterns, unquoted)
			end := strings.Index(s[1:], `"`)
			if end < 0 {
				return nil, fmt.Errorf("invalid quoted pattern in go:embed")
			}
			s = s[end+2:]
		case '`':
			end := strings.Index(s[1:], "`")
			if end < 0 {
				return nil, fmt.Errorf("invalid quoted pattern in go:embed")
			}
			patterns = append(patterns, s[1:end+1])
			s = s[end+2:]
		default:
			i := strings.IndexAny(s, " \t")
			if i < 0 {
				patterns = append(patterns, s)
				s = ""
			} else {
				patterns = append(patterns, s[:i])
				s = s[i:]
			}
		}
	}
	return patterns, nil
}

func validateEmbedPattern(p string) error {
	if p == "" {
		return fmt.Errorf("invalid pattern syntax")
	}
	if strings.Contains(p, `\`) {
		return fmt.Errorf("invalid pattern syntax")
	}
	elems := strings.Split(p, "/")
	for _, e := range elems {
		if e == "" {
			return fmt.Errorf("invalid pattern syntax")
		}
		if e == "." || e == ".." {
			return fmt.Errorf("invalid pattern syntax")
		}
	}
	if strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") {
		return fmt.Errorf("invalid pattern syntax")
	}
	return nil
}

func isEmbedFSType(typ *itype) bool {
	if typ == nil {
		return false
	}
	return typ.cat == valueT && typ.rtype == reflect.TypeOf(yaegiembed.FS{})
}

func (interp *Interpreter) processEmbedDecl(n *node, typ *itype) error {
	if n.embedDoc == nil {
		return nil
	}
	if n.anc == nil || n.anc.kind != varDecl || n.anc.anc == nil || n.anc.anc.kind != fileStmt {
		return nil
	}

	patterns, err := parseEmbedDirectives(n.embedDoc)
	if err != nil {
		return n.cfgErrorf("%s", err.Error())
	}
	if len(patterns) == 0 {
		return nil
	}

	pos := interp.fset.Position(n.pos)
	pkgDir := filepath.ToSlash(path.Dir(pos.Filename))

	files, err := resolveEmbedPatterns(interp.opt.filesystem, pkgDir, patterns)
	if err != nil {
		return n.cfgErrorf("%s", err.Error())
	}

	val, err := embedValueForType(typ, files)
	if err != nil {
		return n.cfgErrorf("%s", err.Error())
	}

	l := len(n.child)
	if n.kind == defineStmt || n.kind == defineXStmt {
		l = n.nleft
	} else if n.kind == valueSpec {
		l--
	}

	for i := 0; i < l; i++ {
		ident := n.child[i].ident
		if ident == "_" {
			continue
		}
		sym := n.scope.sym[ident]
		if sym == nil {
			continue
		}
		sym.rval = val
		sym.embedded = true
	}
	n.embedded = true
	return nil
}

func embedValueForType(typ *itype, files map[string][]byte) (reflect.Value, error) {
	if isEmbedFSType(typ) {
		return reflect.ValueOf(yaegiembed.NewFS(files)), nil
	}

	rt := typ.TypeOf()
	switch {
	case rt.Kind() == reflect.String:
		if len(files) != 1 {
			return reflect.Value{}, fmt.Errorf("go:embed requires exactly one file for string variables")
		}
		for _, data := range files {
			return reflect.ValueOf(string(data)), nil
		}
	case rt.Kind() == reflect.Slice && rt.Elem().Kind() == reflect.Uint8:
		if len(files) != 1 {
			return reflect.Value{}, fmt.Errorf("go:embed requires exactly one file for []byte variables")
		}
		for _, data := range files {
			return reflect.ValueOf(append([]byte(nil), data...)), nil
		}
	default:
		return reflect.Value{}, fmt.Errorf("go:embed requires a variable of type string, []byte, or embed.FS")
	}
	return reflect.Value{}, fmt.Errorf("go:embed: no files matched")
}

func resolveEmbedPatterns(fsys fs.FS, pkgDir string, patterns []string) (map[string][]byte, error) {
	allFiles, err := listPackageFiles(fsys, pkgDir)
	if err != nil {
		return nil, err
	}

	matched := map[string][]byte{}
	for _, pattern := range patterns {
		all := false
		if strings.HasPrefix(pattern, "all:") {
			all = true
			pattern = strings.TrimPrefix(pattern, "all:")
		}
		if err := validateEmbedPattern(pattern); err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", pattern, err)
		}

		found := false
		for _, name := range allFiles {
			if ok, _ := path.Match(pattern, name); !ok {
				continue
			}
			if !all && embedExcluded(name) {
				continue
			}
			data, err := fs.ReadFile(fsys, path.Join(pkgDir, name))
			if err != nil {
				return nil, err
			}
			matched[name] = data
			found = true
		}

		if dirMatches(fsys, pkgDir, pattern) {
			subFiles, err := listDirTree(fsys, pkgDir, pattern, all)
			if err != nil {
				return nil, err
			}
			for name, data := range subFiles {
				matched[name] = data
				found = true
			}
		}

		if !found {
			return nil, fmt.Errorf("pattern %s: no matching files found", pattern)
		}
	}

	if len(matched) == 0 {
		return nil, fmt.Errorf("go:embed: no files matched")
	}
	return matched, nil
}

func listPackageFiles(fsys fs.FS, pkgDir string) ([]string, error) {
	var names []string
	err := fs.WalkDir(fsys, pkgDir, func(fullPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(pkgDir, fullPath)
		if err != nil {
			return err
		}
		names = append(names, filepath.ToSlash(rel))
		return nil
	})
	return names, err
}

func dirMatches(fsys fs.FS, pkgDir, pattern string) bool {
	if strings.ContainsAny(pattern, "*?[") {
		return false
	}
	info, err := fs.Stat(fsys, path.Join(pkgDir, pattern))
	return err == nil && info.IsDir()
}

func listDirTree(fsys fs.FS, pkgDir, dir string, all bool) (map[string][]byte, error) {
	root := path.Join(pkgDir, dir)
	files := map[string][]byte{}
	err := fs.WalkDir(fsys, root, func(fullPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(pkgDir, fullPath)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if !all && embedExcluded(name) {
			return nil
		}
		data, err := fs.ReadFile(fsys, fullPath)
		if err != nil {
			return err
		}
		files[name] = data
		return nil
	})
	return files, err
}

func embedExcluded(name string) bool {
	parts := strings.Split(name, "/")
	for _, p := range parts {
		if p == "" {
			continue
		}
		if p[0] == '.' || p[0] == '_' {
			return true
		}
	}
	return false
}

func (interp *Interpreter) applyEmbedValues(pkgName string) error {
	sc := interp.scopes[pkgName]
	if sc == nil {
		return nil
	}
	for _, sym := range sc.sym {
		if sym.kind != varSym || !sym.global || !sym.embedded || !sym.rval.IsValid() {
			continue
		}
		if sym.index < 0 || sym.index >= len(interp.frame.data) {
			continue
		}
		interp.frame.data[sym.index] = sym.rval
	}
	return nil
}
