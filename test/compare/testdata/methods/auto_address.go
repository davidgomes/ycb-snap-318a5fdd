// run

package main

import "fmt"

type T struct {
	n int
}

func (t *T) Inc()     { t.n++ }
func (t T) Value() int { return t.n }

type Wrapper struct {
	T T
}

var global T

func main() {

	// x.Inc() is shorthand for (&x).Inc() if x is addressable.
	var t T
	t.Inc()
	fmt.Println(t.n)

	// Addressable struct fields.
	var w Wrapper
	w.T.Inc()
	w.T.Inc()
	fmt.Println(w.T.n)

	// Addressable slice and array elements.
	s := []T{{1}, {2}}
	s[1].Inc()
	fmt.Println(s[0].n, s[1].n)
	a := [2]T{{1}, {2}}
	a[0].Inc()
	fmt.Println(a[0].n, a[1].n)

	// Package variables.
	global.Inc()
	global.Inc()
	fmt.Println(global.n)

	// Parameters and variables captured by closures.
	func(x T) {
		x.Inc()
		fmt.Println(x.n)
	}(T{10})
	var c T
	f := func() {
		c.Inc()
	}
	f()
	c.Inc()
	fmt.Println(c.n)

	// p.Value() is shorthand for (*p).Value() if p is a pointer.
	p := &T{5}
	fmt.Println(p.Value())
	p.Inc()
	fmt.Println(p.Value(), p.n)

	// Method values with a pointer receiver share the receiver.
	inc := t.Inc
	inc()
	inc()
	fmt.Println(t.n)

	// Method values with a value receiver copy the receiver when they are
	// evaluated.
	t.n = 1
	value := t.Value
	t.n = 2
	fmt.Println(value(), t.Value())

	pv := p.Value
	p.n = 100
	fmt.Println(pv(), p.Value())

	// Method values in variables, slices and maps.
	methods := []func() int{T{1}.Value, T{2}.Value, (&T{3}).Value}
	for _, m := range methods {
		fmt.Print(m(), " ")
	}
	fmt.Println()
	byName := map[string]func(){"inc": t.Inc}
	byName["inc"]()
	fmt.Println(t.n)

}
