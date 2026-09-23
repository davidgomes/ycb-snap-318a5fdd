package checker_test

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"

	// See [MissingOnImport].
	/*! [MissingOnImport]: unknown symbol "MissingOnImport" in current package */
	"strconv"
)

var (
	_ = fmt.Sprintf
	_ io.Reader
	_ fs.FS
	_ = strconv.Itoa
)

// See [MissingSym].
/*! [MissingSym]: unknown symbol "MissingSym" in current package */
func UsesMissing() {}

// See [fmt.NotAFmtSymbol].
/*! [fmt.NotAFmtSymbol]: "NotAFmtSymbol" not found in package "fmt" */
func UsesMissingFmt() {}

// See [MissingType.Method].
/*! [MissingType.Method]: type "MissingType" not found in current package */
func UsesMissingType() {}

// See [io.MissingType.Read].
/*! [io.MissingType.Read]: type "MissingType" not found in package "io" */
func UsesMissingIOType() {}

// See [Documented.NoSuch].
/*! [Documented.NoSuch]: type "Documented" has no method or field "NoSuch" */
func UsesMissingMember() {}

// See [io.Reader.NoSuch].
/*! [io.Reader.NoSuch]: type "Reader" has no method or field "NoSuch" */
func UsesMissingIOMember() {}

// See [PlainFunc.Method].
/*! [PlainFunc.Method]: "PlainFunc" is not a type */
func UsesFuncReceiver() {}

// See [PlainConst.Method].
/*! [PlainConst.Method]: "PlainConst" is not a type */
func UsesConstReceiver() {}

// See [PlainVar.Field].
/*! [PlainVar.Field]: "PlainVar" is not a type */
func UsesVarReceiver() {}

// See [fmt.Println.Format].
/*! [fmt.Println.Format]: "Println" is not a type */
func UsesFmtFuncReceiver() {}

// See [notimported.Foo].
/*! [notimported.Foo]: package "notimported" is not imported */
func UsesUnimportedPkg() {}

// See [notimported].
/*! [notimported]: package "notimported" is not imported */
func UsesUnimportedPkgOnly() {}

// See [encoding/json.Marshal].
/*! [encoding/json.Marshal]: package "encoding/json" is not imported */
func UsesUnimportedPath() {}

// See [*MissingStar.Method].
/*! [*MissingStar.Method]: type "MissingStar" not found in current package */
func UsesStarReceiver() {}

// See [*Documented.NoSuch].
/*! [*Documented.NoSuch]: type "Documented" has no method or field "NoSuch" */
func UsesStarMember() {}

// See [io/fs.MissingFS].
/*! [io/fs.MissingFS]: "MissingFS" not found in package "fs" */
func UsesFSPath() {}

// See [io/fs.FS.Nope].
/*! [io/fs.FS.Nope]: type "FS" has no method or field "Nope" */
func UsesFSMember() {}

// See [GenericBox.Nope].
/*! [GenericBox.Nope]: type "GenericBox" has no method or field "Nope" */
func UsesGeneric() {}

// See [Alias.Nope].
/*! [Alias.Nope]: type "Alias" has no method or field "Nope" */
type aliasUser struct{}

// See [MissingA] and [MissingB].
/*! [MissingA]: unknown symbol "MissingA" in current package */
/*! [MissingB]: unknown symbol "MissingB" in current package */
func UsesTwoMissing() {}

const (
	// See [MissingConstSym].
	/*! [MissingConstSym]: unknown symbol "MissingConstSym" in current package */
	CommentedConst = 1
)

// BufWrap embeds a buffer. See [BufWrap.NoSuch].
/*! [BufWrap.NoSuch]: type "BufWrap" has no method or field "NoSuch" */
type BufWrap struct {
	// See [bytes.Buffer.NoSuch].
	/*! [bytes.Buffer.NoSuch]: type "Buffer" has no method or field "NoSuch" */
	bytes.Buffer
}

// See [fmt] package docs and also [MissingOnVar].
/*! [MissingOnVar]: unknown symbol "MissingOnVar" in current package */
var groupedVar = 0

/*
See [MissingInBlock].
*/
/*! [MissingInBlock]: unknown symbol "MissingInBlock" in current package */
func UsesBlockComment() {}

// Items:
//   - [MissingInList]
/*! [MissingInList]: unknown symbol "MissingInList" in current package */
func UsesList() {}

// HeadingBad mentions a missing symbol.
//
// # See [MissingHeading]
/*! [MissingHeading]: unknown symbol "MissingHeading" in current package */
func UsesHeading() {}
