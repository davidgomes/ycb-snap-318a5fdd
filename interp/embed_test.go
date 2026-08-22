package interp_test

import (
	"bytes"
	"embed"
	"io"
	"io/fs"
	"reflect"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

func newEmbedInterp(t *testing.T, fsys fs.FS) *interp.Interpreter {
	t.Helper()
	var stdout, stderr bytes.Buffer
	i := interp.New(interp.Options{
		Stdout:               &stdout,
		Stderr:               &stderr,
		SourcecodeFilesystem: fsys,
	})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	return i
}

func TestEmbedPackageAvailableWithoutUse(t *testing.T) {
	fsys := fstest.MapFS{
		"main.go": &fstest.MapFile{Data: []byte(`package main

import "embed"

//go:embed hello.txt
var f embed.FS
`)},
		"hello.txt": &fstest.MapFile{Data: []byte("ok")},
	}

	i := interp.New(interp.Options{SourcecodeFilesystem: fsys})
	if _, err := i.EvalPath("main.go"); err != nil {
		t.Fatal(err)
	}
	v, err := i.Eval("f")
	if err != nil {
		t.Fatal(err)
	}
	ef, ok := v.Interface().(embed.FS)
	if !ok {
		t.Fatalf("got %T", v.Interface())
	}
	b, err := ef.ReadFile("hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "ok" {
		t.Fatalf("got %q", b)
	}
}

func TestEmbedString(t *testing.T) {
	fsys := fstest.MapFS{
		"main.go": &fstest.MapFile{Data: []byte(`package main

import (
	_ "embed"
	"fmt"
)

//go:embed hello.txt
var s string

func main() {
	fmt.Print(s)
}
`)},
		"hello.txt": &fstest.MapFile{Data: []byte("hello embed")},
	}

	var stdout bytes.Buffer
	i := interp.New(interp.Options{Stdout: &stdout, SourcecodeFilesystem: fsys})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if _, err := i.EvalPath("main.go"); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "hello embed" {
		t.Fatalf("got %q", got)
	}
}

func TestEmbedBytes(t *testing.T) {
	fsys := fstest.MapFS{
		"main.go": &fstest.MapFile{Data: []byte(`package main

import (
	_ "embed"
	"fmt"
)

//go:embed hello.txt
var b []byte

func main() {
	fmt.Print(string(b))
}
`)},
		"hello.txt": &fstest.MapFile{Data: []byte("bytes embed")},
	}

	var stdout bytes.Buffer
	i := interp.New(interp.Options{Stdout: &stdout, SourcecodeFilesystem: fsys})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if _, err := i.EvalPath("main.go"); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "bytes embed" {
		t.Fatalf("got %q", got)
	}
}

func TestEmbedFS(t *testing.T) {
	fsys := fstest.MapFS{
		"main.go": &fstest.MapFile{Data: []byte(`package main

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed hello.txt data
var content embed.FS

func main() {
	b, err := content.ReadFile("hello.txt")
	if err != nil {
		panic(err)
	}
	fmt.Println(string(b))
	b, err = content.ReadFile("data/a.txt")
	if err != nil {
		panic(err)
	}
	fmt.Println(string(b))

	ents, err := fs.ReadDir(content, "data")
	if err != nil {
		panic(err)
	}
	for _, e := range ents {
		fmt.Println(e.Name())
	}
}
`)},
		"hello.txt":      &fstest.MapFile{Data: []byte("root file")},
		"data/a.txt":     &fstest.MapFile{Data: []byte("nested a")},
		"data/b.txt":     &fstest.MapFile{Data: []byte("nested b")},
		"data/.hidden":   &fstest.MapFile{Data: []byte("secret")},
		"data/_skipped":  &fstest.MapFile{Data: []byte("skip")},
		"data/sub/c.txt": &fstest.MapFile{Data: []byte("deep")},
		"data/sub/.nope": &fstest.MapFile{Data: []byte("no")},
	}

	var stdout bytes.Buffer
	i := interp.New(interp.Options{Stdout: &stdout, SourcecodeFilesystem: fsys})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if _, err := i.EvalPath("main.go"); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(stdout.String())
	want := "root file\nnested a\na.txt\nb.txt\nsub"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestEmbedGroupedVar(t *testing.T) {
	fsys := fstest.MapFS{
		"main.go": &fstest.MapFile{Data: []byte(`package main

import (
	_ "embed"
	"fmt"
)

var (
	//go:embed a.txt
	a string

	//go:embed b.txt
	b string
)

func main() {
	fmt.Print(a, ",", b)
}
`)},
		"a.txt": &fstest.MapFile{Data: []byte("A")},
		"b.txt": &fstest.MapFile{Data: []byte("B")},
	}

	var stdout bytes.Buffer
	i := interp.New(interp.Options{Stdout: &stdout, SourcecodeFilesystem: fsys})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if _, err := i.EvalPath("main.go"); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "A,B" {
		t.Fatalf("got %q", got)
	}
}

func TestEmbedMultipleDirectives(t *testing.T) {
	fsys := fstest.MapFS{
		"main.go": &fstest.MapFile{Data: []byte(`package main

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed one.txt
//go:embed two.txt
var files embed.FS

func main() {
	ents, err := fs.ReadDir(files, ".")
	if err != nil {
		panic(err)
	}
	for _, e := range ents {
		fmt.Println(e.Name())
	}
}
`)},
		"one.txt": &fstest.MapFile{Data: []byte("1")},
		"two.txt": &fstest.MapFile{Data: []byte("2")},
	}

	var stdout bytes.Buffer
	i := interp.New(interp.Options{Stdout: &stdout, SourcecodeFilesystem: fsys})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if _, err := i.EvalPath("main.go"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "one.txt\ntwo.txt" {
		t.Fatalf("got %q", got)
	}
}

func TestEmbedAllPrefix(t *testing.T) {
	fsys := fstest.MapFS{
		"main.go": &fstest.MapFile{Data: []byte(`package main

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed all:hid
var files embed.FS

func main() {
	ents, err := fs.ReadDir(files, "hid")
	if err != nil {
		panic(err)
	}
	for _, e := range ents {
		fmt.Println(e.Name())
	}
}
`)},
		"hid/visible.txt": &fstest.MapFile{Data: []byte("v")},
		"hid/.dot":        &fstest.MapFile{Data: []byte("d")},
		"hid/_under":      &fstest.MapFile{Data: []byte("u")},
	}

	var stdout bytes.Buffer
	i := interp.New(interp.Options{Stdout: &stdout, SourcecodeFilesystem: fsys})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if _, err := i.EvalPath("main.go"); err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	sort.Strings(got)
	want := []string{".dot", "_under", "visible.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestEmbedNoMatch(t *testing.T) {
	fsys := fstest.MapFS{
		"main.go": &fstest.MapFile{Data: []byte(`package main

import _ "embed"

//go:embed missing.txt
var s string

func main() {}
`)},
	}

	i := newEmbedInterp(t, fsys)
	_, err := i.EvalPath("main.go")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no matching files") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEmbedStringMultipleFiles(t *testing.T) {
	fsys := fstest.MapFS{
		"main.go": &fstest.MapFile{Data: []byte(`package main

import _ "embed"

//go:embed *.txt
var s string

func main() {}
`)},
		"a.txt": &fstest.MapFile{Data: []byte("a")},
		"b.txt": &fstest.MapFile{Data: []byte("b")},
	}

	i := newEmbedInterp(t, fsys)
	_, err := i.EvalPath("main.go")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "multiple files") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEmbedAvailableBeforeInit(t *testing.T) {
	fsys := fstest.MapFS{
		"main.go": &fstest.MapFile{Data: []byte(`package main

import (
	_ "embed"
	"fmt"
)

//go:embed hello.txt
var s string

var copied = s

func init() {
	fmt.Print("init:", s, ",", copied)
}

func main() {
	fmt.Print("|main:", s)
}
`)},
		"hello.txt": &fstest.MapFile{Data: []byte("X")},
	}

	var stdout bytes.Buffer
	i := interp.New(interp.Options{Stdout: &stdout, SourcecodeFilesystem: fsys})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if _, err := i.EvalPath("main.go"); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "init:X,X|main:X" {
		t.Fatalf("got %q", got)
	}
}

func TestEmbedFSHostAPI(t *testing.T) {
	fsys := fstest.MapFS{
		"main.go": &fstest.MapFile{Data: []byte(`package main

import "embed"

//go:embed hello.txt dir
var content embed.FS
`)},
		"hello.txt":   &fstest.MapFile{Data: []byte("hello")},
		"dir/a.txt":   &fstest.MapFile{Data: []byte("aa")},
		"dir/b/c.txt": &fstest.MapFile{Data: []byte("cc")},
	}

	i := newEmbedInterp(t, fsys)
	if _, err := i.EvalPath("main.go"); err != nil {
		t.Fatal(err)
	}

	v, err := i.Eval("content")
	if err != nil {
		t.Fatal(err)
	}
	ef, ok := v.Interface().(embed.FS)
	if !ok {
		t.Fatalf("got %T, want embed.FS", v.Interface())
	}

	var _ fs.FS = ef
	var _ fs.ReadFileFS = ef
	var _ fs.ReadDirFS = ef

	b1, err := ef.ReadFile("hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	b2, err := ef.ReadFile("hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(b1) != "hello" || string(b2) != "hello" {
		t.Fatalf("ReadFile: %q %q", b1, b2)
	}
	b1[0] = 'x'
	if string(b2) != "hello" {
		t.Fatal("ReadFile must return an independent copy")
	}

	ents, err := ef.ReadDir("dir")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	if !sort.StringsAreSorted(names) {
		t.Fatalf("ReadDir not sorted: %v", names)
	}

	f, err := ef.Open("dir")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, ok := f.(fs.ReadDirFile); !ok {
		t.Fatal("opened directory does not implement fs.ReadDirFile")
	}
	if _, err := f.(io.Reader).Read(make([]byte, 1)); err == nil {
		// directories may error on Read; that's fine
	}

	if _, err := ef.ReadFile("dir/b/c.txt"); err != nil {
		t.Fatal(err)
	}
}

func TestEmbedRelativeToSourceDir(t *testing.T) {
	fsys := fstest.MapFS{
		"sub/main.go": &fstest.MapFile{Data: []byte(`package main

import (
	_ "embed"
	"fmt"
)

//go:embed local.txt
var s string

func main() {
	fmt.Print(s)
}
`)},
		"sub/local.txt": &fstest.MapFile{Data: []byte("from-sub")},
		"local.txt":     &fstest.MapFile{Data: []byte("from-root")},
	}

	var stdout bytes.Buffer
	i := interp.New(interp.Options{Stdout: &stdout, SourcecodeFilesystem: fsys})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if _, err := i.EvalPath("sub/main.go"); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "from-sub" {
		t.Fatalf("got %q", got)
	}
}

func TestEmbedGlobAndQuoted(t *testing.T) {
	fsys := fstest.MapFS{
		"main.go": &fstest.MapFile{Data: []byte(`package main

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed "file with spaces.txt" *.md
var files embed.FS

func main() {
	ents, err := fs.ReadDir(files, ".")
	if err != nil {
		panic(err)
	}
	for _, e := range ents {
		fmt.Println(e.Name())
	}
}
`)},
		"file with spaces.txt": &fstest.MapFile{Data: []byte("sp")},
		"readme.md":            &fstest.MapFile{Data: []byte("md")},
	}

	var stdout bytes.Buffer
	i := interp.New(interp.Options{Stdout: &stdout, SourcecodeFilesystem: fsys})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if _, err := i.EvalPath("main.go"); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(stdout.String())
	if !strings.Contains(got, "file with spaces.txt") || !strings.Contains(got, "readme.md") {
		t.Fatalf("got %q", got)
	}
}

func TestEmbedPathMatch(t *testing.T) {
	// path.Match: * does not cross directories.
	fsys := fstest.MapFS{
		"main.go": &fstest.MapFile{Data: []byte(`package main

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed *.txt
var files embed.FS

func main() {
	ents, err := fs.ReadDir(files, ".")
	if err != nil {
		panic(err)
	}
	for _, e := range ents {
		fmt.Println(e.Name())
	}
}
`)},
		"top.txt":      &fstest.MapFile{Data: []byte("t")},
		"nested/x.txt": &fstest.MapFile{Data: []byte("n")},
	}

	var stdout bytes.Buffer
	i := interp.New(interp.Options{Stdout: &stdout, SourcecodeFilesystem: fsys})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if _, err := i.EvalPath("main.go"); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(stdout.String())
	if got != "top.txt" {
		t.Fatalf("got %q, * should not match nested files", got)
	}
}

func TestEmbedGlobIncludesHiddenFiles(t *testing.T) {
	// A pattern that names files directly (dir/*) includes names starting with
	// '.' or '_'. Walking the directory itself does not.
	fsys := fstest.MapFS{
		"main.go": &fstest.MapFile{Data: []byte(`package main

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed hid/*
var files embed.FS

func main() {
	ents, err := fs.ReadDir(files, "hid")
	if err != nil {
		panic(err)
	}
	for _, e := range ents {
		fmt.Println(e.Name())
	}
}
`)},
		"hid/visible.txt": &fstest.MapFile{Data: []byte("v")},
		"hid/.dot":        &fstest.MapFile{Data: []byte("d")},
	}

	var stdout bytes.Buffer
	i := interp.New(interp.Options{Stdout: &stdout, SourcecodeFilesystem: fsys})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if _, err := i.EvalPath("main.go"); err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	sort.Strings(got)
	want := []string{".dot", "visible.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestEmbedIgnoresInitializer(t *testing.T) {
	fsys := fstest.MapFS{
		"main.go": &fstest.MapFile{Data: []byte(`package main

import (
	_ "embed"
	"fmt"
)

//go:embed hello.txt
var s string = "OVERWRITE"

func main() {
	fmt.Print(s)
}
`)},
		"hello.txt": &fstest.MapFile{Data: []byte("kept")},
	}

	var stdout bytes.Buffer
	i := interp.New(interp.Options{Stdout: &stdout, SourcecodeFilesystem: fsys})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if _, err := i.EvalPath("main.go"); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "kept" {
		t.Fatalf("initializer overwrote embed: got %q", got)
	}
}

func TestEmbedDoesNotOverwriteAfterInit(t *testing.T) {
	// A later interpreted statement must still see the embed value.
	fsys := fstest.MapFS{
		"hello.txt": &fstest.MapFile{Data: []byte("stay")},
	}
	i := interp.New(interp.Options{SourcecodeFilesystem: fsys})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	src := `
package main
import _ "embed"
//go:embed hello.txt
var s string
`
	if _, err := i.Eval(src); err != nil {
		t.Fatal(err)
	}
	v, err := i.Eval("s")
	if err != nil {
		t.Fatal(err)
	}
	if v.String() != "stay" {
		t.Fatalf("got %q", v.String())
	}
}
