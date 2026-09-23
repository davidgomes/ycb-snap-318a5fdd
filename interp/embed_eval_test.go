package interp_test

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

func evalEmbed(t *testing.T, fsys fstest.MapFS, path string) (string, error) {
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
	if _, err := i.EvalPath(path); err != nil {
		return stdout.String(), err
	}
	return stdout.String(), nil
}

func TestEmbedStringAndBytes(t *testing.T) {
	src := `package main

import _ "embed"

var before = len(msg)

//go:embed hello.txt
var msg string

type S string
//go:embed hello.txt
var named S

//go:embed "my file.txt"
var spaced string

//go:embed data.bin
var raw []byte

var after = msg

func init() {
	if msg != "hello" || after != "hello" || before != 5 {
		panic("init saw " + msg)
	}
}

func main() {
	println(before)
	println(msg)
	println(after)
	println(named)
	println(spaced)
	println(len(raw))
	println(raw[0], raw[1], raw[2])
	raw[0] = 1
	println(raw[0])
}
`
	fsys := fstest.MapFS{
		"main.go":     {Data: []byte(src)},
		"hello.txt":   {Data: []byte("hello")},
		"my file.txt": {Data: []byte("sp ace")},
		"data.bin":    {Data: []byte{0xff, 0x00, 0x01}},
	}
	out, err := evalEmbed(t, fsys, "main.go")
	if err != nil {
		t.Fatal(err)
	}
	want := "5\nhello\nhello\nhello\nsp ace\n3\n255 0 1\n1\n"
	if out != want {
		t.Fatalf("output %q, want %q", out, want)
	}
}

func TestEmbedGroupedAndSubdir(t *testing.T) {
	src := `package main

import _ "embed"

var (
	//go:embed a.txt

	a string

	//go:embed data.bin
	b []byte
)

//go:embed hello.txt
var (
	nested string
)

func main() {
	println(a)
	println(string(b))
	println(nested)
}
`
	fsys := fstest.MapFS{
		"pkg/main.go":   {Data: []byte(src)},
		"pkg/a.txt":     {Data: []byte("A")},
		"pkg/b.txt":     {Data: []byte("B")},
		"pkg/data.bin":  {Data: []byte("BIN")},
		"pkg/hello.txt": {Data: []byte("frompkg")},
	}
	out, err := evalEmbed(t, fsys, "pkg/main.go")
	if err != nil {
		t.Fatal(err)
	}
	want := "A\nBIN\nfrompkg\n"
	if out != want {
		t.Fatalf("output %q, want %q", out, want)
	}
}

func TestEmbedFS(t *testing.T) {
	src := `package main

import (
	"embed"
	"io/fs"
)

//go:embed a.txt
//go:embed *
var content embed.FS

func main() {
	b, err := content.ReadFile("a.txt")
	if err != nil {
		panic(err)
	}
	println(string(b))
	b[0] = 'X'
	b2, err := content.ReadFile("a.txt")
	if err != nil {
		panic(err)
	}
	println(string(b2))

	ents, err := content.ReadDir("sub")
	if err != nil {
		panic(err)
	}
	for _, e := range ents {
		println(e.Name())
	}

	f, err := content.Open("sub")
	if err != nil {
		panic(err)
	}
	d, ok := f.(fs.ReadDirFile)
	if !ok {
		panic("not ReadDirFile")
	}
	ents, err = d.ReadDir(-1)
	if err != nil {
		panic(err)
	}
	for _, e := range ents {
		println("open", e.Name())
	}

	top, err := content.ReadDir(".")
	if err != nil {
		panic(err)
	}
	for _, e := range top {
		println("top", e.Name(), e.IsDir())
	}

	hidden, err := fs.ReadFile(content, "sub/.hid")
	if err == nil {
		panic("hidden file visible: " + string(hidden))
	}
	allb, err := fs.ReadFile(content, "root.dot")
	if err != nil {
		panic(err)
	}
	println("dot", string(allb))
}
`
	fsys := fstest.MapFS{
		"main.go":   {Data: []byte(src)},
		"a.txt":     {Data: []byte("alpha")},
		"root.dot":  {Data: []byte("DOT")},
		".hidden":   {Data: []byte("NO")},
		"_skip":     {Data: []byte("NO")},
		"sub/b.txt": {Data: []byte("b")},
		"sub/a.txt": {Data: []byte("a")},
		"sub/.hid":  {Data: []byte("H")},
		"sub/_skip": {Data: []byte("S")},
	}
	out, err := evalEmbed(t, fsys, "main.go")
	if err != nil {
		t.Fatal(err)
	}
	want := "alpha\nalpha\na.txt\nb.txt\nopen a.txt\nopen b.txt\ntop .hidden false\ntop _skip false\ntop a.txt false\ntop main.go false\ntop root.dot false\ntop sub true\ndot DOT\n"
	if out != want {
		t.Fatalf("output %q, want %q", out, want)
	}
}

func TestEmbedAllPrefix(t *testing.T) {
	src := `package main

import "embed"

//go:embed all:sub
var content embed.FS

func main() {
	ents, err := content.ReadDir("sub")
	if err != nil {
		panic(err)
	}
	for _, e := range ents {
		println(e.Name())
	}
	b, err := content.ReadFile("sub/.hid")
	if err != nil {
		panic(err)
	}
	println(string(b))
}
`
	fsys := fstest.MapFS{
		"main.go":   {Data: []byte(src)},
		"sub/b.txt": {Data: []byte("b")},
		"sub/.hid":  {Data: []byte("H")},
		"sub/_skip": {Data: []byte("S")},
	}
	out, err := evalEmbed(t, fsys, "main.go")
	if err != nil {
		t.Fatal(err)
	}
	want := ".hid\n_skip\nb.txt\nH\n"
	if out != want {
		t.Fatalf("output %q, want %q", out, want)
	}
}

func TestEmbedErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "missing",
			src: `package main
import _ "embed"
//go:embed missing.txt
var s string
func main() {}
`,
			want: "no matching files found",
		},
		{
			name: "multiple files",
			src: `package main
import _ "embed"
//go:embed a.txt b.txt
var s string
func main() {}
`,
			want: "multiple files",
		},
		{
			name: "inside func",
			src: `package main
import _ "embed"
func main() {
//go:embed a.txt
var s string
}
`,
			want: "inside func",
		},
		{
			name: "initializer",
			src: `package main
import _ "embed"
//go:embed a.txt
var s string = "x"
func main() {}
`,
			want: "initializer",
		},
		{
			name: "multiple vars",
			src: `package main
import _ "embed"
//go:embed a.txt
var s, t string
func main() {}
`,
			want: "multiple vars",
		},
		{
			name: "wrong type",
			src: `package main
import _ "embed"
//go:embed a.txt
var s int
func main() {}
`,
			want: "cannot apply to var of type",
		},
		{
			name: "no import",
			src: `package main
//go:embed a.txt
var s string
func main() {}
`,
			want: `import "embed"`,
		},
		{
			name: "empty dir",
			src: `package main
import "embed"
//go:embed empty
var s embed.FS
func main() {}
`,
			want: "contains no embeddable files",
		},
		{
			name: "bad pattern",
			src: `package main
import "embed"
//go:embed .
var s embed.FS
func main() {}
`,
			want: "invalid pattern syntax",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fsys := fstest.MapFS{
				"main.go": {Data: []byte(tc.src)},
				"a.txt":   {Data: []byte("A")},
				"b.txt":   {Data: []byte("B")},
			}
			if tc.name == "empty dir" {
				fsys["empty/.keep"] = &fstest.MapFile{Data: []byte("x")}
				// Replace the pattern target with a directory whose only
				// entries are excluded, which is empty for embedding.
				tc.src = strings.Replace(tc.src, "empty", "blank", 1)
				fsys["main.go"] = &fstest.MapFile{Data: []byte(strings.Replace(string(fsys["main.go"].Data), "//go:embed empty", "//go:embed blank", 1))}
				fsys["blank/.hid"] = &fstest.MapFile{Data: []byte("h")}
				fsys["blank/_x"] = &fstest.MapFile{Data: []byte("x")}
			}
			_, err := evalEmbed(t, fsys, "main.go")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want substring %q", err, tc.want)
			}
		})
	}
}
