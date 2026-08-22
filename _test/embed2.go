package main

import (
	"embed"
	"fmt"
)

//go:embed embed2.txt
var f embed.FS

func main() {
	b, err := f.ReadFile("embed2.txt")
	if err != nil {
		panic(err)
	}
	fmt.Println(string(b))
}

// Output:
// fs from embed
