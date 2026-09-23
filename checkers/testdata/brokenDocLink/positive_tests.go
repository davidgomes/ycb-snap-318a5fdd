package checker_test

import (
	"fmt"
	"io"

	. "errors"
	fmt2 "fmt"
	. "io"
)

var (
	_ = fmt.Println
	_ = io.EOF
	_ = fmt2.Sprintf
	_ = New
	_ Reader
)

// See [BrokenMissing].
/*! [BrokenMissing]: unknown symbol "BrokenMissing" in current package */
func unknownSymbol() {}

// See [BrokenMissingType.Method].
/*! [BrokenMissingType.Method]: type "BrokenMissingType" not found in current package */
func unknownType() {}

// See [BrokenWidget.Missing].
/*! [BrokenWidget.Missing]: type "BrokenWidget" has no method or field "Missing" */
func missingMember() {}

// See [BrokenHelper.Method].
/*! [BrokenHelper.Method]: "BrokenHelper" is not a type */
func funcAsReceiver() {}

// See [BrokenPi.Abs].
/*! [BrokenPi.Abs]: "BrokenPi" is not a type */
func constAsReceiver() {}

// See [fmt.NotAFunc].
/*! [fmt.NotAFunc]: "NotAFunc" not found in package "fmt" */
func missingInPackage() {}

// See [io.MissingType.Read].
/*! [io.MissingType.Read]: type "MissingType" not found in package "io" */
func missingTypeInPackage() {}

// See [io.Reader.Missing].
/*! [io.Reader.Missing]: type "Reader" has no method or field "Missing" */
func missingMethodInPackage() {}

// See [fmt.Println.Nope].
/*! [fmt.Println.Nope]: "Println" is not a type */
func nonTypeInPackage() {}

// See [notpkg.Foo].
/*! [notpkg.Foo]: package "notpkg" is not imported */
func packageNotImported() {}

// See [fmt2.NotAFunc].
/*! [fmt2.NotAFunc]: "NotAFunc" not found in package "fmt2" */
func renamedImport() {}

// See [math.Sin].
/*! [math.Sin]: package "math" is not imported */
func stdlibNotImported() {}

// See [*BrokenMissingPtr.Listen].
/*! [*BrokenMissingPtr.Listen]: type "BrokenMissingPtr" not found in current package */
func starReceiver() {}

// Two problems on one declaration: [BrokenMissingA] and [fmt.MissingB].
/*! [BrokenMissingA]: unknown symbol "BrokenMissingA" in current package */
/*! [fmt.MissingB]: "MissingB" not found in package "fmt" */
func twoProblems() {}

/*
See [BrokenNoSuch].
*/
/*! [BrokenNoSuch]: unknown symbol "BrokenNoSuch" in current package */
func blockUnknown() {}

type brokenFieldStruct struct {
	// BrokenField documents [BrokenMissingField].
	/*! [BrokenMissingField]: unknown symbol "BrokenMissingField" in current package */
	BrokenField int
}

// See [len.Nope].
/*! [len.Nope]: "len" is not a type */
func builtinNonType() {}

// See [error.Missing].
/*! [error.Missing]: type "error" has no method or field "Missing" */
func builtinMissingMethod() {}

// Dot-imported names are local: [DotMissing].
/*! [DotMissing]: unknown symbol "DotMissing" in current package */
func dotImportMissing() {}

// See [Reader.Read] via the dot import of io.
func validDotType() {}

// See [New.Nope].
/*! [New.Nope]: "New" is not a type */
func dotImportNonType() {}

// See [checker_test.BrokenMissingInPkg].
/*! [checker_test.BrokenMissingInPkg]: "BrokenMissingInPkg" not found in package "checker_test" */
func missingInCurrentPackage() {}

const (
	// See [BrokenMissingConst].
	/*! [BrokenMissingConst]: unknown symbol "BrokenMissingConst" in current package */
	BrokenConst = 1
)
