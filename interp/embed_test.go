package interp_test

import (
	"embed"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

func newEmbedInterp(t *testing.T, fsys fs.FS) *interp.Interpreter {
	t.Helper()
	i := interp.New(interp.Options{SourcecodeFilesystem: fsys, GoPath: "/go"})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	return i
}

func TestEmbedFS(t *testing.T) {
	fsys := fstest.MapFS{
		"main.go": &fstest.MapFile{Data: []byte(`package main

import "embed"

var (
	//go:embed "data/b.txt" data/a.txt data/z
	Files embed.FS

	//go:embed data/a.txt
	A []byte
)

func main() {}
`)},
		"data/a.txt":   &fstest.MapFile{Data: []byte("a")},
		"data/b.txt":   &fstest.MapFile{Data: []byte("b")},
		"data/z/c.txt": &fstest.MapFile{Data: []byte("c")},
		"data/z/.d":    &fstest.MapFile{Data: []byte("d")},
	}
	i := newEmbedInterp(t, fsys)
	if _, err := i.EvalPath("main.go"); err != nil {
		t.Fatal(err)
	}

	v, err := i.Eval("Files")
	if err != nil {
		t.Fatal(err)
	}
	files, ok := v.Interface().(embed.FS)
	if !ok {
		t.Fatalf("got %T, want embed.FS", v.Interface())
	}
	if err := fstest.TestFS(files, "data/a.txt", "data/b.txt", "data/z/c.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Open("data/z/.d"); err == nil {
		t.Error("hidden file should not be embedded")
	}

	entries, err := files.ReadDir("data")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if got := strings.Join(names, ","); got != "a.txt,b.txt,z" {
		t.Errorf("got entries %s, want a.txt,b.txt,z", got)
	}

	v, err = i.Eval("string(A)")
	if err != nil {
		t.Fatal(err)
	}
	if got := v.String(); got != "a" {
		t.Errorf("got %q, want %q", got, "a")
	}
}

func TestEmbedImportedPackage(t *testing.T) {
	fsys := fstest.MapFS{
		"main.go": &fstest.MapFile{Data: []byte(`package main

import "guthib.com/foo"

var Msg = foo.Msg

func main() {}
`)},
		"_pkg/src/guthib.com/foo/foo.go": &fstest.MapFile{Data: []byte(`package foo

import _ "embed"

var Msg = "hello " + msg

//go:embed msg.txt
var msg string
`)},
		"_pkg/src/guthib.com/foo/msg.txt": &fstest.MapFile{Data: []byte("world")},
	}
	i := interp.New(interp.Options{SourcecodeFilesystem: fsys, GoPath: "./_pkg"})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if _, err := i.EvalPath("main.go"); err != nil {
		t.Fatal(err)
	}
	v, err := i.Eval("Msg")
	if err != nil {
		t.Fatal(err)
	}
	if got := v.String(); got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestEmbedEval(t *testing.T) {
	fsys := fstest.MapFS{"hello.txt": &fstest.MapFile{Data: []byte("hello")}}
	i := newEmbedInterp(t, fsys)
	if _, err := i.Eval("import _ \"embed\"\n//go:embed hello.txt\nvar s string"); err != nil {
		t.Fatal(err)
	}
	v, err := i.Eval("s")
	if err != nil {
		t.Fatal(err)
	}
	if got := v.String(); got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestEmbedErrors(t *testing.T) {
	testCases := []struct {
		desc string
		src  string
		err  string
	}{
		{
			desc: "no matching files",
			src:  "//go:embed nope.txt\nvar s string",
			err:  "6:1: pattern nope.txt: no matching files found",
		},
		{
			desc: "multiple files for string",
			src:  "//go:embed a.txt b.txt\nvar s string",
			err:  "7:5: invalid go:embed: multiple files for type string",
		},
		{
			desc: "multiple files for bytes",
			src:  "//go:embed *.txt\nvar s []byte",
			err:  "7:5: invalid go:embed: multiple files for type []uint8",
		},
		{
			desc: "invalid type",
			src:  "//go:embed a.txt\nvar s int",
			err:  "7:5: go:embed cannot apply to var of type int",
		},
		{
			desc: "initializer",
			src:  "//go:embed a.txt\nvar s = \"x\"",
			err:  "7:5: go:embed cannot apply to var with initializer",
		},
		{
			desc: "multiple vars",
			src:  "//go:embed a.txt\nvar s, t string",
			err:  "7:5: go:embed cannot apply to multiple vars",
		},
		{
			desc: "misplaced",
			src:  "//go:embed a.txt\nfunc f() {}",
			err:  "6:1: misplaced go:embed directive",
		},
		{
			desc: "invalid pattern",
			src:  "//go:embed ../a.txt\nvar s string",
			err:  "6:1: pattern ../a.txt: invalid pattern syntax",
		},
		{
			desc: "no embeddable files",
			src:  "//go:embed hidden\nvar s embed.FS",
			err:  "6:1: pattern hidden: cannot embed directory hidden: contains no embeddable files",
		},
		{
			desc: "missing pattern",
			src:  "//go:embed\nvar s string",
			err:  "6:1: usage: //go:embed pattern...",
		},
		{
			desc: "invalid quoted string",
			src:  "//go:embed \"a.txt\nvar s string",
			err:  "6:1: invalid quoted string in //go:embed: \"a.txt",
		},
	}

	for _, test := range testCases {
		t.Run(test.desc, func(t *testing.T) {
			src := "package main\n\nimport \"embed\"\n\nvar _ embed.FS\n" + test.src + "\n\nfunc main() {}\n"
			fsys := fstest.MapFS{
				"main.go":        &fstest.MapFile{Data: []byte(src)},
				"a.txt":          &fstest.MapFile{Data: []byte("a")},
				"b.txt":          &fstest.MapFile{Data: []byte("b")},
				"hidden/.hidden": &fstest.MapFile{Data: []byte("h")},
			}
			i := newEmbedInterp(t, fsys)
			_, err := i.EvalPath("main.go")
			if err == nil {
				t.Fatalf("got no error, want %q", test.err)
			}
			if got := err.Error(); got != "main.go:"+test.err {
				t.Errorf("got %q, want %q", got, "main.go:"+test.err)
			}
		})
	}
}
