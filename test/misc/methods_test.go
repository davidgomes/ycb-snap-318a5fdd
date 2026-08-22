// Copyright 2019 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/open2b/scriggo"
	"github.com/open2b/scriggo/internal/fstest"
	"github.com/open2b/scriggo/native"
)

type methodStringer interface {
	String() string
}

type methodPtrOnly interface {
	Mutate()
}

func runProgram(t *testing.T, src string, packages native.Importer) string {
	t.Helper()
	fsys := fstest.Files{"main.go": src}
	opts := &scriggo.BuildOptions{}
	if packages != nil {
		opts.Packages = packages
	}
	program, err := scriggo.Build(fsys, opts)
	if err != nil {
		t.Fatalf("build error: %s", err)
	}
	var buf bytes.Buffer
	err = program.Run(&scriggo.RunOptions{
		Print: func(v interface{}) { fmt.Fprint(&buf, v) },
	})
	if err != nil {
		t.Fatalf("run error: %s", err)
	}
	return buf.String()
}

func TestMethodDeclarations(t *testing.T) {
	src := `
		package main

		type T int
		type U string
		type S struct { N int }
		type L []int

		func (t T) Value() int { return int(t) + 1 }
		func (t *T) Ptr() int  { return int(*t) + 10 }
		func (T) Unnamed() int { return 7 }
		func (u U) Value() string { return string(u) + "!" }
		func (s S) Value() int { return s.N * 2 }
		func (l L) Value() int { return len(l) }

		func main() {
			var t T = 3
			print(t.Value())
			print(" ")
			print(t.Ptr())
			print(" ")
			print(t.Unnamed())
			print(" ")
			var u U = "hi"
			print(u.Value())
			print(" ")
			print(S{N: 4}.Value())
			print(" ")
			print(L{1, 2, 3}.Value())
		}
	`
	got := runProgram(t, src, nil)
	const want = "4 13 7 hi! 8 3"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestMethodAutoAddress(t *testing.T) {
	src := `
		package main

		type T int

		func (t *T) Inc() {
			*t++
		}

		func main() {
			var t T = 5
			t.Inc()
			print(int(t))
		}
	`
	got := runProgram(t, src, nil)
	if got != "6" {
		t.Fatalf("got %q, want %q", got, "6")
	}
}

func TestMethodIndependence(t *testing.T) {
	src := `
		package main

		type A int
		type B int

		func (a A) Name() string { return "A" }
		func (b B) Name() string { return "B" }

		func main() {
			print(A(0).Name())
			print(B(0).Name())
		}
	`
	got := runProgram(t, src, nil)
	if got != "AB" {
		t.Fatalf("got %q, want %q", got, "AB")
	}
}

func TestMethodExpressions(t *testing.T) {
	src := `
		package main

		type T int

		func (t T) Value() int { return int(t) + 1 }
		func (t *T) Ptr() int  { return int(*t) + 10 }

		func main() {
			var t T = 3
			f := T.Value
			print(f(t))
			print(" ")
			print(T.Value(t))
			print(" ")
			g := (*T).Ptr
			print(g(&t))
			print(" ")
			print((*T).Ptr(&t))
			print(" ")
			h := (*T).Value
			print(h(&t))
		}
	`
	got := runProgram(t, src, nil)
	const want = "4 4 13 13 4"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestMethodExpressionPointerReceiverOnValueType(t *testing.T) {
	src := `
		package main

		type T int

		func (t *T) Ptr() int { return 0 }

		func main() {
			_ = T.Ptr
		}
	`
	fsys := fstest.Files{"main.go": src}
	_, err := scriggo.Build(fsys, nil)
	if err == nil {
		t.Fatal("expected build error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "needs pointer receiver") && !strings.Contains(msg, "has no method") {
		t.Fatalf("unexpected error: %s", msg)
	}
}

func TestMethodValues(t *testing.T) {
	src := `
		package main

		type T int

		func (t T) Value() int { return int(t) + 1 }
		func (t *T) Ptr() int  { return int(*t) + 10 }

		func main() {
			var t T = 3
			f := t.Value
			print(f())
			print(" ")
			g := t.Ptr
			print(g())
		}
	`
	got := runProgram(t, src, nil)
	if got != "4 13" {
		t.Fatalf("got %q, want %q", got, "4 13")
	}
}

func TestInterfaceSatisfaction(t *testing.T) {
	packages := native.Packages{
		"iface": native.Package{
			Name: "iface",
			Declarations: native.Declarations{
				"Stringer": reflect.TypeOf((*methodStringer)(nil)).Elem(),
			},
		},
	}
	src := `
		package main

		import "iface"

		type T int

		func (t T) String() string { return "ok" }

		func main() {
			var s iface.Stringer = T(1)
			print(s.String())
			print(" ")
			t := T(2)
			s = t
			print(s.String())
		}
	`
	got := runProgram(t, src, packages)
	if got != "ok ok" {
		t.Fatalf("got %q, want %q", got, "ok ok")
	}
}

func TestPointerReceiverInterface(t *testing.T) {
	packages := native.Packages{
		"iface": native.Package{
			Name: "iface",
			Declarations: native.Declarations{
				"PtrOnly": reflect.TypeOf((*methodPtrOnly)(nil)).Elem(),
			},
		},
	}

	t.Run("value does not implement", func(t *testing.T) {
		src := `
			package main

			import "iface"

			type T int

			func (t *T) Mutate() { *t = 1 }

			func main() {
				var i iface.PtrOnly = T(0)
				_ = i
			}
		`
		fsys := fstest.Files{"main.go": src}
		_, err := scriggo.Build(fsys, &scriggo.BuildOptions{Packages: packages})
		if err == nil {
			t.Fatal("expected build error")
		}
	})

	t.Run("pointer implements and dispatches", func(t *testing.T) {
		src := `
			package main

			import "iface"

			type T int

			func (t *T) Mutate() { *t = 9 }

			func main() {
				t := T(0)
				var i iface.PtrOnly = &t
				i.Mutate()
				print(int(t))
			}
		`
		got := runProgram(t, src, packages)
		if got != "9" {
			t.Fatalf("got %q, want %q", got, "9")
		}
	})
}

func TestMethodOnPointerValue(t *testing.T) {
	src := `
		package main

		type T int

		func (t T) Value() int { return int(t) + 1 }

		func main() {
			t := T(4)
			p := &t
			print(p.Value())
		}
	`
	got := runProgram(t, src, nil)
	if got != "5" {
		t.Fatalf("got %q, want %q", got, "5")
	}
}
