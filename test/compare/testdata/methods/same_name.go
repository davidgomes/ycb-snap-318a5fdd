// run

package main

import (
	"fmt"
	"math"
)

type rect struct {
	width, height float64
}

type circle struct {
	radius float64
}

type square float64

func (r rect) area() float64    { return r.width * r.height }
func (r rect) perim() float64   { return 2*r.width + 2*r.height }
func (c circle) area() float64  { return math.Pi * c.radius * c.radius }
func (c circle) perim() float64 { return 2 * math.Pi * c.radius }
func (s square) area() float64  { return float64(s * s) }
func (s *square) perim() float64 { return float64(4 * *s) }

func (r rect) String() string   { return fmt.Sprintf("rect %vx%v", r.width, r.height) }
func (c circle) String() string { return fmt.Sprintf("circle %v", c.radius) }
func (s square) String() string { return fmt.Sprintf("square %v", float64(s)) }

func area() string { return "the area function" }

func main() {
	r := rect{width: 3, height: 4}
	c := circle{radius: 5}
	s := square(2)
	fmt.Printf("%.2f %.2f\n", r.area(), r.perim())
	fmt.Printf("%.2f %.2f\n", c.area(), c.perim())
	fmt.Printf("%.2f %.2f\n", s.area(), s.perim())
	fmt.Println(area())

	areas := []func() float64{r.area, c.area, s.area}
	for _, a := range areas {
		fmt.Printf("%.2f\n", a())
	}
	fmt.Printf("%.2f %.2f %.2f\n", rect.area(r), circle.area(c), (*square).perim(&s))

	shapes := []fmt.Stringer{r, c, s, &s}
	for _, sh := range shapes {
		fmt.Println(sh.String())
	}
}
