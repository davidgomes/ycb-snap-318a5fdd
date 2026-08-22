// run

package main

import "fmt"

type T int

func (t T) Value() int { return int(t) + 1 }

func (t *T) Ptr() int { return int(*t) + 2 }

func main() {
	var t T = 10
	fmt.Print(T.Value(t))
	fmt.Print((*T).Ptr(&t))
	f := T.Value
	fmt.Print(f(t))
	g := (*T).Ptr
	fmt.Print(g(&t))
	h := t.Value
	fmt.Print(h())
	p := t.Ptr
	fmt.Print(p())
}
