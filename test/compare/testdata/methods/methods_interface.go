// run

package main

import "fmt"

type P struct{ n int }

func (p *P) String() string { return fmt.Sprint("P", p.n) }

type V int

func (v V) String() string { return fmt.Sprint("V", int(v)) }

func show(s fmt.Stringer) string { return s.String() }

func main() {
	p := &P{4}
	var s fmt.Stringer = p
	fmt.Println(s.String())
	f := func() string { return s.String() }
	p.n = 5
	fmt.Println(f())
	fmt.Println(show(V(2)), show(p))
	var pv = new(V)
	*pv = 9
	s = pv
	fmt.Println(s.String())
}
