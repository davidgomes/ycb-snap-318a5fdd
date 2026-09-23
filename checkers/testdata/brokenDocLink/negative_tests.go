package checker_test

import (
	"bytes"
	"fmt"
	"io"

	. "errors"
	fmt2 "fmt"
)

var (
	_ = fmt.Println
	_ = fmt2.Sprintf
	_ = io.EOF
	_ = bytes.MinRead
	_ = New
)

// BrokenWidget is a documented type.
type BrokenWidget struct {
	Name string
}

// Start starts a widget.
func (BrokenWidget) Start() {}

// Listen listens. The pointer receiver must still be linkable as [BrokenServer.Listen].
type BrokenServer struct{}

func (s *BrokenServer) Listen() {}

// BrokenEmbed is embedded by BrokenHolder.
type BrokenEmbed struct {
	Count int
}

// Run runs.
func (BrokenEmbed) Run() {}

// BrokenHolder promotes members of BrokenEmbed.
type BrokenHolder struct {
	BrokenEmbed
}

// BrokenPtrHolder promotes members through a pointer embedding.
type BrokenPtrHolder struct {
	*BrokenEmbed
}

// BrokenInner is embedded two levels down.
type BrokenInner struct {
	Value int
}

// InnerMethod is promoted to BrokenOuter.
func (BrokenInner) InnerMethod() {}

// BrokenMiddle embeds BrokenInner.
type BrokenMiddle struct {
	BrokenInner
}

// BrokenOuter promotes BrokenInner members.
type BrokenOuter struct {
	BrokenMiddle
}

// BrokenCloser is embedded into an interface.
type BrokenCloser interface {
	Close() error
}

// BrokenReadCloser promotes Reader and Closer methods.
type BrokenReadCloser interface {
	io.Reader
	BrokenCloser
}

// BrokenBuf promotes bytes.Buffer methods.
type BrokenBuf struct {
	bytes.Buffer
}

// BrokenHelper is a function, not a type.
func BrokenHelper() {}

// BrokenPi is a constant, not a type.
const BrokenPi = 3

// BrokenKnown is referenced locally below.
const BrokenKnown = 1

// See [BrokenWidget], [BrokenWidget.Start], [BrokenWidget.Name], and [BrokenKnown].
func validLocalLinks() {}

// See [BrokenServer.Listen] and [*BrokenServer.Listen].
func validPointerReceiver() {}

// See [BrokenHolder.Count], [BrokenHolder.Run], [BrokenPtrHolder.Count], and [BrokenPtrHolder.Run].
func validEmbedded() {}

// See [BrokenOuter.Value] and [BrokenOuter.InnerMethod].
func validNestedEmbed() {}

// See [BrokenReadCloser.Read] and [BrokenReadCloser.Close].
func validInterfaceEmbed() {}

// See [BrokenBuf.WriteString].
func validImportedEmbed() {}

// See [fmt.Println], [fmt.Stringer], [fmt.Stringer.String], [io.Reader.Read], [io.EOF], and [fmt2.Sprintf].
func validQualified() {}

// See [New], [Is], and [Join] from the dot import.
func validDotImport() {}

// Builtins are not broken links: [error], [error.Error], [int], [string], [len], [true], and [nil].
func validBuiltins() {}

// Bracket text with spaces or other non-identifiers is not a link: [not a link], [foo-bar], [foo/bar].
// Neither is map[string]int or an indented example:
//
//	x := [NotChecked]
func notSymbolLinks(m map[string]int) {
	_ = m
}

// See the [Go home page].
//
// [Go home page]: https://go.dev
func validLinkDef() {}

/*
Block comment for [BrokenWidget.Start].
*/
func validBlockComment() {}

// A list:
//   - [BrokenWidget]
//   - [fmt2.Errorf]
func validList() {}

// See [checker_test.BrokenWidget] and [checker_test.BrokenKnown].
func validCurrentPackageName() {}

// BrokenBox is generic.
type BrokenBox[T any] struct {
	Value T
}

// Get returns the boxed value.
func (b BrokenBox[T]) Get() T { return b.Value }

// See [BrokenBox.Get] and [BrokenBox.Value].
func validGeneric() {}

// See [fmt], [io], and [fmt2].
func validPackageLinks() {}

// BrokenReader is an alias; its methods are still linkable.
type BrokenReader = io.Reader

// See [BrokenReader.Read].
func validAlias() {}
