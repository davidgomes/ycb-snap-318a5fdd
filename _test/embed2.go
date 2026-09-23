package main

import (
	"embed"
	"fmt"
	"io/fs"
)

var (
	//go:embed all:embed
	all embed.FS

	//go:embed embed/*.txt
	//go:embed embed/sub/*
	glob embed.FS
)

func list(fsys fs.FS) {
	fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			fmt.Println(path)
		}
		return nil
	})
}

func main() {
	list(all)
	fmt.Println("--")
	list(glob)
}

// Output:
// embed/.hidden
// embed/_underscore.txt
// embed/hello.txt
// embed/sub/file.txt
// --
// embed/_underscore.txt
// embed/hello.txt
// embed/sub/file.txt
