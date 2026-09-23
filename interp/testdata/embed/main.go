package main

import _ "embed"

//go:embed nope.txt
var s string

func main() {}
