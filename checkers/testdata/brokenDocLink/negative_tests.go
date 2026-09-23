package checker_test

import (
	// Imported for [json.Marshal].
	"encoding/json"
	"fmt"
	"io"
	. "math"
	strs "strings"
)

var (
	_ = json.Marshal
	_ = fmt.Sprint
	_ io.Reader
	_ = Pi
	_ = strs.ToUpper
)

type Inner struct {
	InnerField int
}

func (*Inner) InnerMethod() {}

type Outer struct {
	*Inner
	OuterField int
}

func (Outer) OuterMethod() {}

type Alias = Outer

type List[T any] struct {
	items []T
}

func (l *List[T]) Push(v T) { l.items = append(l.items, v) }

type Iface interface {
	io.Reader
	IfaceMethod()
}

var Value = 10

// LocalLinks refers to [Outer], [*Outer], [Outer.OuterField], [Outer.OuterMethod],
// [Value], [LocalLinks] and [DocType] from another file.
func LocalLinks() {}

// EmbeddedLinks refers to [Outer.InnerField], [Outer.InnerMethod] and [Inner.InnerMethod].
func EmbeddedLinks() {}

// GenericLinks refers to [List], [List.Push] and [List.items] is not a link.
func GenericLinks() {}

// AliasLinks refers to [Alias], [Alias.OuterMethod] and [Alias.InnerField].
func AliasLinks() {}

// IfaceLinks refers to [Iface.IfaceMethod] and [Iface.Read].
func IfaceLinks() {}

// QualifiedLinks refers to [fmt], [fmt.Println], [fmt.Stringer.String],
// [io.Reader.Read], [io.ReadWriter.Write] and [*io.PipeReader].
func QualifiedLinks() {}

// PathLinks refers to [encoding/json], [encoding/json.Marshal]
// and [encoding/json.Decoder.Decode].
func PathLinks() {}

// RenamedLinks refers to [strs], [strs.Builder] and [strs.Builder.WriteString].
func RenamedLinks() {}

// DotImportLinks refers to [Pi], [Sqrt] and [Sqrt] again.
func DotImportLinks() {}

// SelfLinks refers to [checker_test.Outer] and [checker_test.Outer.OuterMethod].
func SelfLinks() {}

// BuiltinLinks refers to [error], [string], [any], [comparable], [nil],
// [len], [append], [true] and [error.Error].
func BuiltinLinks() {}

// NotLinks has brackets that are not doc links: [some text], [Some Text],
// [a-b], [x+y], [1], [0, 1), [Foo bar.Baz], [type], [_], [], [Outer.field],
// map[string]int, []byte, x[i], s[Missing], [Missing]x.
func NotLinks() {}

// LinkDefs refers to [Missing] and [Go home page], defined as URLs below.
//
// [Missing]: https://example.com/missing
// [Go home page]: https://go.dev
func LinkDefs() {}

// CodeBlock has links inside a code block:
//
//	[Missing]
//	[fmt.Missing]
func CodeBlock() {}

// # Heading [Missing]
//
// Headings are not checked.
func Heading() {}

// Directives are ignored.
//
//lint:ignore U1000 see [Missing]
func Directives() {}

func localDecls() {
	// localType refers to [Missing], but function-local declarations are skipped.
	type localType int
	_ = localType(0)
}

// ValueDoc refers to [Outer.OuterField].
var ValueDoc = Outer{}.OuterField

type (
	// TypeSpecLinks refers to [Inner].
	TypeSpecLinks struct {
		// Field refers to [TypeSpecLinks.Field] and [TypeSpecLinks.Method].
		Field int
	}
)

// Method refers to [TypeSpecLinks.Method].
func (TypeSpecLinks) Method() {}
