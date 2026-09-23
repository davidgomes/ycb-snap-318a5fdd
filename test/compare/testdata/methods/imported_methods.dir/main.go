package main

import (
	"fmt"

	"imported_methods.dir/shapes"
)

type Square struct{ shapes.Rect }

func (s Square) Side() int { return s.W }

func main() {
	r := shapes.Rect{W: 2, H: 3}
	fmt.Println(r.Area(), r.String(), r.Secret())
	r.Grow(1)
	fmt.Println(r)
	var s fmt.Stringer = &r
	fmt.Println(s.String())
	area := shapes.Rect.Area
	fmt.Println(area(r), shapes.Unit.Area())
	grow := (*shapes.Rect).Grow
	grow(&r, 10)
	fmt.Println(r)
	sq := Square{shapes.Rect{W: 4, H: 4}}
	fmt.Println(sq.Side())
}
