package interp_test

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

func embedTestFS(src string) fstest.MapFS {
	return fstest.MapFS{
		"main.go":                 &fstest.MapFile{Data: []byte(src)},
		"hello.txt":               &fstest.MapFile{Data: []byte("hello world")},
		"data.bin":                &fstest.MapFile{Data: []byte{1, 2, 3}},
		"static/index.html":       &fstest.MapFile{Data: []byte("<html>")},
		"static/b.css":            &fstest.MapFile{Data: []byte("b")},
		"static/.hidden":          &fstest.MapFile{Data: []byte("h")},
		"static/_skip.txt":        &fstest.MapFile{Data: []byte("s")},
		"static/sub/a.js":         &fstest.MapFile{Data: []byte("a")},
		"static/sub/.dot/x.txt":   &fstest.MapFile{Data: []byte("x")},
		"empty/.only":             &fstest.MapFile{Data: []byte("")},
		"pkg/src/foo/foo.go":      &fstest.MapFile{Data: []byte("package foo\n\nimport _ \"embed\"\n\n//go:embed foo.txt\nvar Foo string\n")},
		"pkg/src/foo/foo.txt":     &fstest.MapFile{Data: []byte("foo content")},
		"pkg/src/foo/unused.txt":  &fstest.MapFile{Data: []byte("unused")},
		"pkg/src/foo/sub/bar.txt": &fstest.MapFile{Data: []byte("bar")},
	}
}

func evalEmbed(t *testing.T, src string) (string, error) {
	t.Helper()
	var stdout bytes.Buffer
	i := interp.New(interp.Options{
		SourcecodeFilesystem: embedTestFS(src),
		GoPath:               "pkg",
		Stdout:               &stdout,
	})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	_, err := i.EvalPath("main.go")
	return stdout.String(), err
}

func TestEmbed(t *testing.T) {
	tests := []struct {
		desc, src, want string
	}{
		{
			desc: "string and bytes",
			src: `package main

import (
	_ "embed"
	"fmt"
)

//go:embed hello.txt
var s string

//go:embed data.bin
var b []byte

func main() { fmt.Println(s, b) }
`,
			want: "hello world [1 2 3]\n",
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

	x = 3

	//go:embed data.bin
	b []byte
)

func main() { fmt.Println(s, b, x) }
`,
			want: "hello world [1 2 3] 3\n",
		},
		{
			desc: "available before var initialization",
			src: `package main

import (
	_ "embed"
	"fmt"
)

var n = size()

func size() int { return len(s) }

//go:embed hello.txt
var s string

func main() { fmt.Println(n) }
`,
			want: "11\n",
		},
		{
			desc: "embed.FS tree",
			src: `package main

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed static hello.txt
//go:embed data.bin
var content embed.FS

func main() {
	fs.WalkDir(content, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fmt.Println(p, d.IsDir())
		return nil
	})
	b, err := content.ReadFile("static/index.html")
	fmt.Println(string(b), err)
	b[0] = 'X'
	b, _ = content.ReadFile("static/index.html")
	fmt.Println(string(b))
	_, err = content.ReadFile("static/.hidden")
	fmt.Println(err != nil)
	f, _ := content.Open("static")
	_, ok := f.(fs.ReadDirFile)
	fmt.Println(ok)
}
`,
			want: `. true
data.bin false
hello.txt false
static true
static/b.css false
static/index.html false
static/sub true
static/sub/a.js false
<html> <nil>
<html>
true
true
`,
		},
		{
			desc: "all prefix and glob",
			src: `package main

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed all:static
var all embed.FS

//go:embed static/*.css
var css embed.FS

func main() {
	fs.WalkDir(all, ".", func(p string, d fs.DirEntry, err error) error {
		if !d.IsDir() {
			fmt.Println(p)
		}
		return nil
	})
	entries, _ := css.ReadDir("static")
	for _, e := range entries {
		fmt.Println("css:", e.Name())
	}
}
`,
			want: `static/.hidden
static/_skip.txt
static/b.css
static/index.html
static/sub/.dot/x.txt
static/sub/a.js
css: b.css
`,
		},
		{
			desc: "imported package",
			src: `package main

import (
	"fmt"
	"foo"
)

func main() { fmt.Println(foo.Foo) }
`,
			want: "foo content\n",
		},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			out, err := evalEmbed(t, test.src)
			if err != nil {
				t.Fatal(err)
			}
			if out != test.want {
				t.Errorf("got %q, want %q", out, test.want)
			}
		})
	}
}

func TestEmbedError(t *testing.T) {
	tests := []struct {
		desc, src, want string
	}{
		{
			desc: "no match",
			src:  "package main\nimport _ \"embed\"\n//go:embed missing.txt\nvar s string\nfunc main() {}\n",
			want: "pattern missing.txt: no matching files found",
		},
		{
			desc: "multiple files for string",
			src:  "package main\nimport _ \"embed\"\n//go:embed hello.txt data.bin\nvar s string\nfunc main() {}\n",
			want: "multiple files for type string",
		},
		{
			desc: "directory for bytes",
			src:  "package main\nimport _ \"embed\"\n//go:embed static\nvar b []byte\nfunc main() {}\n",
			want: "multiple files for type",
		},
		{
			desc: "invalid type",
			src:  "package main\nimport _ \"embed\"\n//go:embed hello.txt\nvar n int\nfunc main() {}\n",
			want: "go:embed cannot apply to var of type int",
		},
		{
			desc: "initializer",
			src:  "package main\nimport _ \"embed\"\n//go:embed hello.txt\nvar s string = \"x\"\nfunc main() {}\n",
			want: "go:embed cannot apply to var with initializer",
		},
		{
			desc: "invalid pattern",
			src:  "package main\nimport _ \"embed\"\n//go:embed ../hello.txt\nvar s string\nfunc main() {}\n",
			want: "invalid pattern syntax",
		},
		{
			desc: "no embeddable files",
			src:  "package main\nimport \"embed\"\n//go:embed empty\nvar f embed.FS\nfunc main() {}\n",
			want: "contains no embeddable files",
		},
		{
			desc: "inside func",
			src:  "package main\nimport _ \"embed\"\nfunc main() {\n//go:embed hello.txt\nvar s string\n_ = s\n}\n",
			want: "go:embed cannot apply to var inside func",
		},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			_, err := evalEmbed(t, test.src)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("got %q, want %q", err, test.want)
			}
		})
	}
}

func TestEmbedEval(t *testing.T) {
	i := interp.New(interp.Options{SourcecodeFilesystem: embedTestFS("")})
	if _, err := i.Eval(`import "embed"`); err != nil {
		t.Fatal(err)
	}
	if _, err := i.Eval("//go:embed hello.txt\nvar s string"); err != nil {
		t.Fatal(err)
	}
	if _, err := i.Eval("//go:embed static/sub\nvar f embed.FS"); err != nil {
		t.Fatal(err)
	}
	if _, err := i.Eval(`b, _ := f.ReadFile("static/sub/a.js")`); err != nil {
		t.Fatal(err)
	}
	res, err := i.Eval(`s + string(b)`)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Interface(); got != "hello worlda" {
		t.Errorf("got %q", got)
	}
}
