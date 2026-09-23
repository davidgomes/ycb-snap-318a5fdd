package interp_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

func TestGoEmbed(t *testing.T) {
	src := `package main

import "embed"

//go:embed hello.txt
var s string

//go:embed hello.txt
var b []byte

//go:embed hello.txt
//go:embed sub
var f embed.FS

//go:embed all:hidden
var hall embed.FS

var echoed = s + "!"

func init() {
	if s != "hello\n" {
		panic("init saw " + s)
	}
}

func main() {
	if string(b) != s {
		panic("bytes")
	}
	if echoed != "hello\n!" {
		panic(echoed)
	}
	data, err := f.ReadFile("hello.txt")
	if err != nil || string(data) != "hello\n" {
		panic(err)
	}
	data[0] = 'X'
	data2, err := f.ReadFile("hello.txt")
	if err != nil || string(data2) != "hello\n" || data2[0] == 'X' {
		panic("aliased read")
	}
	sub, err := f.ReadFile("sub/a.txt")
	if err != nil || string(sub) != "A" {
		panic(sub)
	}
	_, err = f.ReadFile("sub/.secret")
	if err == nil {
		panic("secret visible")
	}
	_, err = f.ReadFile("sub/_skip.txt")
	if err == nil {
		panic("skip visible")
	}
	ents, err := f.ReadDir("sub")
	if err != nil {
		panic(err)
	}
	if len(ents) != 2 || ents[0].Name() != "a.txt" || ents[1].Name() != "b.txt" {
		panic(ents)
	}
	h, err := hall.ReadFile("hidden/.dot")
	if err != nil || string(h) != "dot" {
		panic(h)
	}
	print("ok")
}
`
	// The interpreted source imports fs only if we add it. The type assert uses fs.DirEntry
	// which requires importing io/fs. Add the import.
	fsys := fstest.MapFS{
		"p/main.go":       {Data: []byte(src)},
		"p/hello.txt":     {Data: []byte("hello\n")},
		"p/sub/a.txt":     {Data: []byte("A")},
		"p/sub/b.txt":     {Data: []byte("B")},
		"p/sub/.secret":   {Data: []byte("nope")},
		"p/sub/_skip.txt": {Data: []byte("nope")},
		"p/hidden/.dot":   {Data: []byte("dot")},
		"p/hidden/_under": {Data: []byte("u")},
	}
	i := interp.New(interp.Options{SourcecodeFilesystem: fsys})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	_, err := i.EvalPath("p/main.go")
	if err != nil {
		t.Fatal(err)
	}
}

func TestGoEmbedErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		fsys fstest.MapFS
		want string
	}{
		{
			name: "missing",
			src:  "package main\n//go:embed missing.txt\nvar s string\nfunc main() {}\n",
			fsys: fstest.MapFS{"p/main.go": {}},
			want: "no matching files",
		},
		{
			name: "multi",
			src:  "package main\n//go:embed sub\nvar s string\nfunc main() {}\n",
			fsys: fstest.MapFS{
				"p/main.go":   {},
				"p/sub/a.txt": {Data: []byte("A")},
				"p/sub/b.txt": {Data: []byte("B")},
			},
			want: "exactly one file",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.fsys["p/main.go"] = &fstest.MapFile{Data: []byte(tc.src)}
			i := interp.New(interp.Options{SourcecodeFilesystem: tc.fsys})
			_, err := i.EvalPath("p/main.go")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestGoEmbedGroupAndCopy(t *testing.T) {
	src := `package main
import "embed"
var (
	//go:embed a.txt
	a string
	//go:embed "b.txt" c.txt
	f embed.FS
)
func main() {
	if a != "A" {
		panic(a)
	}
	b, err := f.ReadFile("b.txt")
	if err != nil || string(b) != "B" {
		panic(b)
	}
	c, err := f.ReadFile("c.txt")
	if err != nil || string(c) != "C" {
		panic(c)
	}
	f1, err := f.Open("b.txt")
	if err != nil {
		panic(err)
	}
	buf := make([]byte, 8)
	n, err := f1.Read(buf)
	if err != nil && err != io.EOF {
		panic(err)
	}
	if string(buf[:n]) != "B" {
		panic(buf)
	}
}
`
	src = strings.Replace(src, "import \"embed\"\n", "import (\n\t\"embed\"\n\t\"io\"\n)\n", 1)
	fsys := fstest.MapFS{
		"p/main.go": {Data: []byte(src)},
		"p/a.txt":   {Data: []byte("A")},
		"p/b.txt":   {Data: []byte("B")},
		"p/c.txt":   {Data: []byte("C")},
	}
	i := interp.New(interp.Options{SourcecodeFilesystem: fsys})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if _, err := i.EvalPath("p/main.go"); err != nil {
		t.Fatal(err)
	}
}
