// Copyright 2019 The Scriggo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package compiler

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/open2b/scriggo/internal/fstest"
	"github.com/open2b/scriggo/native"
)

var methodsTestImporter = native.Packages{
	"fmt": native.Package{
		Name: "fmt",
		Declarations: native.Declarations{
			"Stringer": reflect.TypeOf((*fmt.Stringer)(nil)).Elem(),
		},
	},
}

func TestMethodDeclarations(t *testing.T) {
	cases := []struct {
		src string
		err string
	}{
		// Receivers.
		{`type T int; func (t T) M() {}; func (t *T) N() {}; func (T) O() {}; func (*T) P() {}`, ""},
		{`type T struct{}; func (t T) M() int { return 1 }; func (t T) N() int { return t.M() }`, ""},
		{`type T int; type A = T; func (a A) M() {}`, ""},
		{`type T int; func (T) _() {}; func (T) _() {}`, ""},
		{`type T int; func (T) init() {}; func (T) main() {}`, ""},
		{`func (i int) M() {}`, "3:9: cannot define new methods on non-local type int"},
		{`import "fmt"; func (s fmt.Stringer) M() {}`, "3:27: cannot define new methods on non-local type fmt.Stringer"},
		{`type P *int; func (P) M() {}`, "3:20: invalid receiver type P (pointer or interface type)"},
		{`type I interface{}; func (I) M() {}`, "3:27: invalid receiver type I (pointer or interface type)"},
		{`type T int; func (**T) M() {}`, "3:19: invalid receiver type **T"},
		{`type T int; func ([]T) M() {}`, "3:19: invalid receiver type []T"},
		{`type T int; func (T) M(); func main() {}`, "3:22: missing function body"},
		{`type T int; func (x T) M(x int) {}`, "3:26: duplicate argument x"},
		{`type T int; func (T) M() {}; func (*T) M() {}`, "3:40: method T.M already declared at 3:22"},
		{`type T struct{ M int }; func (T) M() {}`, "3:34: field and method with the same name M"},
		{`type T struct{ m int }; func (T) m() {}`, "3:34: field and method with the same name m"},

		// Method calls and values.
		{`type T int; func (t *T) M() {}; func f() { var t T; t.M(); (&t).M(); f := t.M; f() }`, ""},
		{`type T int; func (t T) M() {}; func f() { var p *T; p.M(); f := p.M; f() }`, ""},
		{`type T int; func (t *T) M() {}; func f() { T(1).M() }`, "3:48: cannot call pointer method M on T"},
		{`type T int; func (t *T) M() {}; func f() { _ = T(1).M }`, "3:52: cannot call pointer method M on T"},
		{`type T int; func (t T) M() {}; func f() { var t T; t.N() }`, "3:53: t.N undefined (type T has no field or method N)"},
		{`type T int; func (t T) M(a int) string { return "" }; func f() { var t T; var s string = t.M(1); _ = s }`, ""},
		{`type T int; func (t T) M(a int) string { return "" }; func f() { var t T; t.M("a") }`, "3:78: cannot use \"a\" (type untyped string) as type int in argument to t.M"},

		// Method expressions.
		{`type T int; func (t T) M(a int) int { return a }; func f() { _ = T.M(T(1), 2); _ = (*T).M(new(T), 2) }`, ""},
		{`type T int; func (t *T) M() {}; func f() { g := (*T).M; g(new(T)) }`, ""},
		{`type T int; func (t *T) M() {}; func f() { _ = T.M }`, "3:49: invalid method expression T.M (needs pointer receiver: (*T).M)"},
		{`type T int; func (t T) M() {}; func f() { _ = T.N }`, "3:48: T.N undefined (type T has no method N)"},
		{`type T int; func (t T) M(a int) {}; func f() { var g func(T, int) = T.M; _ = g }`, ""},
		{`type T int; func (t T) M(a int) {}; func f() { var g func(int) = T.M; _ = g }`, "cannot use T.M (type func(T, int) ) as type func(int) in assignment"},

		// Promoted methods.
		{`type E int; func (E) M() {}; func (*E) P() {}; type S struct{ E }; func f() { var s S; s.M(); s.P(); S{}.M(); g := s.P; g() }`, ""},
		{`type E int; func (*E) P() {}; type S struct{ *E }; func f() { S{}.P() }`, ""},
		{`type E int; func (*E) P() {}; type S struct{ E }; func f() { S{}.P() }`, "3:65: cannot call pointer method P on S"},
		{`type A int; func (A) M() {}; type B int; func (B) M() {}; type S struct{ A; B }; func f() { var s S; s.M() }`, "3:103: ambiguous selector s.M"},
		{`type A int; func (A) M() {}; type S struct{ A; M int }; func f() { var s S; s.M = 1 }`, ""},
		{`import "fmt"; type E int; func (E) String() string { return "" }; type S struct{ E }; var _ fmt.Stringer = S{}`, ""},
		{`import "fmt"; type E int; func (*E) String() string { return "" }; type S struct{ E }; var _ fmt.Stringer = &S{}`, ""},
		{`import "fmt"; type E int; func (*E) String() string { return "" }; type S struct{ *E }; var _ fmt.Stringer = S{}`, ""},
		{`import "fmt"; type E int; func (*E) String() string { return "" }; type S struct{ E }; var _ fmt.Stringer = S{}`, "cannot use S{} (type S) as type fmt.Stringer in assignment"},

		// Interfaces.
		{`import "fmt"; type T int; func (T) String() string { return "" }; var _ fmt.Stringer = T(1)`, ""},
		{`import "fmt"; type T int; func (*T) String() string { return "" }; var _ fmt.Stringer = new(T)`, ""},
		{`import "fmt"; type T int; func (*T) String() string { return "" }; var _ fmt.Stringer = T(1)`, "3:90: cannot use T(1) (type T) as type fmt.Stringer in assignment"},
		{`import "fmt"; type T int; func (T) String() int { return 0 }; var _ fmt.Stringer = T(1)`, "3:85: cannot use T(1) (type T) as type fmt.Stringer in assignment"},
		{`import "fmt"; type T int; var _ fmt.Stringer = T(1)`, "3:49: cannot use T(1) (type T) as type fmt.Stringer in assignment"},
		{`import "fmt"; type T int; func (*T) String() string { return "" }; func f(s fmt.Stringer) { _ = s.(T) }`, "impossible type assertion:\n\tT does not implement fmt.Stringer (String method has pointer receiver)"},
		{`import "fmt"; type T int; func (T) String() string { return "" }; func f(s fmt.Stringer) { _ = s.(T); _ = s.(*T) }`, ""},
	}
	for _, cas := range cases {
		t.Run(cas.src, func(t *testing.T) {
			src := "package main\n\n" + cas.src
			if !strings.Contains(cas.src, "func main()") {
				src += "\nfunc main() {}"
			}
			fsys := fstest.Files{"main.go": src}
			_, err := BuildProgram(fsys, Options{Importer: methodsTestImporter})
			if err == nil {
				if cas.err != "" {
					t.Fatalf("expecting error %q, got no error", cas.err)
				}
				return
			}
			if cas.err == "" {
				t.Fatalf("unexpected error %q", err)
			}
			if got := err.Error(); !strings.Contains(got, cas.err) {
				t.Fatalf("expecting error %q, got %q", cas.err, got)
			}
		})
	}
}

func TestMethodsOfImportedTypes(t *testing.T) {
	pkg := "package pkg\n\ntype T int\n\nfunc (T) Exported() {}\nfunc (T) unexported() {}\nfunc (*T) Pointer() {}\n"
	cases := []struct {
		src string
		err string
	}{
		{`var t pkg.T; t.Exported(); t.Pointer(); pkg.T.Exported(t); (*pkg.T).Pointer(&t)`, ""},
		{`var t pkg.T; t.unexported()`, "5:29: t.unexported undefined (cannot refer to unexported field or method unexported)"},
		{`_ = pkg.T.unexported`, "5:24: pkg.T.unexported undefined (cannot refer to unexported field or method unexported)"},
	}
	for _, cas := range cases {
		t.Run(cas.src, func(t *testing.T) {
			fsys := fstest.Files{
				"go.mod":     "module example.com/m",
				"pkg/pkg.go": pkg,
				"main.go":    "package main\n\nimport \"example.com/m/pkg\"\n\nfunc main() { " + cas.src + " }",
			}
			_, err := BuildProgram(fsys, Options{})
			if err == nil {
				if cas.err != "" {
					t.Fatalf("expecting error %q, got no error", cas.err)
				}
				return
			}
			if cas.err == "" {
				t.Fatalf("unexpected error %q", err)
			}
			if got := err.Error(); !strings.Contains(got, cas.err) {
				t.Fatalf("expecting error %q, got %q", cas.err, got)
			}
		})
	}
}
