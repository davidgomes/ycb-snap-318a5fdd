// Copyright 2026 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"sort"
	"testing"

	"github.com/open2b/scriggo"
	"github.com/open2b/scriggo/internal/fstest"
	"github.com/open2b/scriggo/native"
)

// Namer is a Go interface implemented, in the tests, by Scriggo types.
type Namer interface {
	Name() string
}

// methodsPackages returns the packages that can be imported by the programs
// of the methods tests. The "out" package writes to out.
func methodsPackages(out *bytes.Buffer) native.Packages {
	return native.Packages{
		"fmt": native.Package{
			Name: "fmt",
			Declarations: native.Declarations{
				"Errorf":   fmt.Errorf,
				"Sprint":   fmt.Sprint,
				"Sprintf":  fmt.Sprintf,
				"Stringer": reflect.TypeOf((*fmt.Stringer)(nil)).Elem(),
			},
		},
		"errors": native.Package{
			Name: "errors",
			Declarations: native.Declarations{
				"New":    errors.New,
				"Unwrap": errors.Unwrap,
			},
		},
		"sort": native.Package{
			Name: "sort",
			Declarations: native.Declarations{
				"Interface": reflect.TypeOf((*sort.Interface)(nil)).Elem(),
				"Sort":      sort.Sort,
			},
		},
		"out": native.Package{
			Name: "out",
			Declarations: native.Declarations{
				"Namer":     reflect.TypeOf((*Namer)(nil)).Elem(),
				"Namers":    reflect.TypeOf([]Namer{}),
				"Println":   func(a ...interface{}) { _, _ = fmt.Fprintln(out, a...) },
				"StoreName": func(n Namer) []Namer { return []Namer{n} },
			},
		},
	}
}

// runMethodsProgram builds and runs the program in fsys and returns its
// output.
func runMethodsProgram(fsys fs.FS) (string, error) {
	var out bytes.Buffer
	program, err := scriggo.Build(fsys, &scriggo.BuildOptions{Packages: methodsPackages(&out), AllowGoStmt: true})
	if err != nil {
		return "", err
	}
	err = program.Run(nil)
	return out.String(), err
}

var methodsTests = []struct {
	name string
	src  string
	out  string
}{
	{
		name: "value and pointer receivers",
		src: `package main
		import "out"
		type Counter struct{ n int }
		func (c *Counter) Inc() { c.n++ }
		func (c Counter) Get() int { return c.n }
		func (c Counter) Reset() { c.n = 0 }
		func (Counter) Kind() string { return "counter" }
		func (*Counter) PtrKind() string { return "*counter" }
		func main() {
			var c Counter
			c.Inc()
			c.Inc()
			c.Reset()
			p := &c
			p.Inc()
			out.Println(c.Get(), p.Get(), c.Kind(), c.PtrKind(), p.Kind())
		}`,
		out: "3 3 counter *counter counter\n",
	},
	{
		name: "methods on all definable types",
		src: `package main
		import "out"
		type Int int
		func (i Int) Double() int { return int(i) * 2 }
		type Str string
		func (s Str) Len() int { return len(s) }
		type Slice []int
		func (s *Slice) Push(v ...int) { *s = append(*s, v...) }
		func (s Slice) Len() int { return len(s) }
		type Map map[string]int
		func (m Map) Set(k string, v int) { m[k] = v }
		type Array [2]int
		func (a Array) Sum() int { return a[0] + a[1] }
		type Func func() int
		func (f Func) Call() int { return f() }
		type Chan chan int
		func (c Chan) Cap() int { return cap(c) }
		type Struct struct{ A, B int }
		func (s Struct) Sum() int { return s.A + s.B }
		type Bool bool
		func (b Bool) Not() bool { return !bool(b) }
		type Float float64
		func (f Float) Half() float64 { return float64(f) / 2 }
		func main() {
			s := Slice{1}
			s.Push(2, 3)
			m := Map{}
			m.Set("a", 1)
			out.Println(Int(2).Double(), Str("abc").Len(), s.Len(), len(m), Array{1, 2}.Sum(),
				Func(func() int { return 7 }).Call(), make(Chan, 3).Cap(), Struct{1, 2}.Sum(),
				Bool(false).Not(), Float(3).Half())
		}`,
		out: "4 3 3 1 3 7 3 3 true 1.5\n",
	},
	{
		name: "methods with the same name on different types",
		src: `package main
		import "out"
		type A struct{}
		type B struct{ n int }
		type C int
		func (A) Who() string { return "A" }
		func (b *B) Who() string { return "B" }
		func (c C) Who() string { return "C" }
		func Who() string { return "func" }
		func main() {
			out.Println(A{}.Who(), (&B{}).Who(), C(0).Who(), Who())
			fs := []func() string{A{}.Who, (&B{}).Who, C(0).Who}
			for _, f := range fs {
				out.Println(f())
			}
		}`,
		out: "A B C func\nA\nB\nC\n",
	},
	{
		name: "method values",
		src: `package main
		import "out"
		type T struct{ n int }
		func (t *T) Inc() { t.n++ }
		func (t T) Get() int { return t.n }
		func main() {
			var t T
			inc := t.Inc
			inc()
			inc()
			get := t.Get
			t.n = 10
			out.Println(t.n, get(), t.Get())
			defer func() { out.Println("deferred", t.Get()) }()
			defer t.Inc()
		}`,
		out: "10 2 10\ndeferred 11\n",
	},
	{
		name: "method expressions",
		src: `package main
		import "out"
		type T struct{ n int }
		func (t T) Get() int { return t.n }
		func (t T) Add(d int) int { return t.n + d }
		func (t *T) Set(n int) { t.n = n }
		func apply(f func(T, int) int, t T) int { return f(t, 1) }
		func main() {
			t := T{1}
			get := T.Get
			set := (*T).Set
			pget := (*T).Get
			set(&t, 5)
			out.Println(get(t), pget(&t), T.Get(t), (*T).Get(&t), apply(T.Add, t))
			m := map[string]func(T) int{"get": T.Get}
			out.Println(m["get"](T{9}))
		}`,
		out: "5 5 5 5 6\n9\n",
	},
	{
		name: "interfaces",
		src: `package main
		import (
			"fmt"
			"out"
		)
		type Celsius float64
		func (c Celsius) String() string { return fmt.Sprintf("%.1fC", float64(c)) }
		type Point struct{ X, Y int }
		func (p *Point) String() string { return fmt.Sprint(p.X, ",", p.Y) }
		type Name string
		func (n Name) Name() string { return string(n) }
		func show(s fmt.Stringer) string { return "<" + s.String() + ">" }
		var global fmt.Stringer = Celsius(1)
		func main() {
			var s fmt.Stringer = Celsius(2)
			out.Println(s.String(), show(&Point{1, 2}), global.String())
			var v interface{} = Point{3, 4}
			_, ok := v.(fmt.Stringer)
			v = &Point{3, 4}
			st, ok2 := v.(fmt.Stringer)
			out.Println(ok, ok2, st.String())
			switch v := interface{}(Celsius(3)).(type) {
			case fmt.Stringer:
				out.Println("stringer", v.String())
			}
			list := []fmt.Stringer{Celsius(4), &Point{5, 6}}
			out.Println(list[0].String(), list[1].String())
			out.Println(Celsius(7), &Point{8, 9})
			var n out.Namer = Name("scriggo")
			stored := out.StoreName(n)
			out.Println(n.Name(), stored[0].Name(), len(out.Namers{n}))
		}`,
		out: "2.0C <1,2> 1.0C\nfalse true 3,4\nstringer 3.0C\n4.0C 5,6\n7.0C 8,9\nscriggo scriggo 1\n",
	},
	{
		name: "errors",
		src: `package main
		import (
			"errors"
			"fmt"
			"out"
		)
		type NotFound struct{ Key string }
		func (e *NotFound) Error() string { return e.Key + " not found" }
		func find(key string) error {
			if key == "" {
				return nil
			}
			return &NotFound{key}
		}
		func main() {
			err := find("k")
			out.Println(err.Error(), err, find("") == nil)
			w := fmt.Errorf("find: %w", err)
			out.Println(w)
			if nf, ok := errors.Unwrap(w).(*NotFound); ok {
				out.Println(nf.Key)
			}
			errs := []error{err, errors.New("other")}
			for _, e := range errs {
				out.Println(e.Error())
			}
		}`,
		out: "k not found k not found true\nfind: k not found\nk\nk not found\nother\n",
	},
	{
		name: "sort.Interface",
		src: `package main
		import (
			"out"
			"sort"
		)
		type ByLen []string
		func (s ByLen) Len() int { return len(s) }
		func (s ByLen) Less(i, j int) bool { return len(s[i]) < len(s[j]) }
		func (s ByLen) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
		func main() {
			words := ByLen{"ccc", "a", "bb"}
			var si sort.Interface = words
			out.Println(si.Len(), si.Less(0, 1))
			sort.Sort(words)
			out.Println([]string(words))
		}`,
		out: "3 false\n[a bb ccc]\n",
	},
}

func TestMethods(t *testing.T) {
	for _, test := range methodsTests {
		t.Run(test.name, func(t *testing.T) {
			out, err := runMethodsProgram(fstest.Files{"main.go": test.src})
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			if out != test.out {
				t.Fatalf("expected output %q, got %q", test.out, out)
			}
		})
	}
}

func TestMethodsInImportedPackage(t *testing.T) {
	fsys := fstest.Files{
		"go.mod": "module a.b\ngo 1.16",
		"main.go": `package main
		import (
			"a.b/p"
			"fmt"
			"out"
		)
		type T struct{ n int }
		func (t T) Get() int { return -t.n }
		func main() {
			v := p.T{N: 2}
			v.Double()
			var s fmt.Stringer = v
			out.Println(v.Get(), T{2}.Get(), s.String(), p.T.Get(v))
		}`,
		"p/p.go": `package p
		import "fmt"
		type T struct{ N int }
		func (t T) Get() int { return t.N }
		func (t *T) Double() { t.N *= 2 }
		func (t T) String() string { return fmt.Sprint("T", t.N) }`,
	}
	out, err := runMethodsProgram(fsys)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if expected := "4 -2 T4 4\n"; out != expected {
		t.Fatalf("expected output %q, got %q", expected, out)
	}
}

var methodErrorsTests = []struct {
	src string
	err string
}{
	{
		src: `type T int; func (t *T) M() {}; func main() { _ = T.M }`,
		err: "main:1:66: invalid method expression T.M (needs pointer receiver: (*T).M)",
	},
	{
		src: `type T int; func (t *T) M() {}; func main() { T(0).M() }`,
		err: "main:1:65: cannot call pointer method M on T",
	},
	{
		src: `import "fmt"; type T int; func (t *T) String() string { return "" }; func main() { var _ fmt.Stringer = T(0) }`,
		err: "main:1:120: cannot use T(0) (type T) as type fmt.Stringer in assignment:\n\tT does not implement fmt.Stringer (String method has pointer receiver)",
	},
	{
		src: `import "fmt"; type T int; func (t T) String() int { return 0 }; func main() { var _ fmt.Stringer = T(0) }`,
		err: "main:1:115: cannot use T(0) (type T) as type fmt.Stringer in assignment:\n\tT does not implement fmt.Stringer (wrong type for String method)\n\t\thave func() int\n\t\twant func() string",
	},
	{
		src: `func (i int) M() {}; func main() {}`,
		err: "main:1:23: cannot define new methods on non-local type int",
	},
	{
		src: `type T int; func (T) M() {}; func (*T) M() {}; func main() {}`,
		err: "main:1:54: method redeclared: T.M",
	},
	{
		src: `type S struct{ M int }; func (S) M() {}; func main() {}`,
		err: "main:1:48: type S has both field and method named M",
	},
	{
		src: `type P *int; func (P) M() {}; func main() {}`,
		err: "main:1:34: invalid receiver type P (pointer or interface type)",
	},
	{
		src: `type T int; func (t T) M(t int) {}; func main() {}`,
		err: "main:1:40: duplicate argument t",
	},
	{
		src: `type T int; func (T) M() {}; func main() { var t T; t.N() }`,
		err: "main:1:68: t.N undefined (type T has no field or method N)",
	},
}

func TestMethodErrors(t *testing.T) {
	for _, test := range methodErrorsTests {
		src := "package main; " + test.src
		_, err := runMethodsProgram(fstest.Files{"main.go": src})
		if err == nil {
			t.Errorf("source %q: expected error %q, got no error", src, test.err)
			continue
		}
		if err.Error() != test.err {
			t.Errorf("source %q: expected error %q, got %q", src, test.err, err)
		}
	}
}

func TestUnexportedMethodInImportedPackage(t *testing.T) {
	fsys := fstest.Files{
		"go.mod":  "module a.b\ngo 1.16",
		"main.go": `package main; import "a.b/p"; func main() { _ = p.T{}.m }`,
		"p/p.go":  `package p; type T struct{}; func (T) m() {}`,
	}
	_, err := runMethodsProgram(fsys)
	const expected = "main:1:54: p.T{}.m undefined (cannot refer to unexported field or method m)"
	if err == nil || err.Error() != expected {
		t.Fatalf("expected error %q, got %v", expected, err)
	}
}
