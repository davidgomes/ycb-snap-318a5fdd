package misc

import (
	"reflect"
	"strings"
	"testing"

	"github.com/open2b/scriggo"
	"github.com/open2b/scriggo/internal/fstest"
	"github.com/open2b/scriggo/native"
)

func runPrinted(t *testing.T, src string, packages native.Importer) string {
	t.Helper()
	fsys := fstest.Files{"main.go": src}
	program, err := scriggo.Build(fsys, &scriggo.BuildOptions{Packages: packages})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	err = program.Run(&scriggo.RunOptions{
		Print: func(v interface{}) {
			b.WriteString(printed(v))
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func printed(v interface{}) string {
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return itoa(int(r.Int()))
	case reflect.String:
		return r.String()
	default:
		return r.Kind().String()
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func TestUserMethods(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{
			name: "value method",
			src: `
package main
type Int int
func (i Int) Double() int { return int(i) * 2 }
func main() {
	var i Int = 3
	print(i.Double())
}
`,
			want: "6",
		},
		{
			name: "unnamed receiver",
			src: `
package main
type Int int
func (Int) Const() int { return 8 }
func main() {
	var i Int
	print(i.Const())
}
`,
			want: "8",
		},
		{
			name: "convert defined int",
			src: `
package main
type Int int
func main() {
	var i Int = 3
	print(int(i))
}
`,
			want: "3",
		},
		{
			name: "pointer receiver explicit",
			src: `
package main
type Int int
func (i *Int) Inc() { *i = *i + 1 }
func main() {
	var i Int = 3
	p := &i
	p.Inc()
	print(int(i))
}
`,
			want: "4",
		},
		{
			name: "pointer receiver auto address",
			src: `
package main
type Int int
func (i *Int) Inc() { *i = *i + 1 }
func main() {
	var i Int = 3
	i.Inc()
	print(int(i))
}
`,
			want: "4",
		},
		{
			name: "same name different types",
			src: `
package main
type A int
type B int
func (A) N() int { return 1 }
func (B) N() int { return 2 }
func main() {
	var a A
	var b B
	print(a.N())
	print(b.N())
}
`,
			want: "12",
		},
		{
			name: "method expression value",
			src: `
package main
type T int
func (t T) Value() int { return int(t) + 1 }
func main() {
	var t T = 10
	print(T.Value(t))
	f := T.Value
	print(f(t))
}
`,
			want: "1111",
		},
		{
			name: "method expression pointer",
			src: `
package main
type T int
func (t *T) Ptr() int { return int(*t) + 2 }
func main() {
	var t T = 10
	print((*T).Ptr(&t))
	g := (*T).Ptr
	print(g(&t))
}
`,
			want: "1212",
		},
		{
			name: "method value",
			src: `
package main
type T int
func (t T) Value() int { return int(t) + 1 }
func main() {
	var t T = 10
	h := t.Value
	print(h())
}
`,
			want: "11",
		},
		{
			name: "pointer method value auto address",
			src: `
package main
type T int
func (t *T) Ptr() int { return int(*t) + 2 }
func main() {
	var t T = 10
	p := t.Ptr
	print(p())
}
`,
			want: "12",
		},
		{
			name: "struct slice string",
			src: `
package main
type Pair struct{ A, B int }
type Slice []int
type Str string
func (p Pair) Sum() int { return p.A + p.B }
func (s Slice) First() int { return s[0] }
func (s Str) Len() int { return len(s) }
func main() {
	print(Pair{2, 5}.Sum())
	print(Slice{7, 8}.First())
	print(Str("abcd").Len())
}
`,
			want: "774",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runPrinted(t, tc.src, nil)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestMethodExpressionNeedsPointer(t *testing.T) {
	src := `
package main

type T int

func (t *T) Ptr() {}

func main() {
	_ = T.Ptr
}
`
	fsys := fstest.Files{"main.go": src}
	_, err := scriggo.Build(fsys, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid method expression T.Ptr") {
		t.Fatalf("unexpected error %v", err)
	}
}

type testIface interface {
	M()
}

func TestPointerReceiverDoesNotSatisfyValueInterface(t *testing.T) {
	src := `
package main

type I interface { M() }

type P int

func (p *P) M() {}

func main() {
	var p P
	var i I = p
	_ = i
}
`
	fsys := fstest.Files{"main.go": src}
	_, err := scriggo.Build(fsys, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "does not implement") && !strings.Contains(err.Error(), "missing method") {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestInterfaceMethodDispatch(t *testing.T) {
	var calls []string
	packages := native.Packages{
		"iface": native.Package{
			Name: "iface",
			Declarations: native.Declarations{
				"I":      reflect.TypeOf((*testIface)(nil)).Elem(),
				"Record": func(s string) { calls = append(calls, s) },
			},
		},
	}
	src := `
package main

import "iface"

type V int
func (v V) M() { iface.Record("V") }

type P int
func (p *P) M() { iface.Record("P") }

func main() {
	var v V
	var i iface.I = v
	i.M()
	var p P
	var j iface.I = &p
	j.M()
}
`
	_ = runPrinted(t, src, packages)
	if got := strings.Join(calls, ","); got != "V,P" {
		t.Fatalf("got calls %q", got)
	}
}
