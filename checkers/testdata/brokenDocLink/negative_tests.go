package checker_test

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
)

var (
	_ = fmt.Sprintf
	_ io.Reader
	_ fs.FS
)

// See [PlainFunc], [Documented], [Documented.Method], [Documented.PtrMethod],
// [Documented.Field], [Documented.Embedded], [Documented.M], [Inner.M],
// [Alias.PtrMethod], [GenericBox.Get], [ReaderIface.Read], [fmt.Println],
// [fmt.Stringer], [io.Reader], [io.Reader.Read], [io.Writer.Write],
// [bytes.Buffer], [bytes.Buffer.Write], [io/fs.FS], [io/fs.FS.Open] and [fmt].
func ValidLinks() {}

// BufWrapOK embeds [bytes.Buffer]. See [BufWrapOK.Write] and [BufWrapOK.Bytes].
type BufWrapOK struct {
	bytes.Buffer
}

// IOEmbed promotes [io.Reader]. See [IOEmbed.Read].
type IOEmbed interface {
	io.Reader
}

// PtrOuter promotes [Inner] fields. See [PtrOuter.Embedded] and [PtrOuter.M].
type PtrOuter struct {
	*Inner
}

// Not a symbol link: [Not A Symbol], [Not-A-Symbol], [foo$bar] and map[string]int.
func NonLinks() {}

// Builtins [int], [string], [len], [error], [error.Error], [any], [byte] and [rune].
func BuiltinLinks() {}

// See the [docs].
//
// [docs]: https://go.dev/doc/comment
func LinkDefinition() {}

// Code sample:
//
//	[TotallyMissingSymbol]
func CodeBlock() {}

// [fmt.Printf] returns nothing useful here.
func FmtPrintf() {}

// HeadingOK mentions a real symbol.
//
// # See [PlainFunc]
func HeadingOK() {}
