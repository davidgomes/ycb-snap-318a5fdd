// run

package main

import (
	"fmt"

	. "github.com/open2b/scriggo/test/compare/testpkg"
)

type V int

func (v V) M() { fmt.Print("V", int(v)) }

type P int

func (p *P) M() { fmt.Print("P", int(*p)) }

func main() {
	var v V = 3
	var i I = v
	i.M()

	var p P = 7
	var j I = &p
	j.M()
}
