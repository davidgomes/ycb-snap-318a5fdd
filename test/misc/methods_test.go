// Copyright 2019 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"reflect"
	"testing"

	"github.com/open2b/scriggo"
	"github.com/open2b/scriggo/internal/fstest"
	"github.com/open2b/scriggo/native"
)

type valueI interface{ M() int }
type ptrI interface{ P() int }

func TestScriggoMethods(t *testing.T) {
	var got []int
	packages := native.Packages{
		"pkg": native.Package{
			Name: "pkg",
			Declarations: native.Declarations{
				"I":  reflect.TypeOf((*valueI)(nil)).Elem(),
				"PI": reflect.TypeOf((*ptrI)(nil)).Elem(),
				"Add": func(n int) {
					got = append(got, n)
				},
			},
		},
	}
	main := `
package main

import "pkg"

type T int

func (t T) M() int { return int(t) + 1 }

func (t *T) P() int { return int(*t) + 2 }

func (t *T) Inc() { *t = *t + T(1) }

func (T) U() int { return 7 }

type S struct{ N int }

func (s S) Get() int { return s.N }

type A int
type B int

func (A) Name() int { return 1 }
func (B) Name() int { return 2 }

type Slice []int

func (s Slice) Len() int { return len(s) }

func main() {
	var t T = 3
	pkg.Add(t.M())
	t.Inc()
	pkg.Add(t.P())
	pkg.Add(T.U(t))
	f := T.M
	pkg.Add(f(T(4)))
	g := (*T).P
	pkg.Add(g(&t))
	var i pkg.I = T(10)
	pkg.Add(i.M())
	var pi pkg.PI = &t
	pkg.Add(pi.P())
	pkg.Add(S{N: 6}.Get())
	pkg.Add(A(0).Name())
	pkg.Add(B(0).Name())
	pkg.Add(Slice{1, 2, 3}.Len())
	mv := t.M
	pkg.Add(mv())
}
`
	fsys := fstest.Files{"main.go": main}
	program, err := scriggo.Build(fsys, &scriggo.BuildOptions{Packages: packages})
	if err != nil {
		t.Fatal(err)
	}
	if err := program.Run(nil); err != nil {
		t.Fatal(err)
	}
	// t starts at 3. M -> 4. Inc -> 4. P -> 6. U -> 7. f(4) -> 5.
	// g(&t) -> 6. i.M on T(10) -> 11. pi.P on t (still 4) -> 6.
	// S.Get -> 6. A.Name -> 1. B.Name -> 2. Len -> 3.
	// method value t.M captured t after Inc, which is 4, so 5.
	want := []int{4, 6, 7, 5, 6, 11, 6, 6, 1, 2, 3, 5}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestPointerMethodExpressionError(t *testing.T) {
	src := `
package main

type T int

func (t *T) P() int { return int(*t) }

func main() {
	_ = T.P
}
`
	_, err := scriggo.Build(fstest.Files{"main.go": src}, nil)
	if err == nil {
		t.Fatal("expected compile error")
	}
	if got := err.Error(); !contains(got, "needs pointer receiver") {
		t.Fatal(got)
	}
}

func TestValueDoesNotSatisfyPointerInterface(t *testing.T) {
	packages := native.Packages{
		"pkg": native.Package{
			Name: "pkg",
			Declarations: native.Declarations{
				"PI": reflect.TypeOf((*ptrI)(nil)).Elem(),
			},
		},
	}
	src := `
package main

import "pkg"

type T int

func (t *T) P() int { return int(*t) }

func main() {
	var i pkg.PI = T(1)
	_ = i
}
`
	_, err := scriggo.Build(fstest.Files{"main.go": src}, &scriggo.BuildOptions{Packages: packages})
	if err == nil {
		t.Fatal("expected compile error")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		})())
}
