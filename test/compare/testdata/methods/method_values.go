// run

package main

import (
	"fmt"
	"strings"
)

type Point struct{ X, Y int }

func (p Point) String() string { return fmt.Sprintf("(%d,%d)", p.X, p.Y) }
func (p *Point) Scale(k int)   { p.X *= k; p.Y *= k }

type rot int

func (r rot) shift(c rune) rune {
	if c >= 'a' && c <= 'z' {
		return 'a' + (c-'a'+rune(r))%26
	}
	return c
}

type Worker struct{ id int }

func (w *Worker) Run(out chan<- string) { out <- fmt.Sprintf("worker %d done", w.id) }

type Acc struct{ total int }

func (a *Acc) Add(n int) { a.total += n }

func each(values []int, f func(int)) {
	for _, v := range values {
		f(v)
	}
}

type T struct{ n int }

func (t T) Adder() func(int) int {
	return func(x int) int { return x + t.n }
}

func main() {
	p := Point{1, 2}

	// The receiver of a method value is evaluated and copied when the
	// method value is evaluated.
	f := p.String
	p.X = 999
	fmt.Println(f(), p.String())

	g := p.Scale
	g(2)
	fmt.Println(p.String())

	pp := &p
	h := pp.String
	pp.Y = 0
	fmt.Println(h(), pp.String())

	fmt.Println(strings.Map(rot(13).shift, "hello"))

	var acc Acc
	each([]int{1, 2, 3, 4}, acc.Add)
	fmt.Println(acc.total)

	fs := make([]func() string, 3)
	for i := 0; i < 3; i++ {
		fs[i] = Point{i, i}.String
	}
	for _, f := range fs {
		fmt.Print(f(), " ")
	}
	fmt.Println()

	t := T{10}
	add := t.Adder()
	t.n = 50
	fmt.Println(add(1))

	func() {
		defer p.Scale(3)
		p.X = 1
		p.Y = 1
	}()
	fmt.Println(p.String())

	out := make(chan string)
	w := &Worker{7}
	go w.Run(out)
	fmt.Println(<-out)
	run := w.Run
	go run(out)
	fmt.Println(<-out)
}
