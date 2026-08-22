package interp_test

import (
	"testing"
	"testing/fstest"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

func testEmbedFS(t *testing.T) fstest.MapFS {
	t.Helper()
	return fstest.MapFS{
		"main.go": &fstest.MapFile{
			Data: []byte(`package main

import (
	_ "embed"
	"embed"
)

//go:embed hello.txt
var hello string

//go:embed data.bin
var data []byte

//go:embed templates
var templates embed.FS

func main() {
	if hello != "hello world\n" {
		panic("bad string embed")
	}
	if string(data) != "\x01\x02" {
		panic("bad byte embed")
	}
	content, err := templates.ReadFile("templates/page.html")
	if err != nil || string(content) != "<html></html>\n" {
		panic("bad fs embed")
	}
}
`),
		},
		"hello.txt":              &fstest.MapFile{Data: []byte("hello world\n")},
		"data.bin":               &fstest.MapFile{Data: []byte{1, 2}},
		"templates/page.html":    &fstest.MapFile{Data: []byte("<html></html>\n")},
		"templates/_hidden.html": &fstest.MapFile{Data: []byte("hidden")},
	}
}

func TestGoEmbedString(t *testing.T) {
	fsys := fstest.MapFS{
		"main.go": &fstest.MapFile{
			Data: []byte(`package main

import _ "embed"

//go:embed hello.txt
var hello string

func main() {
	if hello != "hello world\n" {
		panic("bad string embed")
	}
}
`),
		},
		"hello.txt": &fstest.MapFile{Data: []byte("hello world\n")},
	}
	i := interp.New(interp.Options{SourcecodeFilesystem: fsys})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if _, err := i.EvalPath("main.go"); err != nil {
		t.Fatal(err)
	}
}

func TestGoEmbed(t *testing.T) {
	i := interp.New(interp.Options{SourcecodeFilesystem: testEmbedFS(t)})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if _, err := i.EvalPath("main.go"); err != nil {
		t.Fatal(err)
	}
}

func TestGoEmbedGroupedVar(t *testing.T) {
	fsys := fstest.MapFS{
		"main.go": &fstest.MapFile{
			Data: []byte(`package main

import _ "embed"

var (
	//go:embed msg.txt
	s string
)

func main() {
	if s != "grouped" {
		panic("bad grouped embed")
	}
}
`),
		},
		"msg.txt": &fstest.MapFile{Data: []byte("grouped")},
	}
	i := interp.New(interp.Options{SourcecodeFilesystem: fsys})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if _, err := i.EvalPath("main.go"); err != nil {
		t.Fatal(err)
	}
}

func TestGoEmbedFSReadDir(t *testing.T) {
	fsys := fstest.MapFS{
		"main.go": &fstest.MapFile{
			Data: []byte(`package main

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed files
var content embed.FS

func main() {
	entries, err := content.ReadDir("files")
	if err != nil {
		panic(err)
	}
	if len(entries) != 2 {
		panic(fmt.Sprintf("want 2 entries, got %d", len(entries)))
	}
	if entries[0].Name() != "a.txt" || entries[1].Name() != "b.txt" {
		panic("entries not sorted")
	}
	dir, err := content.Open("files")
	if err != nil {
		panic(err)
	}
	if _, ok := dir.(fs.ReadDirFile); !ok {
		panic("Open did not return ReadDirFile")
	}
	_ = dir.Close()
}
`),
		},
		"files/a.txt": &fstest.MapFile{Data: []byte("a")},
		"files/b.txt": &fstest.MapFile{Data: []byte("b")},
	}
	i := interp.New(interp.Options{SourcecodeFilesystem: fsys})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if _, err := i.EvalPath("main.go"); err != nil {
		t.Fatal(err)
	}
}

func TestGoEmbedReadFileCopy(t *testing.T) {
	fsys := fstest.MapFS{
		"main.go": &fstest.MapFile{
			Data: []byte(`package main

import "embed"

//go:embed copy.txt
var content embed.FS

func main() {
	a, _ := content.ReadFile("copy.txt")
	b, _ := content.ReadFile("copy.txt")
	a[0] = 'X'
	if b[0] != 'A' {
		panic("ReadFile did not return independent copy")
	}
}
`),
		},
		"copy.txt": &fstest.MapFile{Data: []byte("ABC")},
	}
	i := interp.New(interp.Options{SourcecodeFilesystem: fsys})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if _, err := i.EvalPath("main.go"); err != nil {
		t.Fatal(err)
	}
}

func TestGoEmbedNoMatch(t *testing.T) {
	fsys := fstest.MapFS{
		"main.go": &fstest.MapFile{
			Data: []byte(`package main

import _ "embed"

//go:embed missing.txt
var s string

func main() {}
`),
		},
	}
	i := interp.New(interp.Options{SourcecodeFilesystem: fsys})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if _, err := i.EvalPath("main.go"); err == nil {
		t.Fatal("expected error for unmatched embed pattern")
	}
}
