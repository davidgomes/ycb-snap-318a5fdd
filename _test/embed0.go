package main

import (
	_ "embed"
	"fmt"
	"strings"
)

var upper = strings.ToUpper(hello)

//go:embed embed/hello.txt
var hello string

var (
	//go:embed embed/hello.txt
	helloBytes []byte

	//go:embed embed/sub
	sub string
)

func init() {
	fmt.Printf("init: %q\n", hello)
}

func main() {
	fmt.Printf("%q\n", hello)
	fmt.Printf("%q\n", helloBytes)
	fmt.Printf("%q\n", upper)
	fmt.Printf("%q\n", sub)
}

// Output:
// init: "Hello, embed!\n"
// "Hello, embed!\n"
// "Hello, embed!\n"
// "HELLO, EMBED!\n"
// "sub file\n"
