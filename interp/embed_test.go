package interp_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
	yembed "github.com/traefik/yaegi/stdlib/embed"
)

func runEmbed(t *testing.T, files fstest.MapFS, path string, useStdlib bool) (*interp.Interpreter, string, error) {
	t.Helper()
	var stdout bytes.Buffer
	i := interp.New(interp.Options{SourcecodeFilesystem: files, Stdout: &stdout})
	if useStdlib {
		if err := i.Use(stdlib.Symbols); err != nil {
			t.Fatal(err)
		}
	}
	_, err := i.EvalPath(path)
	return i, stdout.String(), err
}

func TestEmbedStringAndBytes(t *testing.T) {
	src := `package main
import _ "embed"

//go:embed hello.txt
var s string

//go:embed hello.txt
var b []byte

//go:embed empty.txt
var empty []byte

type S string
type B []byte
type MB byte

//go:embed hello.txt
var ns S

//go:embed hello.txt
var nb B

//go:embed hello.txt
var nmb []MB

//go:embed hello.txt
//go:embed hello.txt
var dup string

var n = len(s)

func init() {
	if n != len("hello\n") {
		panic(n)
	}
	b[0] = 'H'
}

func main() {
	println(s)
	println(string(b))
	println(empty == nil)
	println(len(empty))
	println(ns)
	println(string(nb))
	println(string(nmb))
	println(dup)
	println(n)
}
`
	files := fstest.MapFS{
		"p/main.go":   {Data: []byte(src)},
		"p/hello.txt": {Data: []byte("hello\n")},
		"p/empty.txt": {Data: []byte{}},
		"hello.txt":   {Data: []byte("wrong")},
	}
	i, out, err := runEmbed(t, files, "p/main.go", false)
	if err != nil {
		t.Fatal(err)
	}
	want := "hello\n\nHello\n\nfalse\n0\nhello\n\nhello\n\nhello\n\nhello\n\n6\n"
	if out != want {
		t.Fatalf("stdout:\n%q\nwant:\n%q", out, want)
	}
	g := i.Globals()
	if g["s"].String() != "hello\n" {
		t.Fatalf("global s = %q", g["s"].String())
	}
	if string(g["b"].Bytes()) != "Hello\n" {
		t.Fatalf("global b = %q", g["b"].Bytes())
	}
	if g["empty"].IsNil() || g["empty"].Len() != 0 {
		t.Fatalf("empty = %#v", g["empty"])
	}
}

func TestEmbedGroupedAndQuoted(t *testing.T) {
	src := `package main
import _ "embed"

var (
	//go:embed "my file.txt"
	// comment kept with the directive
	q string

	//go:embed dir
	one string
)

//go:embed "my file.txt"

// still attached across a blank line
var blank string

func main() {
	println(q)
	println(one)
	println(blank)
}
`
	files := fstest.MapFS{
		"p/main.go":      {Data: []byte(src)},
		"p/my file.txt":  {Data: []byte("spaced")},
		"p/dir/only.txt": {Data: []byte("world\n")},
	}
	_, out, err := runEmbed(t, files, "p/main.go", false)
	if err != nil {
		t.Fatal(err)
	}
	if out != "spaced\nworld\n\nspaced\n" {
		t.Fatalf("stdout %q", out)
	}
}

func TestEmbedFSPatterns(t *testing.T) {
	src := `package main

import (
	"embed"
	"io/fs"
)

//go:embed image/*
var star embed.FS

//go:embed image
var tree embed.FS

//go:embed all:image
var all embed.FS

//go:embed .hidden
var dot string

//go:embed *
var everything embed.FS

type F = embed.FS

//go:embed hello.txt
var alias F

func has(f embed.FS, name string) {
	_, err := f.ReadFile(name)
	if err != nil {
		println("missing", name)
		return
	}
	println("have", name)
}

func list(label string, f embed.FS, dir string) {
	ents, err := fs.ReadDir(f, dir)
	if err != nil {
		println(label, err.Error())
		return
	}
	println(label, len(ents))
	for _, e := range ents {
		kind := "file"
		if e.IsDir() {
			kind = "dir"
		}
		println(e.Name(), kind)
	}
}

func main() {
	has(star, "image/.temp")
	has(star, "image/a.png")
	has(star, "image/dir/keep.txt")
	has(star, "image/dir/.tempfile")
	has(tree, "image/.temp")
	has(tree, "image/a.png")
	has(tree, "image/dir/keep.txt")
	has(tree, "image/dir/.tempfile")
	has(all, "image/.temp")
	has(all, "image/dir/.tempfile")
	has(everything, ".hidden")
	has(everything, "_hidden")
	has(everything, "image/.temp")
	has(everything, "image/dir/.tempfile")
	has(everything, "hello.txt")
	println(dot)
	list("star", star, "image")
	list("tree", tree, "image")
	b1, _ := alias.ReadFile("hello.txt")
	b1[0] = 'X'
	b2, _ := alias.ReadFile("hello.txt")
	println(string(b1), string(b2))
	file, err := tree.Open("image")
	if err != nil {
		println(err.Error())
		return
	}
	if _, ok := file.(fs.ReadDirFile); !ok {
		println("not readdirfile")
		return
	}
	println("readdirfile")
}
`
	files := fstest.MapFS{
		"p/main.go":             {Data: []byte(src)},
		"p/hello.txt":           {Data: []byte("hello")},
		"p/.hidden":             {Data: []byte("dot")},
		"p/_hidden":             {Data: []byte("under")},
		"p/image/a.png":         {Data: []byte("png")},
		"p/image/.temp":         {Data: []byte("temp")},
		"p/image/dir/keep.txt":  {Data: []byte("keep")},
		"p/image/dir/.tempfile": {Data: []byte("hid")},
	}
	i, out, err := runEmbed(t, files, "p/main.go", true)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"have image/.temp",
		"have image/a.png",
		"have image/dir/keep.txt",
		"missing image/dir/.tempfile",
		"missing image/.temp",
		"have image/a.png",
		"have image/dir/keep.txt",
		"missing image/dir/.tempfile",
		"have image/.temp",
		"have image/dir/.tempfile",
		"have .hidden",
		"have _hidden",
		"missing image/.temp",
		"missing image/dir/.tempfile",
		"have hello.txt",
		"dot",
		"star 3",
		".temp file",
		"a.png file",
		"dir dir",
		"tree 2",
		"a.png file",
		"dir dir",
		"Xello hello",
		"readdirfile",
		"",
	}, "\n")
	if out != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", out, want)
	}
	fsys := i.Globals()["star"].Interface().(yembed.FS)
	b1, err := fsys.ReadFile("image/a.png")
	if err != nil || string(b1) != "png" {
		t.Fatalf("host ReadFile = %q %v", b1, err)
	}
	b1[0] = 'X'
	b2, err := fsys.ReadFile("image/a.png")
	if err != nil || string(b2) != "png" {
		t.Fatalf("host copy = %q %v", b2, err)
	}
}

func TestEmbedErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
		file map[string]string
		dir  map[string]bool
	}{
		{
			name: "no import",
			src: `package main
//go:embed hello.txt
var s string
func main() {}
`,
			want: `go:embed only allowed in Go files that import "embed"`,
			file: map[string]string{"hello.txt": "x"},
		},
		{
			name: "no match",
			src: `package main
import _ "embed"
//go:embed missing.txt
var s string
func main() {}
`,
			want: "pattern missing.txt: no matching files found",
		},
		{
			name: "multiple files",
			src: `package main
import _ "embed"
//go:embed a.txt b.txt
var s string
func main() {}
`,
			want: "invalid go:embed: multiple files for type string",
			file: map[string]string{"a.txt": "a", "b.txt": "b"},
		},
		{
			name: "multiple named",
			src: `package main
import _ "embed"
type S string
//go:embed a.txt b.txt
var s S
func main() {}
`,
			want: "invalid go:embed: multiple files for type S",
			file: map[string]string{"a.txt": "a", "b.txt": "b"},
		},
		{
			name: "multiple bytes",
			src: `package main
import _ "embed"
//go:embed a.txt b.txt
var s []byte
func main() {}
`,
			want: "invalid go:embed: multiple files for type []byte",
			file: map[string]string{"a.txt": "a", "b.txt": "b"},
		},
		{
			name: "inside func",
			src: `package main
import _ "embed"
func main() {
//go:embed hello.txt
var s string
println(s)
}
`,
			want: "go:embed cannot apply to var inside func",
			file: map[string]string{"hello.txt": "x"},
		},
		{
			name: "initializer",
			src: `package main
import _ "embed"
//go:embed hello.txt
var s = "x"
func main() {}
`,
			want: "go:embed cannot apply to var with initializer",
			file: map[string]string{"hello.txt": "x"},
		},
		{
			name: "multiple vars",
			src: `package main
import _ "embed"
//go:embed hello.txt
var s, t string
func main() {}
`,
			want: "go:embed cannot apply to multiple vars",
			file: map[string]string{"hello.txt": "x"},
		},
		{
			name: "wrong type",
			src: `package main
import _ "embed"
//go:embed hello.txt
var s int
func main() {}
`,
			want: "go:embed cannot apply to var of type int",
			file: map[string]string{"hello.txt": "x"},
		},
		{
			name: "defined fs",
			src: `package main
import "embed"
type F embed.FS
//go:embed hello.txt
var f F
func main() {}
`,
			want: "go:embed cannot apply to var of type F",
			file: map[string]string{"hello.txt": "x"},
		},
		{
			name: "before group",
			src: `package main
import _ "embed"
//go:embed hello.txt
var (
s string
)
func main() {}
`,
			want: "misplaced go:embed directive",
			file: map[string]string{"hello.txt": "x"},
		},
		{
			name: "on func",
			src: `package main
import _ "embed"
//go:embed hello.txt
func main() {}
`,
			want: "misplaced go:embed directive",
			file: map[string]string{"hello.txt": "x"},
		},
		{
			name: "trailing",
			src: `package main
import _ "embed"
var s string //go:embed hello.txt
func main() {}
`,
			want: "misplaced compiler directive",
			file: map[string]string{"hello.txt": "x"},
		},
		{
			name: "empty dir",
			src: `package main
import "embed"
//go:embed *
var f embed.FS
func main() {}
`,
			want: "pattern *: cannot embed directory emptydir: contains no embeddable files",
			file: map[string]string{"a.txt": "a"},
			dir:  map[string]bool{"emptydir": true},
		},
		{
			name: "dot pattern",
			src: `package main
import _ "embed"
//go:embed .
var s string
func main() {}
`,
			want: "pattern .: invalid pattern syntax",
		},
		{
			name: "parent pattern",
			src: `package main
import _ "embed"
//go:embed ../hello.txt
var s string
func main() {}
`,
			want: "pattern ../hello.txt: invalid pattern syntax",
		},
		{
			name: "usage",
			src: `package main
import _ "embed"
//go:embed
var s string
func main() {}
`,
			want: "usage: //go:embed pattern...",
		},
		{
			name: "bad name",
			src: `package main
import _ "embed"
//go:embed a:b.txt
var s string
func main() {}
`,
			want: "pattern a:b.txt: cannot embed file a:b.txt: invalid name a:b.txt",
			file: map[string]string{"a:b.txt": "x"},
		},
		{
			name: "git dir",
			src: `package main
import "embed"
//go:embed .git
var f embed.FS
func main() {}
`,
			want: "pattern .git: cannot embed directory .git: invalid name .git",
			file: map[string]string{".git/config": "x"},
		},
		{
			name: "star git",
			src: `package main
import "embed"
//go:embed *
var f embed.FS
func main() {}
`,
			want: "pattern *: cannot embed directory .git: invalid name .git",
			file: map[string]string{"a.txt": "a", ".git/config": "x"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := fstest.MapFS{"p/main.go": {Data: []byte(tc.src)}}
			for name, data := range tc.file {
				files["p/"+name] = &fstest.MapFile{Data: []byte(data)}
			}
			for name := range tc.dir {
				files["p/"+name] = &fstest.MapFile{Mode: fs.ModeDir}
			}
			_, _, err := runEmbed(t, files, "p/main.go", true)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestEmbedSymlink(t *testing.T) {
	files := fstest.MapFS{
		"p/main.go": {Data: []byte(`package main
import _ "embed"
//go:embed link.txt
var s string
func main() { println(s) }
`)},
		"p/a.txt":    {Data: []byte("target")},
		"p/link.txt": {Data: []byte("a.txt"), Mode: fs.ModeSymlink},
	}
	_, _, err := runEmbed(t, files, "p/main.go", false)
	if err == nil || !strings.Contains(err.Error(), "pattern link.txt: cannot embed irregular file link.txt") {
		t.Fatalf("got %v", err)
	}
}

func TestEmbedImportSrc(t *testing.T) {
	root := t.TempDir()
	lib := filepath.Join(root, "src", "lib")
	app := filepath.Join(root, "src", "app")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "hello.txt"), []byte("fromlib"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "lib.go"), []byte(`package lib
import _ "embed"
//go:embed hello.txt
var Msg string
func init() {
	if Msg != "fromlib" {
		panic(Msg)
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "main.go"), []byte(`package main
import "lib"
func main() { println(lib.Msg) }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	i := interp.New(interp.Options{GoPath: root, Stdout: &stdout})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if _, err := i.EvalPath(filepath.Join(app, "main.go")); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "fromlib\n" {
		t.Fatalf("stdout %q", stdout.String())
	}
}
