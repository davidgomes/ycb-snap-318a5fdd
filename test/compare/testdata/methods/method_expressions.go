// run

package main

import "fmt"

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

	defer (*Point).Scale(&p, 0)
	defer fmt.Println(Point.String(p))

	var np *Point
	defer func() {
		fmt.Println("recovered:", recover() != nil)
	}()
	fmt.Println(str(np))
}
