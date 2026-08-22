package main

import (
	_ "embed"
	"fmt"
)

var (
	//go:embed embed3a.txt
	a string
	//go:embed embed3b.txt
	b string
)

func main() {
	fmt.Println(a)
	fmt.Println(b)
}

// Output:
// grouped-a
// grouped-b
