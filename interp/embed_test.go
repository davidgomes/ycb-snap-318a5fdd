package interp_test

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

var embedTestFiles = fstest.MapFS{
	"hello.txt":               {Data: []byte("hello\n")},
	"my file.txt":             {Data: []byte("spaced\n")},
	"static/index.html":       {Data: []byte("<h1>index</h1>\n")},
	"static/css/style.css":    {Data: []byte("body {}\n")},
	"static/.hidden":          {Data: []byte("hidden\n")},
	"static/_draft.txt":       {Data: []byte("draft\n")},
	"static/_private/key.txt": {Data: []byte("key\n")},
	"dotfiles/.config":        {Data: []byte("config\n")},
	"pkg/pkg.go": {Data: []byte(`package pkg

import _ "embed"

//go:embed data/value.txt
var Value string
`)},
	"pkg/data/value.txt": {Data: []byte("value\n")},
}

// evalEmbed runs the main package src, using a source filesystem which
// also contains embedTestFiles, and returns its output.
func evalEmbed(t *testing.T, src string) (string, error) {
	t.Helper()

	fsys := fstest.MapFS{"main.go": {Data: []byte(src)}}
	for name, file := range embedTestFiles {
		fsys[name] = file
	}

	var stdout bytes.Buffer
	i := interp.New(interp.Options{SourcecodeFilesystem: fsys, Stdout: &stdout})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	_, err := i.EvalPath("main.go")
	return stdout.String(), err
}

func TestEmbed(t *testing.T) {
	tests := []testCase{
		{
			desc: "string",
			src: `package main

import (
	_ "embed"
	"fmt"
)

//go:embed hello.txt
var s string

func main() { fmt.Printf("%q", s) }
`,
			res: `"hello\n"`,
		},
		{
			desc: "byte slice",
			src: `package main

import (
	_ "embed"
	"fmt"
)

//go:embed hello.txt
var b []byte

func main() { fmt.Printf("%q", b) }
`,
			res: `"hello\n"`,
		},
		{
			desc: "grouped vars",
			src: `package main

import (
	_ "embed"
	"fmt"
)

var (
	//go:embed hello.txt
	s string

	n int

	//go:embed static/index.html
	b []byte
)

func main() { fmt.Printf("%q %d %q", s, n, b) }
`,
			res: `"hello\n" 0 "<h1>index</h1>\n"`,
		},
		{
			desc: "blank line and comment before var",
			src: `package main

import (
	_ "embed"
	"fmt"
)

//go:embed hello.txt

// s is embedded.
var s string

func main() { fmt.Printf("%q", s) }
`,
			res: `"hello\n"`,
		},
		{
			desc: "quoted pattern",
			src: `package main

import (
	_ "embed"
	"fmt"
)

//go:embed "my file.txt"
var s string

func main() { fmt.Printf("%q", s) }
`,
			res: `"spaced\n"`,
		},
		{
			desc: "set before variable initializers",
			src: `package main

import (
	_ "embed"
	"fmt"
)

var n = size()

func size() int { return len(s) }

var m = len(s)

//go:embed hello.txt
var s string

func main() { fmt.Println(n, m) }
`,
			res: "6 6\n",
		},
		{
			desc: "set before init",
			src: `package main

import (
	_ "embed"
	"fmt"
)

func init() { fmt.Printf("%q", s) }

//go:embed hello.txt
var s string

func main() {}
`,
			res: `"hello\n"`,
		},
		{
			desc: "fs tree",
			src: `package main

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed hello.txt static
var f embed.FS

func main() {
	fs.WalkDir(f, ".", func(path string, d fs.DirEntry, err error) error {
		fmt.Println(path, d.IsDir())
		return err
	})
}
`,
			res: `. true
hello.txt false
static true
static/css true
static/css/style.css false
static/index.html false
`,
		},
		{
			desc: "fs multiple directives and all prefix",
			src: `package main

import (
	"embed"
	"fmt"
)

//go:embed all:static
//go:embed hello.txt
var f embed.FS

func main() {
	entries, err := f.ReadDir("static")
	for _, e := range entries {
		fmt.Println(e.Name(), e.IsDir())
	}
	fmt.Println(err)
}
`,
			res: `.hidden false
_draft.txt false
_private true
css true
index.html false
<nil>
`,
		},
		{
			desc: "fs glob matching dot file",
			src: `package main

import (
	"embed"
	"fmt"
)

//go:embed dotfiles/*
var f embed.FS

func main() {
	b, err := f.ReadFile("dotfiles/.config")
	fmt.Printf("%q %v", b, err)
}
`,
			res: `"config\n" <nil>`,
		},
		{
			desc: "fs interfaces",
			src: `package main

import (
	"embed"
	"fmt"
	"io/fs"
	"testing/fstest"
)

//go:embed static
var f embed.FS

func main() {
	var fsys fs.FS = f
	_, isReadFileFS := fsys.(fs.ReadFileFS)
	_, isReadDirFS := fsys.(fs.ReadDirFS)
	fmt.Println(isReadFileFS, isReadDirFS)

	d, _ := f.Open("static")
	_, isReadDirFile := d.(fs.ReadDirFile)
	fmt.Println(isReadDirFile)

	fmt.Println(fstest.TestFS(f, "static/index.html", "static/css/style.css"))
}
`,
			res: "true true\ntrue\n<nil>\n",
		},
		{
			desc: "fs read file returns a copy",
			src: `package main

import (
	"embed"
	"fmt"
)

//go:embed hello.txt
var f embed.FS

func main() {
	b, _ := f.ReadFile("hello.txt")
	b[0] = 'j'
	c, _ := f.ReadFile("hello.txt")
	fmt.Printf("%q %q", b, c)
}
`,
			res: `"jello\n" "hello\n"`,
		},
		{
			desc: "fs errors",
			src: `package main

import (
	"embed"
	"fmt"
)

//go:embed static
var f embed.FS

func main() {
	_, err := f.ReadFile("static")
	fmt.Println(err)
	_, err = f.ReadDir("static/index.html")
	fmt.Println(err)
	_, err = f.Open("hello.txt")
	fmt.Println(err)
}
`,
			res: `read static: is a directory
read static/index.html: not a directory
open hello.txt: file does not exist
`,
		},
		{
			desc: "fs zero value",
			src: `package main

import (
	"embed"
	"fmt"
	"testing/fstest"
)

var f embed.FS

func main() {
	entries, err := f.ReadDir(".")
	fmt.Println(len(entries), err, fstest.TestFS(f))
}
`,
			res: "0 <nil> <nil>\n",
		},
		{
			desc: "imported package",
			src: `package main

import (
	"fmt"

	"./pkg"
)

func main() { fmt.Printf("%q", pkg.Value) }
`,
			res: `"value\n"`,
		},
		{
			desc: "no matching files",
			src: `package main

import _ "embed"

//go:embed nope.txt
var s string

func main() {}
`,
			err: "pattern nope.txt: no matching files found",
		},
		{
			desc: "invalid pattern",
			src: `package main

import _ "embed"

//go:embed ../hello.txt
var s string

func main() {}
`,
			err: "pattern ../hello.txt: invalid pattern syntax",
		},
		{
			desc: "no embeddable files",
			src: `package main

import "embed"

//go:embed dotfiles
var f embed.FS

func main() {}
`,
			err: "pattern dotfiles: cannot embed directory dotfiles: contains no embeddable files",
		},
		{
			desc: "multiple files for string",
			src: `package main

import _ "embed"

//go:embed static/index.html
//go:embed hello.txt
var s string

func main() {}
`,
			err: "invalid go:embed: multiple files for type string",
		},
		{
			desc: "directory for byte slice",
			src: `package main

import _ "embed"

//go:embed static
var b []byte

func main() {}
`,
			err: "invalid go:embed: multiple files for type []",
		},
		{
			desc: "invalid type",
			src: `package main

import _ "embed"

//go:embed hello.txt
var i int

func main() {}
`,
			err: "go:embed cannot apply to var of type int",
		},
		{
			desc: "multiple vars",
			src: `package main

import _ "embed"

//go:embed hello.txt
var a, b string

func main() {}
`,
			err: "go:embed cannot apply to multiple vars",
		},
		{
			desc: "initializer",
			src: `package main

import _ "embed"

//go:embed hello.txt
var s = "x"

func main() {}
`,
			err: "go:embed cannot apply to var with initializer",
		},
		{
			desc: "inside func",
			src: `package main

import _ "embed"

func main() {
	//go:embed hello.txt
	var s string
	println(s)
}
`,
			err: "misplaced go:embed directive",
		},
		{
			desc: "before func",
			src: `package main

import _ "embed"

//go:embed hello.txt
func main() {}
`,
			err: "misplaced go:embed directive",
		},
		{
			desc: "missing pattern",
			src: `package main

import _ "embed"

//go:embed
var s string

func main() {}
`,
			err: "usage: //go:embed pattern...",
		},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			res, err := evalEmbed(t, test.src)
			if test.err != "" {
				if err == nil || !strings.Contains(err.Error(), test.err) {
					t.Fatalf("got error %v, want %q", err, test.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if res != test.res {
				t.Fatalf("got %q, want %q", res, test.res)
			}
		})
	}
}

func TestEmbedByteSliceReinitialized(t *testing.T) {
	fsys := fstest.MapFS{"hello.txt": embedTestFiles["hello.txt"]}
	i := interp.New(interp.Options{SourcecodeFilesystem: fsys})
	prog, err := i.Compile("//go:embed hello.txt\nvar b []byte")
	if err != nil {
		t.Fatal(err)
	}
	for k := 0; k < 2; k++ {
		if _, err := i.Execute(prog); err != nil {
			t.Fatal(err)
		}
		res, err := i.Eval("string(b)")
		if err != nil {
			t.Fatal(err)
		}
		if s := res.String(); s != "hello\n" {
			t.Fatalf("got %q, want %q", s, "hello\n")
		}
		if _, err := i.Eval("b[0] = 'j'"); err != nil {
			t.Fatal(err)
		}
	}
}
