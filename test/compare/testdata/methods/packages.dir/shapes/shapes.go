package shapes

import "fmt"

type Rect struct {
	W, H int
}

func (r Rect) Area() int { return r.W * r.H }

func (r *Rect) Grow(d int) {
	r.W += d
	r.H += d
}

func (r Rect) String() string { return fmt.Sprintf("%dx%d", r.W, r.H) }

func (r Rect) perimeter() int { return 2 * (r.W + r.H) }

func (r Rect) Perimeter() int { return r.perimeter() }

func New(w, h int) *Rect { return &Rect{w, h} }

func Describe(s fmt.Stringer) string { return "shape " + s.String() }
