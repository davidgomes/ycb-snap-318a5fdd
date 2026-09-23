// Copyright 2026 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/open2b/scriggo"
	"github.com/open2b/scriggo/internal/fstest"
	"github.com/open2b/scriggo/native"
)

type methodsShape interface {
	Area() int
	Name() string
}

func methodsPackages(out *strings.Builder) native.Importer {
	return native.Packages{
		"fmt": native.Package{
			Name: "fmt",
			Declarations: native.Declarations{
				"Println":  func(a ...interface{}) { fmt.Fprintln(out, a...) },
				"Sprint":   fmt.Sprint,
				"Sprintf":  fmt.Sprintf,
				"Stringer": reflect.TypeOf((*fmt.Stringer)(nil)).Elem(),
			},
		},
		"sort": native.Package{
			Name: "sort",
			Declarations: native.Declarations{
				"Interface": reflect.TypeOf((*sort.Interface)(nil)).Elem(),
				"Sort":      sort.Sort,
			},
		},
		"shapes": native.Package{
			Name: "shapes",
			Declarations: native.Declarations{
				"Shape": reflect.TypeOf((*methodsShape)(nil)).Elem(),
				"Describe": func(s fmt.Stringer) string {
					return "<" + s.String() + ">"
				},
				"ErrorText": func(err error) string {
					return err.Error()
				},
			},
		},
	}
}

var methodsTests = []struct {
	name string
	src  string
	out  string
	err  string
}{
	{
		name: "value and pointer receivers",
		src: `package main

		import "fmt"

		type Counter struct{ n int }

		func (c Counter) Get() int { return c.n }
		func (c *Counter) Inc() { c.n++ }
		func (c *Counter) Add(a, b int) int { c.n += a + b; return c.n }

		func main() {
			var c Counter
			c.Inc()
			c.Inc()
			fmt.Println(c.Get(), c.Add(1, 2))
			p := &c
			p.Inc()
			fmt.Println(p.Get(), c.Get())
		}`,
		out: "2 5\n6 6\n",
	},
	{
		name: "unnamed receivers",
		src: `package main

		import "fmt"

		type T int

		func (T) Value() string { return "value" }
		func (*T) Pointer() string { return "pointer" }

		func main() {
			var t T
			fmt.Println(t.Value(), t.Pointer())
		}`,
		out: "value pointer\n",
	},
	{
		name: "methods on all definable types",
		src: `package main

		import "fmt"

		type Int int
		type Str string
		type Slice []int
		type Map map[string]int
		type Func func(int) int
		type Array [3]int
		type Chan chan int
		type Bool bool
		type Float float64

		func (i Int) Double() Int { return i * 2 }
		func (s Str) Upper() Str { return s + "!" }
		func (s Slice) Sum() int {
			n := 0
			for _, v := range s {
				n += v
			}
			return n
		}
		func (s *Slice) Push(v int) { *s = append(*s, v) }
		func (m Map) Keys() int { return len(m) }
		func (f Func) Apply(v int) int { return f(v) }
		func (a Array) First() int { return a[0] }
		func (a *Array) Reset() { *a = Array{7, 8, 9} }
		func (c Chan) Cap() int { return cap(c) }
		func (b Bool) Not() Bool { return !b }
		func (f Float) Half() Float { return f / 2 }

		func main() {
			var s Slice
			s.Push(1)
			s.Push(2)
			var a Array
			a.Reset()
			fmt.Println(int(Int(21).Double()), string(Str("hi").Upper()), s.Sum(), Map{"a": 1}.Keys())
			fmt.Println(Func(func(v int) int { return v + 1 }).Apply(1), a.First(), make(Chan, 4).Cap(), bool(Bool(false).Not()), float64(Float(3).Half()))
		}`,
		out: "42 hi! 3 1\n2 7 4 true 1.5\n",
	},
	{
		name: "same method name on different types",
		src: `package main

		import "fmt"

		type A int
		type B string

		func (a A) Name() string { return "A" }
		func (b B) Name() string { return "B" + string(b) }
		func (a *A) Set(v int) { *a = A(v) }
		func (b *B) Set(v int) { *b = B(fmt.Sprint(v)) }

		func main() {
			var a A
			var b B
			a.Set(1)
			b.Set(2)
			fmt.Println(a.Name(), b.Name(), int(a))
		}`,
		out: "A B2 1\n",
	},
	{
		name: "method values",
		src: `package main

		import "fmt"

		type T struct{ s string }

		func (t T) Get() string { return t.s }
		func (t *T) Set(s string) { t.s = s }

		func main() {
			t := T{"a"}
			get := t.Get
			set := t.Set
			set("b")
			fmt.Println(get(), t.Get())
			p := &t
			pget := p.Get
			t.s = "c"
			fmt.Println(pget())
		}`,
		out: "a b\nb\n",
	},
	{
		name: "method expressions",
		src: `package main

		import "fmt"

		type T struct{ n int }

		func (t T) Get() int { return t.n }
		func (t *T) Set(n int) { t.n = n }
		func (t T) Sum(a ...int) int {
			s := t.n
			for _, v := range a {
				s += v
			}
			return s
		}

		func main() {
			t := T{1}
			(*T).Set(&t, 5)
			get := T.Get
			pget := (*T).Get
			fmt.Println(T.Get(t), get(t), pget(&t), T.Sum(t, 1, 2, 3))
			fs := []func(T) int{T.Get}
			fmt.Println(fs[0](T{9}))
		}`,
		out: "5 5 5 11\n9\n",
	},
	{
		name: "method expression with pointer receiver",
		src: `package main

		type T int

		func (t *T) M() {}

		func main() {
			_ = T.M
		}`,
		err: "invalid method expression T.M (needs pointer receiver: (*T).M)",
	},
	{
		name: "pointer method on non-addressable value",
		src: `package main

		type T int

		func (t *T) M() {}

		func main() {
			T(1).M()
		}`,
		err: "cannot call pointer method M on T",
	},
	{
		name: "Go interface satisfaction and dispatch",
		src: `package main

		import (
			"fmt"
			"shapes"
		)

		type Square struct{ side int }
		type Rect struct{ w, h int }

		func (s Square) Area() int    { return s.side * s.side }
		func (s Square) Name() string { return "square" }
		func (r *Rect) Area() int     { return r.w * r.h }
		func (r *Rect) Name() string  { return "rect" }

		func describe(s shapes.Shape) {
			fmt.Println(s.Name(), s.Area())
		}

		func main() {
			describe(Square{3})
			describe(&Rect{2, 5})
			describe(&Square{2})
			var s shapes.Shape = Square{4}
			if sq, ok := s.(Square); ok {
				fmt.Println("square", sq.side)
			}
			area := s.Area
			fmt.Println(area())
		}`,
		out: "square 9\nrect 10\nsquare 4\nsquare 4\n16\n",
	},
	{
		name: "pointer receivers satisfy only pointer interfaces",
		src: `package main

		import "shapes"

		type Rect struct{ w, h int }

		func (r *Rect) Area() int    { return r.w * r.h }
		func (r *Rect) Name() string { return "rect" }

		func main() {
			var s shapes.Shape = Rect{1, 2}
			_ = s
		}`,
		err: "cannot use Rect{...} (type Rect) as type misc.methodsShape in assignment",
	},
	{
		name: "String and Error methods called by Go code",
		src: `package main

		import (
			"fmt"
			"shapes"
		)

		type Name string
		type MyErr struct{ code int }

		func (n Name) String() string { return "name:" + string(n) }
		func (e *MyErr) Error() string { return fmt.Sprint("code ", e.code) }

		func main() {
			fmt.Println(Name("x"), shapes.Describe(Name("y")))
			var err error = &MyErr{3}
			fmt.Println(err, shapes.ErrorText(&MyErr{4}), err.Error())
			var s fmt.Stringer = Name("z")
			fmt.Println(s.String())
		}`,
		out: "name:x <name:y>\ncode 3 code 4 code 3\nname:z\n",
	},
	{
		name: "type switch on Scriggo types with methods",
		src: `package main

		import "fmt"

		type T int

		func (t T) String() string { return "T" }

		func main() {
			var i interface{} = T(1)
			switch v := i.(type) {
			case fmt.Stringer:
				fmt.Println("stringer", v.String())
			default:
				fmt.Println("default")
			}
			_, ok := i.(error)
			fmt.Println(ok)
		}`,
		out: "stringer T\nfalse\n",
	},
	{
		name: "defer and go method calls",
		src: `package main

		import "fmt"

		type T struct{ s []string }

		func (t *T) Add(s string) { t.s = append(t.s, s) }

		func run(t *T) {
			defer t.Add("deferred")
			t.Add("body")
		}

		func main() {
			t := &T{}
			run(t)
			fmt.Println(t.s)
		}`,
		out: "[body deferred]\n",
	},
	{
		name: "sort.Interface implemented by a Scriggo type",
		src: `package main

		import (
			"fmt"
			"sort"
		)

		type ByLen []string

		func (a ByLen) Len() int           { return len(a) }
		func (a ByLen) Less(i, j int) bool { return len(a[i]) < len(a[j]) }
		func (a ByLen) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }

		func main() {
			words := ByLen{"ccc", "a", "bb"}
			sort.Sort(words)
			var i sort.Interface = words
			fmt.Println(words[0], words[1], words[2], i.Len())
		}`,
		out: "a bb ccc 3\n",
	},
	{
		name: "duplicate method",
		src: `package main

		type T int

		func (T) M() {}
		func (*T) M() {}

		func main() {}`,
		err: "method T.M already declared",
	},
	{
		name: "method on non-local type",
		src: `package main

		func (i int) M() {}

		func main() {}`,
		err: "cannot define new methods on non-local type int",
	},
	{
		name: "field and method with the same name",
		src: `package main

		type T struct{ M int }

		func (T) M() {}

		func main() {}`,
		err: "field and method with the same name M",
	},
}

func TestMethods(t *testing.T) {
	for _, cas := range methodsTests {
		t.Run(cas.name, func(t *testing.T) {
			var out strings.Builder
			fsys := fstest.Files{"main.go": cas.src}
			program, err := scriggo.Build(fsys, &scriggo.BuildOptions{Packages: methodsPackages(&out)})
			if cas.err != "" {
				if err == nil {
					t.Fatalf("expected error %q, got no error", cas.err)
				}
				var buildErr *scriggo.BuildError
				if !errors.As(err, &buildErr) {
					t.Fatalf("expected build error, got %s", err)
				}
				if msg := buildErr.Message(); msg != cas.err {
					t.Fatalf("expected error %q, got %q", cas.err, msg)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			err = program.Run(nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := out.String(); got != cas.out {
				t.Fatalf("expected output %q, got %q", cas.out, got)
			}
		})
	}
}
