// run

package main

import (
	"fmt"
	"strings"
)

type rect struct {
	width, height int
}

func (r *rect) area() int {
	return r.width * r.height
}

func (r rect) perim() int {
	return 2*r.width + 2*r.height
}

type Point struct{ X, Y int }

func (p Point) Add(q Point) Point { return Point{p.X + q.X, p.Y + q.Y} }
func (p *Point) Scale(k int)      { p.X *= k; p.Y *= k }
func (p Point) String() string    { return fmt.Sprintf("(%d,%d)", p.X, p.Y) }

type Counter int

func (c *Counter) Inc()   { *c++ }
func (c *Counter) Add(n Counter) { *c += n }
func (c Counter) Get() int { return int(c) }

type Name string

func (n Name) Exclaim() Name { return n + "!" }
func (Name) Kind() string    { return "name" }
func (*Name) PtrKind() string { return "*name" }

type Words []string

func (w Words) Join(sep string) string { return strings.Join(w, sep) }
func (w *Words) Push(s ...string)      { *w = append(*w, s...) }

type Set map[string]bool

func (s Set) Add(k string)      { s[k] = true }
func (s Set) Has(k string) bool { return s[k] }

type Op func(int, int) int

func (o Op) Apply(a, b int) int { return o(a, b) }

type Flag bool

func (f Flag) Not() Flag { return !f }

type Vec [3]float64

func (v Vec) Sum() float64 { return v[0] + v[1] + v[2] }

type Pipe chan int

func (p Pipe) Send(v int) { p <- v }

type Cplx complex128

func (c Cplx) Re() float64 { return real(c) }

type Bytes []byte

func (b Bytes) Upper() string { return strings.ToUpper(string(b)) }

type Str string

func (s Str) Twice() (Str, int) { return s + s, 2 }

type T struct{ n int }

func (t *T) Inc() *T { t.n++; return t }
func (t T) Get() (n int) {
	n = t.n
	return
}
func (T T) Self() T { return T }
func (T) _()       {}

func newT() *T { return &T{100} }

func main() {

	r := rect{width: 10, height: 5}
	fmt.Println("area: ", r.area())
	fmt.Println("perim:", r.perim())
	rp := &r
	fmt.Println("area: ", rp.area())
	fmt.Println("perim:", rp.perim())

	p := Point{1, 2}
	fmt.Println(p.Add(Point{3, 4}).String())
	p.Scale(10)
	fmt.Println(p.String())
	pp := &p
	pp.Scale(2)
	fmt.Println(pp.String(), pp.Add(Point{1, 1}).String())

	var c Counter
	c.Inc()
	c.Inc()
	c.Add(40)
	fmt.Println(c.Get())

	n := Name("hi")
	fmt.Println(string(n.Exclaim().Exclaim()), n.Kind(), n.PtrKind())

	var w Words
	w.Push("a", "b")
	w.Push("c")
	parts := []string{"d", "e"}
	w.Push(parts...)
	fmt.Println(w.Join("-"))

	s := Set{}
	s.Add("x")
	fmt.Println(s.Has("x"), s.Has("z"))

	var o Op = func(a, b int) int { return a * b }
	fmt.Println(o.Apply(6, 7))

	var fl Flag
	fmt.Println(bool(fl.Not()), bool(fl.Not().Not()))

	v := Vec{1, 2, 3}
	fmt.Println(v.Sum())

	ch := make(Pipe, 1)
	ch.Send(42)
	fmt.Println(<-ch)

	fmt.Println(Cplx(3 + 4i).Re())
	fmt.Println(Bytes("abc").Upper())

	s2, k := Str("ab").Twice()
	fmt.Println(string(s2), k)

	t := T{}
	t.Inc().Inc().Inc()
	fmt.Println(t.Get(), t.Self().Get())

	ts := []T{{1}, {2}}
	ts[0].Inc()
	ts[1].Inc().Inc()
	fmt.Println(ts[0].Get(), ts[1].Get())

	type wrapper struct{ inner T }
	wr := wrapper{T{5}}
	wr.inner.Inc()
	pw := &wr
	pw.inner.Inc()
	fmt.Println(wr.inner.Get())

	fmt.Println((&T{9}).Inc().Get(), newT().Inc().Get())

	m := map[string]T{"a": {3}}
	fmt.Println(m["a"].Get())

	arr := [2]T{{7}, {8}}
	arr[1].Inc()
	fmt.Println(arr[1].Get())

	var np *T
	defer func() {
		fmt.Println("recovered:", recover() != nil)
	}()
	np.Get()
}
