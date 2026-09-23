package main

import (
	"fmt"

	"packages.dir/shapes"
)

// A type with the same name and methods of a type of another package.
type Rect struct{ Side int }

func (r Rect) Area() int { return r.Side * r.Side }

func main() {
	r := shapes.Rect{W: 2, H: 3}
	fmt.Println(r.Area(), r.Perimeter())
	r.Grow(1)
	fmt.Println(r.Area(), r.String())
	p := shapes.New(1, 1)
	p.Grow(1)
	fmt.Println(p.Area())
	var s fmt.Stringer = r
	fmt.Println(s, shapes.Describe(p))
	area := shapes.Rect.Area
	fmt.Println(area(r), Rect{3}.Area())
}
