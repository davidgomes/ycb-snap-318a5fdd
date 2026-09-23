// Package checker_test exercises doc links that should not be reported.
//
// Package comments are ignored, including this broken link [NotARealSymbol].
package checker_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	fmt2 "fmt"

	. "io"
)

var (
	_ = fmt.Sprintf
	_ = fmt2.Sprintf
	_ = json.Marshal
	_ = bytes.MinRead
	_ = EOF
	_ = Copy
)

// Widget is a struct. See [Widget.Name], [Widget.ID], [Widget.Greet], and [Widget.Reset].
type Widget struct {
	// Name is a field of [Widget].
	Name string

	Inner
}

// Inner is embedded in [Widget].
type Inner struct {
	// ID is promoted through [Widget].
	ID int
}

// Greet is a value-receiver method of [Widget].
func (Widget) Greet() {}

// Reset is a pointer-receiver method of [Widget].
func (*Widget) Reset() {}

// Helper is referenced as a non-type in positive tests.
func Helper() {}

// Answer is a constant.
const Answer = 42

// Global is a variable.
var Global int

// Box is generic. See [Box.Get], [Box.Set], [Box.Value], and [Box.ID].
type Box[T any] struct {
	Value T
	Inner
}

// Get returns the zero value.
func (Box[T]) Get() T {
	var zero T
	return zero
}

// Set is a pointer-receiver method.
func (*Box[T]) Set(T) {}

// Alias aliases [bytes.Buffer]. See [Alias.Bytes].
type Alias = bytes.Buffer

// ReaderIface embeds the dot-imported [Reader]. See [ReaderIface.Read].
type ReaderIface interface {
	Reader
}

// L1 is the base of an embedding chain.
type L1 struct{ N int }

// L2 embeds [L1].
type L2 struct{ L1 }

// L3 embeds [L2]. See [L3.N].
type L3 struct{ L2 }

// L4 embeds a pointer. See [L4.N].
type L4 struct{ *L1 }

// StringerWrap embeds [fmt.Stringer]. See [StringerWrap.String].
type StringerWrap struct{ fmt.Stringer }

// ValidLinks lists references that resolve.
//
// Current package: [checker_test.Widget].
// Imports: [fmt.Println], [fmt2.Sprintf], [fmt2.Stringer.String], [json.Marshal], [encoding/json.Decoder].
// Dot imports: [Reader], [Reader.Read], [Copy], [EOF].
// Stars: [*Widget.Reset], [*bytes.Buffer].
// Builtins: [int], [error], [len], [string], [bool], [true], [nil], [append], [any], [comparable].
// Prose and non-identifiers: [note], [sic], [not a link], [foo+bar], [123abc].
// Not a link: map[MissingType]int and a [Size]byte.
//
// See the [docs].
//
// [docs]: https://example.com
func ValidLinks() {}

// CodeBlock contains a snippet that must not be scanned for links.
//
//	m := map[MissingCode]int{}
func CodeBlock() {}

// Heading documents that heading brackets are not symbol links.
//
// # See also [NoSuchHeading]
func Heading() {}

// Listed items can mention real symbols: [Widget].
//
//   - [Reader.Read]
//   - [fmt.Println]
func Listed() {}
