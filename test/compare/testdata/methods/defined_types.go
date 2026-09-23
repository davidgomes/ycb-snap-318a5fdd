// run

package main

import (
	"fmt"
	"strings"
)

type Int int

func (i Int) Double() Int { return i * 2 }
func (i *Int) Inc()       { *i = *i + 1 }

type Float float64

func (f Float) Half() float64 { return float64(f) / 2 }

type Complex complex128

func (c Complex) Real() float64 { return real(c) }

type Bool bool

func (b Bool) Not() bool { return !bool(b) }

type Str string

func (s Str) Upper() string { return strings.ToUpper(string(s)) }
func (s Str) Len() int      { return len(s) }

type Slice []int

func (s Slice) Sum() int {
	sum := 0
	for _, v := range s {
		sum += v
	}
	return sum
}

func (s *Slice) Push(v ...int) { *s = append(*s, v...) }

type Map map[string]int

func (m Map) Set(k string, v int) { m[k] = v }
func (m Map) Get(k string) int     { return m[k] }

type Array [3]int

func (a Array) First() int { return a[0] }

type Func func(int) int

func (f Func) Apply(n int) int { return f(n) }

type Chan chan int

func (c Chan) Send(v int) { c <- v }
func (c Chan) Recv() int  { return <-c }

type Struct struct {
	A, B int
}

func (s Struct) Sum() int { return s.A + s.B }

type Ptr struct{ v string }

func (p *Ptr) Value() string {
	if p == nil {
		return "nil"
	}
	return p.v
}

type Empty struct{}

func (Empty) Name() string { return "empty" }

func main() {

	i := Int(21)
	fmt.Println(int(i.Double()), int(i.Double().Double()))
	i.Inc()
	fmt.Println(int(i))

	fmt.Println(Float(3).Half())
	fmt.Println(Complex(2 + 3i).Real())
	fmt.Println(Bool(true).Not(), Bool(false).Not())
	fmt.Println(Str("go").Upper(), Str("hello").Len())

	s := Slice{1, 2}
	s.Push(3, 4)
	fmt.Println(s.Sum(), len(s))

	m := Map{}
	m.Set("a", 1)
	fmt.Println(m.Get("a"), m.Get("b"))

	fmt.Println(Array{7, 8, 9}.First())

	double := Func(func(n int) int { return n * 2 })
	fmt.Println(double.Apply(5))

	c := make(Chan, 1)
	c.Send(42)
	fmt.Println(c.Recv())

	fmt.Println(Struct{1, 2}.Sum())

	var p *Ptr
	fmt.Println(p.Value(), (&Ptr{"x"}).Value())

	fmt.Println(Empty{}.Name())

}
