// run

package main

import (
	"fmt"
	"strings"
)

type Point struct{ X, Y int }

func (p Point) Add(q Point) Point { return Point{p.X + q.X, p.Y + q.Y} }
func (p *Point) Scale(k int)      { p.X *= k; p.Y *= k }
func (p Point) String() string    { return fmt.Sprintf("(%d,%d)", p.X, p.Y) }

type Num int

func (n Num) Plus(m ...Num) Num {
	for _, x := range m {
		n += x
	}
	return n
}

func pair() (Point, Point) { return Point{1, 1}, Point{2, 3} }

type Rune rune

func (r Rune) Up(c rune) rune { return c - 32 }

type holder struct {
	f func(Point) string
	g func(*Point, int)
}

func get() func(Point) string { return Point.String }

func apply(f func(Point, Point) Point, a, b Point) Point { return f(a, b) }

func main() {
	p := Point{1, 2}

	add := Point.Add
	fmt.Println(add(p, Point{1, 1}).String())
	fmt.Println(Point.Add(Point{5, 5}, Point{1, 1}).String())
	fmt.Println(Point.Add(pair()).String())
	fmt.Println(apply(Point.Add, p, p).String())

	scale := (*Point).Scale
	scale(&p, 2)
	fmt.Println(p.String())
	(*Point).Scale(&p, 3)
	fmt.Println(p.String())

	str := (*Point).String
	fmt.Println(str(&p), (*Point).String(&p), Point.String(p))
	addPtr := (*Point).Add
	fmt.Println(addPtr(&p, Point{1, 1}).String())

	fmt.Println(int(Num.Plus(1)), int(Num.Plus(1, 2, 3)), int(Num.Plus(1, []Num{4, 5}...)))
	plus := Num(10).Plus
	fmt.Println(int(plus()), int(plus(1, 2)))

	var f func(*Point, int) = (*Point).Scale
	f(&p, 10)
	fmt.Println(p.String())

	fs := map[string]func(Point) string{"s": Point.String}
	fmt.Println(fs["s"](Point{7, 8}))

	lit := []func(Point) string{Point.String, get()}
	lit = append(lit, Point.String)
	fmt.Println(lit[0](Point{1, 0}), lit[1](Point{2, 0}), lit[2](Point{3, 0}), len(lit))
	h := holder{f: Point.String, g: (*Point).Scale}
	q := Point{1, 1}
	h.g(&q, 4)
	fmt.Println(h.f(q))
	ch := make(chan func(Point) string, 1)
	ch <- Point.String
	fmt.Println((<-ch)(Point{5, 5}))
	var i interface{} = Point.String
	if f, ok := i.(func(Point) string); ok {
		fmt.Println(f(Point{6, 6}))
	}
	func(f func(Point) string) { fmt.Println(f(Point{7, 7})) }(Point.String)
	fmt.Println(strings.Map(Rune(0).Up, "abc"), Point.String(Point{8, 8})+"!")

	defer (*Point).Scale(&p, 0)
	defer fmt.Println(Point.String(p))

	var np *Point
	defer func() {
		fmt.Println("recovered:", recover() != nil)
	}()
	fmt.Println(str(np))
}
