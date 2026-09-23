package interp

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/traefik/yaegi/stdlib"
)

func TestEmbedDirective(t *testing.T) {
	fsys := fstest.MapFS{
		"hello.txt":        {Data: []byte("hello")},
		"file name.txt":    {Data: []byte("spaced")},
		"a.txt":            {Data: []byte("A")},
		"b.txt":            {Data: []byte("B")},
		"img/a.txt":        {Data: []byte("img-a")},
		"img/.hidden":      {Data: []byte("dot")},
		"img/_skip":        {Data: []byte("under")},
		"img/sub/b.txt":    {Data: []byte("sub-b")},
		"img/sub/.secret":  {Data: []byte("sec")},
		"keep/.keep":       {Data: []byte("k")},
		"keep/visible.txt": {Data: []byte("vis")},
	}

	run := func(t *testing.T, src string) error {
		t.Helper()
		i := New(Options{SourcecodeFilesystem: fsys})
		if err := i.Use(stdlib.Symbols); err != nil {
			t.Fatal(err)
		}
		_, err := i.Eval(src)
		return err
	}

	t.Run("string and bytes", func(t *testing.T) {
		err := run(t, `
package main
import _ "embed"
import "fmt"

//go:embed hello.txt
var s string

//go:embed hello.txt
var b []byte

type S string
//go:embed hello.txt
var named S

var copied = s + "!"

func init() {
	if s != "hello" || string(b) != "hello" || string(named) != "hello" || copied != "hello!" {
		panic(fmt.Sprintf("init s=%q b=%q named=%q copied=%q", s, b, named, copied))
	}
	b[0] = 'H'
}

func main() {
	if s != "hello" || string(b) != "Hello" || copied != "hello!" {
		panic(fmt.Sprintf("main s=%q b=%q copied=%q", s, b, copied))
	}
}
`)
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("grouped and quoted", func(t *testing.T) {
		err := run(t, `
package main
import _ "embed"

var (
	//go:embed hello.txt
	//go:embed "file name.txt"
	s string
	other int
)

func main() {
	if s != "hello" || other != 0 {
		panic(s)
	}
}
`)
		// Two files cannot initialize a string. The quoted name is a second file.
		if err == nil || !strings.Contains(err.Error(), "multiple files") {
			t.Fatalf("got %v", err)
		}
		err = run(t, `
package main
import _ "embed"
var (
	//go:embed "file name.txt"
	s string
)
func main() {
	if s != "spaced" {
		panic(s)
	}
}
`)
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("filesystem", func(t *testing.T) {
		err := run(t, `
package main
import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
)

//go:embed img
//go:embed a.txt b.txt
var content embed.FS

//go:embed all:img
var all embed.FS

func main() {
	ents, err := content.ReadDir("img")
	if err != nil {
		panic(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	got := fmt.Sprint(names)
	if got != "[a.txt sub]" {
		panic("readdir " + got)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			panic("unsorted")
		}
	}
	b1, err := content.ReadFile("img/a.txt")
	if err != nil {
		panic(err)
	}
	b2, err := content.ReadFile("img/a.txt")
	if err != nil {
		panic(err)
	}
	b1[0] = 'X'
	if !bytes.Equal(b2, []byte("img-a")) {
		panic(fmt.Sprintf("copy %q", b2))
	}
	if _, err := content.ReadFile("img/.hidden"); err == nil {
		panic("hidden file embedded")
	}
	sub, err := content.ReadFile("img/sub/b.txt")
	if err != nil || string(sub) != "sub-b" {
		panic(fmt.Sprintf("sub %q %v", sub, err))
	}
	f, err := content.Open("img")
	if err != nil {
		panic(err)
	}
	rdf, ok := f.(fs.ReadDirFile)
	if !ok {
		panic(fmt.Sprintf("open dir type %T", f))
	}
	dents, err := rdf.ReadDir(-1)
	if err != nil || len(dents) != 2 {
		panic(fmt.Sprintf("readdirfile %v %v", dents, err))
	}
	var fsys fs.FS = content
	var rff fs.ReadFileFS = content
	var rdfsys fs.ReadDirFS = content
	if fsys == nil || rff == nil || rdfsys == nil {
		panic("interfaces")
	}
	hidden, err := all.ReadFile("img/.hidden")
	if err != nil || string(hidden) != "dot" {
		panic(fmt.Sprintf("all hidden %q %v", hidden, err))
	}
	sec, err := all.ReadFile("img/sub/.secret")
	if err != nil || string(sec) != "sec" {
		panic(fmt.Sprintf("all secret %q %v", sec, err))
	}
	under, err := all.ReadFile("img/_skip")
	if err != nil || string(under) != "under" {
		panic(fmt.Sprintf("all under %q %v", under, err))
	}
	ab, err := content.ReadFile("a.txt")
	if err != nil || string(ab) != "A" {
		panic(fmt.Sprintf("a %q %v", ab, err))
	}
}
`)
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("source directory", func(t *testing.T) {
		nested := fstest.MapFS{
			"pkg/main.go": {Data: []byte(`package main
import _ "embed"
//go:embed data/hello.txt
var s string
func main() {
	if s != "nested" {
		panic(s)
	}
}
`)},
			"pkg/data/hello.txt": {Data: []byte("nested")},
		}
		i := New(Options{SourcecodeFilesystem: nested})
		if err := i.Use(stdlib.Symbols); err != nil {
			t.Fatal(err)
		}
		if _, err := i.EvalPath("pkg/main.go"); err != nil {
			t.Fatal(err)
		}
	})

	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "no import",
			src:  "package main\n//go:embed hello.txt\nvar s string\nfunc main() {}\n",
			want: `import "embed"`,
		},
		{
			name: "multiple vars",
			src:  "package main\nimport _ \"embed\"\n//go:embed hello.txt\nvar a, b string\nfunc main() {}\n",
			want: "multiple vars",
		},
		{
			name: "initializer",
			src:  "package main\nimport _ \"embed\"\n//go:embed hello.txt\nvar s = \"x\"\nfunc main() {}\n",
			want: "initializer",
		},
		{
			name: "inside func",
			src:  "package main\nimport _ \"embed\"\nfunc main() {\n//go:embed hello.txt\nvar s string\nprintln(s)\n}\n",
			want: "inside func",
		},
		{
			name: "wrong type",
			src:  "package main\nimport _ \"embed\"\n//go:embed hello.txt\nvar s int\nfunc main() {}\n",
			want: "cannot apply to var of type",
		},
		{
			name: "two files",
			src:  "package main\nimport _ \"embed\"\n//go:embed a.txt b.txt\nvar s string\nfunc main() {}\n",
			want: "multiple files",
		},
		{
			name: "missing",
			src:  "package main\nimport _ \"embed\"\n//go:embed missing.txt\nvar s string\nfunc main() {}\n",
			want: "no matching files found",
		},
		{
			name: "bad pattern",
			src:  "package main\nimport _ \"embed\"\n//go:embed ../x.txt\nvar s string\nfunc main() {}\n",
			want: "invalid pattern syntax",
		},
		{
			name: "above group",
			src:  "package main\nimport _ \"embed\"\n//go:embed hello.txt\nvar (\ns string\n)\nfunc main() { println(s) }\n",
			want: "misplaced go:embed directive",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := run(t, tc.src)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want substring %q", err, tc.want)
			}
		})
	}
}
