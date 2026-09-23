package checker_test

import (
	"bufio"
	. "io"
	"net/http"
	str "strings"
)

var _ = bufio.NewReader
var _ = http.Get
var _ = str.NewReader
var _ Reader

type localType struct {
	Field int
}

func (localType) Method() {}

// LocalRefs refers to [Base], [Derived], [PlainFunc] and [*Derived].
func LocalRefs() {}

// MemberRefs refers to [Derived.Own], [Derived.Method], [*Derived.Method],
// [Derived.Embedded] and [Derived.BaseMethod].
func MemberRefs() {}

// LowerTypeRefs refers to [localType], [localType.Field] and [localType.Method].
func LowerTypeRefs() {}

// QualifiedRefs refers to [bufio.Reader], [bufio.Reader.ReadString],
// [bufio.ReadWriter.ReadString], [http.Client.Do] and [net/http.Client].
func QualifiedRefs() {}

// RenamedRefs refers to [str.Builder] and [str.Builder.WriteString].
func RenamedRefs() {}

// DotImportRefs refers to [Writer], [EOF] and [ReadCloser.Close].
func DotImportRefs() {}

// BuiltinRefs refers to [error], [any], [int], [len], [error.Error] and [comparable].
func BuiltinRefs() {}

// PackageRefs refers to [bufio], [str], [http], [net/http], [fmt] and [encoding/json].
func PackageRefs() {}

// SelfQualifiedRefs refers to [checker_test.Base] and [checker_test.Derived.Own].
func SelfQualifiedRefs() {}

// NotLinks has [some text], [a-b], [1], [], [T.], [.T], [type], x[Foo] and [Foo]x.
func NotLinks() {}

// URLLinks refers to [Some Page] and [Other].
//
// [Some Page]: https://example.com
// [Other]: https://example.com/other
func URLLinks() {}

// CodeBlock:
//
//	a[NoSuchThing] = [NoSuchThing2]
func CodeBlock() {}

func localFunc() {
	// LocalDocs are not checked: [NoSuchThing].
	type localDef int
}
