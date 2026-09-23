// Copyright 2019 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package scriggo

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/open2b/scriggo/native"
)

type methodRecorder struct {
	lines []string
}

func (r *methodRecorder) add(s string) { r.lines = append(r.lines, s) }

func (r *methodRecorder) packages() native.Packages {
	return native.Packages{
		"rec": native.Package{
			Name: "rec",
			Declarations: native.Declarations{
				"Add":      r.add,
				"Stringer": reflect.TypeOf((*fmt.Stringer)(nil)).Elem(),
				"Namer": reflect.TypeOf((*interface {
					Name() string
				})(nil)).Elem(),
			},
		},
	}
}

func runMethodSrc(t *testing.T, src string, pkgs native.Packages) {
	t.Helper()
	prog, err := Build(Files{"main.go": []byte(src)}, &BuildOptions{Packages: pkgs})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := prog.Run(nil); err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestMethodDeclarations(t *testing.T) {
	rec := &methodRecorder{}
	src := `
package main

import "rec"

type Point struct { X, Y int }

func (p Point) Sum() int { return p.X + p.Y }
func (p *Point) Move(dx, dy int) { p.X += dx; p.Y += dy }

type MyInt int
func (n MyInt) Double() MyInt { return n * 2 }
func (n *MyInt) Inc() { *n = *n + 1 }

type Pair []int
func (p Pair) First() int { return p[0] }

type Set map[string]int
func (s Set) Has(k string) bool { _, ok := s[k]; return ok }

func (Point) ValueMethod(a int) int { return a + 1 }
func (p *Point) PtrMethod(a int) int { return p.X + a }

func main() {
	p := Point{X: 2, Y: 3}
	rec.Add(itoa(p.Sum()))
	p.Move(1, 4)
	rec.Add(itoa(p.Sum()))
	rec.Add(itoa(Point{X: 1, Y: 1}.Sum()))
	(&p).Move(0, 0)

	n := MyInt(4)
	rec.Add(itoa(int(n.Double())))
	n.Inc()
	rec.Add(itoa(int(n)))

	rec.Add(itoa(Pair{7, 8}.First()))
	rec.Add(bools(Set{"a": 1}.Has("a")))
	rec.Add(bools(Set{"a": 1}.Has("b")))

	f := Point.ValueMethod
	rec.Add(itoa(f(Point{}, 10)))
	rec.Add(itoa(Point.ValueMethod(Point{X: 1}, 2)))
	g := (*Point).PtrMethod
	rec.Add(itoa(g(&p, 5)))
	rec.Add(itoa((*Point).ValueMethod(&Point{X: 9}, 1)))

	mv := p.PtrMethod
	rec.Add(itoa(mv(6)))
	vv := p.ValueMethod
	rec.Add(itoa(vv(3)))
}

func itoa(n int) string {
	if n == 0 { return "0" }
	neg := n < 0
	if neg { n = -n }
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
func bools(b bool) string { if b { return "true" }; return "false" }
`
	runMethodSrc(t, src, rec.packages())
	got := strings.Join(rec.lines, ",")
	want := "5,10,2,8,5,7,true,false,11,3,8,2,9,4"
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestMethodInterfaces(t *testing.T) {
	rec := &methodRecorder{}
	src := `
package main

import "rec"

type Name string
func (n Name) String() string { return "name:" + string(n) }
func (n *Name) Name() string { return string(*n) }

func main() {
	n := Name("ada")
	var s rec.Stringer = n
	rec.Add(s.String())
	var s2 rec.Stringer = &n
	rec.Add(s2.String())
	var nm rec.Namer = &n
	rec.Add(nm.Name())
	take := func(s rec.Stringer) { rec.Add(s.String()) }
	take(n)
}
`
	runMethodSrc(t, src, rec.packages())
	got := strings.Join(rec.lines, ",")
	want := "name:ada,name:ada,ada,name:ada"
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestMethodErrors(t *testing.T) {
	cases := []struct {
		src string
		err string
	}{
		{
			src: `package main
type T struct{}
func (t T) M() {}
func (t *T) M() {}
func main() {}`,
			err: "redeclared in this block",
		},
		{
			src: `package main
type T struct{}
func (t *T) Ptr() {}
func main() { _ = T.Ptr }`,
			err: "needs pointer receiver",
		},
		{
			src: `package main
type T struct{}
func (t *T) M() {}
func main() { T{}.M() }`,
			err: "cannot call pointer method",
		},
		{
			src: `package main
func (int) M() {}
func main() {}`,
			err: "cannot define new methods on non-local type int",
		},
		{
			src: `package main
func (struct{}) M() {}
func main() {}`,
			err: "invalid receiver type",
		},
		{
			src: `package main
type T *int
func (T) M() {}
func main() {}`,
			err: "invalid receiver type",
		},
		{
			src: `package main
type A int
type B int
func (A) M() int { return 1 }
func (B) M() int { return 2 }
func main() {}`,
			err: "",
		},
	}
	for _, cas := range cases {
		_, err := Build(Files{"main.go": []byte(cas.src)}, nil)
		if cas.err == "" {
			if err != nil {
				t.Fatalf("unexpected error: %v\nsrc:\n%s", err, cas.src)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), cas.err) {
			t.Fatalf("error %q, want substring %q\nsrc:\n%s", err, cas.err, cas.src)
		}
	}
}

func TestMethodMoreForms(t *testing.T) {
	rec := &methodRecorder{}
	src := `
package main

import "rec"

type A int
type B int
func (a A) M() int { return int(a) + 1 }
func (b B) M() int { return int(b) + 2 }

type Arr [2]int
func (a Arr) First() int { return a[0] }

type Ch chan int
func (c Ch) Send(v int) { c <- v }

type Fn func(int) int
func (f Fn) Call(v int) int { return f(v) }

func (T) unnamed(x int) int { return x + 3 }
type T struct{ N int }
func (t *T) add(x int) { t.N += x }
func (t T) vals() (int, int) { return t.N, t.N + 1 }
func (t T) sum(a ...int) int {
	s := t.N
	for _, v := range a {
		s += v
	}
	return s
}

func main() {
	rec.Add(itoa(A(10).M()))
	rec.Add(itoa(B(10).M()))
	rec.Add(itoa(Arr{4, 5}.First()))
	c := make(Ch, 1)
	c.Send(9)
	rec.Add(itoa(<-c))
	f := Fn(func(v int) int { return v * 3 })
	rec.Add(itoa(f.Call(3)))
	rec.Add(itoa(T{}.unnamed(4)))
	t := T{N: 1}
	t.add(6)
	rec.Add(itoa(t.N))
	p := &t
	p.add(2)
	rec.Add(itoa(t.N))
	a, b := p.vals()
	rec.Add(itoa(a))
	rec.Add(itoa(b))
	rec.Add(itoa(t.sum(1, 2, 3)))
	defer func() { rec.Add("defer") }()
	defer t.add(1)
	rec.Add("body")
}

func itoa(n int) string {
	if n == 0 { return "0" }
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
`
	runMethodSrc(t, src, rec.packages())
	got := strings.Join(rec.lines, ",")
	want := "11,12,4,9,9,7,7,9,9,10,15,body,defer"
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestPointerReceiverDoesNotSatisfyValueInterface(t *testing.T) {
	src := `
package main
import "rec"
type Name string
func (n *Name) String() string { return string(*n) }
func main() {
	n := Name("a")
	var s rec.Stringer = n
	_ = s
}
`
	rec := &methodRecorder{}
	_, err := Build(Files{"main.go": []byte(src)}, &BuildOptions{Packages: rec.packages()})
	if err == nil || !strings.Contains(err.Error(), "Stringer") {
		t.Fatalf("expected interface error, got %v", err)
	}
}
