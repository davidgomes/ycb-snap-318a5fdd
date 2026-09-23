package main

import (
	"embed"
	"fmt"
)

//go:embed a.txt
var s string

var (
	//go:embed a.txt
	b []byte
	//go:embed d
	f embed.FS
)

func Result() string {
	e, _ := f.ReadDir("d")
	var names []string
	for _, x := range e {
		names = append(names, x.Name())
	}
	return s + "|" + string(b) + "|" + fmt.Sprint(names)
}

func main() {
	if r := Result(); r != "hi\n|hi\n|[b.txt]" {
		panic(r)
	}
}
