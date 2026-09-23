package main

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed embed
var content embed.FS

func main() {
	fs.WalkDir(content, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fmt.Println(path, d.IsDir())
		return nil
	})

	entries, err := content.ReadDir("embed")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		fmt.Println("entry:", e.Name())
	}

	f, err := content.Open("embed/sub")
	if err != nil {
		panic(err)
	}
	_, ok := f.(fs.ReadDirFile)
	fmt.Println("ReadDirFile:", ok)

	b1, _ := content.ReadFile("embed/hello.txt")
	b1[0] = 'J'
	b2, _ := content.ReadFile("embed/hello.txt")
	fmt.Printf("%q\n", b2)

	_, err = content.ReadFile("embed/.hidden")
	fmt.Println(err)
}

// Output:
// . true
// embed true
// embed/hello.txt false
// embed/sub true
// embed/sub/file.txt false
// entry: hello.txt
// entry: sub
// ReadDirFile: true
// "Hello, embed!\n"
// open embed/.hidden: file does not exist
