package main

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed testdata/embed/hello.txt
var s string

//go:embed testdata/embed/hello.txt
var b []byte

var (
	//go:embed testdata/embed/static
	static embed.FS

	//go:embed all:testdata/embed/static
	//go:embed testdata/embed/hello.txt
	all embed.FS
)

var size = length()

func length() int { return len(s) }

func walk(fsys fs.FS) {
	fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		fmt.Println(path, d.IsDir())
		return err
	})
}

func main() {
	fmt.Printf("%q %q %d\n", s, b, size)
	walk(static)
	walk(all)
}

// Output:
// "hello\n" "hello\n" 6
// . true
// testdata true
// testdata/embed true
// testdata/embed/static true
// testdata/embed/static/css true
// testdata/embed/static/css/style.css false
// testdata/embed/static/index.html false
// . true
// testdata true
// testdata/embed true
// testdata/embed/hello.txt false
// testdata/embed/static true
// testdata/embed/static/.hidden false
// testdata/embed/static/_draft.txt false
// testdata/embed/static/css true
// testdata/embed/static/css/style.css false
// testdata/embed/static/index.html false
