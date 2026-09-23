// Copyright 2019 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/open2b/scriggo"
	"github.com/open2b/scriggo/internal/fstest"
	"github.com/open2b/scriggo/native"
)

type Shape interface {
	Area() int
	Name() string
}

type ShapeBox struct {
	Shape Shape
}

func TestMethodsWithNativeInterfaces(t *testing.T) {
	var out strings.Builder
	packages := native.Packages{
		"pkg": native.Package{
			Name: "pkg",
			Declarations: native.Declarations{
				"Shape":    reflect.TypeOf((*Shape)(nil)).Elem(),
				"ShapeBox": reflect.TypeOf(ShapeBox{}),
				"Keep":     func(s Shape) Shape { return s },
				"Stringer": reflect.TypeOf((*fmt.Stringer)(nil)).Elem(),
				"String":   func(s fmt.Stringer) string { return s.String() },
				"Error":    func(err error) string { return err.Error() },
				"Sort":     sort.Sort,
				"Print":    func(a ...interface{}) { fmt.Fprintln(&out, a...) },
			},
		},
	}
	main := `
	package main

	import "pkg"

	type Square struct{ side int }

	func (s Square) Area() int      { return s.side * s.side }
	func (s Square) Name() string   { return "square" }

	type Rect struct{ w, h int }

	func (r *Rect) Area() int    { return r.w * r.h }
	func (r *Rect) Name() string { return "rect" }

	type Side int

	func (s Side) String() string { return "side" }

	type NotFound string

	func (e *NotFound) Error() string { return string(*e) + " not found" }

	type Ints []int

	func (s Ints) Len() int           { return len(s) }
	func (s Ints) Less(i, j int) bool { return s[i] > s[j] }
	func (s Ints) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

	var global pkg.Shape = Square{3}

	func main() {
		shapes := []pkg.Shape{Square{2}, &Rect{2, 3}}
		shapes = append(shapes, global)
		for _, s := range shapes {
			pkg.Print(s.Name(), s.Area())
		}
		box := pkg.ShapeBox{Shape: &Rect{1, 5}}
		pkg.Print(box.Shape.Name(), box.Shape.Area())
		k := pkg.Keep(Square{4})
		if sq, ok := k.(Square); ok {
			pkg.Print(sq.side, k.Area())
		}
		var s pkg.Stringer = Side(0)
		pkg.Print(pkg.String(s), pkg.String(Side(1)), s)
		nf := NotFound("page")
		var err error = &nf
		pkg.Print(pkg.Error(err), err)
		ints := Ints{1, 3, 2}
		pkg.Sort(ints)
		pkg.Print(ints[0], ints[1], ints[2])
	}`
	fsys := fstest.Files{"main.go": main}
	program, err := scriggo.Build(fsys, &scriggo.BuildOptions{Packages: packages})
	if err != nil {
		t.Fatal(err)
	}
	err = program.Run(nil)
	if err != nil {
		t.Fatal(err)
	}
	expected := "square 4\nrect 6\nsquare 9\nrect 5\n4 16\nside side side\npage not found page not found\n3 2 1\n"
	if got := out.String(); got != expected {
		t.Fatalf("expected output:\n%s\ngot:\n%s", expected, got)
	}
}
