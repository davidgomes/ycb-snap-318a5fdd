package main

import _ "embed"

//go:embed embed0.txt
var msg string

func main() {
	println(msg)
}

// Output:
// embedded-ok
