package checker_test

import (
	"bytes"
	"fmt"

	fmt2 "fmt"
	. "github.com/go-critic/go-critic/checkers/testdata/_importable/strings"

	"encoding/json"
)

// Local symbol [Documented] is fine.
func Documented() {}

// [Documented] is referenced again.
var DocumentedVar = 1

// Type [Named] and method [Named.Method] and field [Named.Field].
type Named struct {
	// Field is a documented field. See [Named].
	Field int
}

// Method is a value method. See [Named.Field].
func (Named) Method() {}

// PtrMethod is a pointer method. See [Named.PtrMethod].
func (*Named) PtrMethod() {}

type inner struct {
	N int
}

func (inner) M() {}

func (*inner) P() {}

type mid struct{ inner }

// Promoted members [Outer.N], [Outer.M] and [Outer.P].
type Outer struct{ *mid }

// Alias keeps members [Alias.Method].
type Alias = Named

type I interface {
	// M is documented. See [I.M].
	M()
}

// Qualified links [fmt.Printf], [bytes.Buffer], [bytes.Buffer.Bytes],
// [fmt2.Sprintf] and [json.Marshal].
func Qualified(buf *bytes.Buffer) {
	fmt.Print(buf)
	fmt2.Print(buf)
	_ = json.Marshal
}

// Full import path [encoding/json.Marshal] and package link [fmt].
func FullPath() {}

// Dot-imported [Contains] counts as a local symbol.
func DotImported() { Contains() }

// Builtins are not reported: [error], [string], [len], [true], [nil].
func Builtins() error { return nil }

// Not symbol links: [not a link], [foo bar], [123], [a+b], map-like text.
func NotLinks() {}

// Code span `[Missing]` is not a link.
func CodeSpan() {}

// Example introduces a code block:
//
//	[AlsoNotALink]
func CodeBlock() {}

// [fmt2] names the renamed import.
func RenamedPackage() {}
