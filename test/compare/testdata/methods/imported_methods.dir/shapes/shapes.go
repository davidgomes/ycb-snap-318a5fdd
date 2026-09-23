package shapes

import "fmt"

type Rect struct{ W, H int }

func (r Rect) Area() int      { return r.W * r.H }
func (r *Rect) Grow(n int)    { r.W += n; r.H += n }
func (r Rect) String() string { return fmt.Sprintf("Rect(%dx%d)", r.W, r.H) }
func (r Rect) secret() int    { return 7 }
func (r Rect) Secret() int    { return r.secret() * 6 }

var Unit = Rect{1, 1}
