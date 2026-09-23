package checker_test

import (
	"fmt"
	"io"
	. "math"
	str "strings"
)

var (
	_ = fmt.Sprint
	_ io.Reader
	_ = Sqrt
	_ = str.ToUpper
)

type DocType struct {
	Field int
}

func (DocType) Method() {}

func DocFunc() {}

// UnknownLocal refers to [MissingFunc].
/*! [MissingFunc]: unknown symbol "MissingFunc" in current package */
func UnknownLocal() {}

// UnknownLocalPtr refers to [*MissingType].
/*! [*MissingType]: unknown symbol "MissingType" in current package */
func UnknownLocalPtr() {}

// UnknownLocalType refers to [MissingType.Method].
/*! [MissingType.Method]: type "MissingType" not found in current package */
func UnknownLocalType() {}

// UnknownLocalMember refers to [DocType.Missing].
/*! [DocType.Missing]: type "DocType" has no method or field "Missing" */
func UnknownLocalMember() {}

// NonTypeLocalRecv refers to [DocFunc.Method].
/*! [DocFunc.Method]: "DocFunc" is not a type */
func NonTypeLocalRecv() {}

// UnknownQualified refers to [fmt.Printline].
/*! [fmt.Printline]: "Printline" not found in package "fmt" */
func UnknownQualified() {}

// UnknownQualifiedType refers to [io.Rader.Read].
/*! [io.Rader.Read]: type "Rader" not found in package "io" */
func UnknownQualifiedType() {}

// UnknownQualifiedMember refers to [io.Reader.Write].
/*! [io.Reader.Write]: type "Reader" has no method or field "Write" */
func UnknownQualifiedMember() {}

// NonTypeQualifiedRecv refers to [fmt.Println.Method].
/*! [fmt.Println.Method]: "Println" is not a type */
func NonTypeQualifiedRecv() {}

// NotImported refers to [os.Exit].
/*! [os.Exit]: package "os" is not imported */
func NotImported() {}

// NotImportedPackage refers to [os].
/*! [os]: package "os" is not imported */
func NotImportedPackage() {}

// NotImportedPath refers to [net/http.Client].
/*! [net/http.Client]: package "net/http" is not imported */
func NotImportedPath() {}

// RenamedImport refers to [str.Missing].
/*! [str.Missing]: "Missing" not found in package "str" */
func RenamedImport() {}

// RenamedImportType refers to [str.Builder.Missing].
/*! [str.Builder.Missing]: type "Builder" has no method or field "Missing" */
func RenamedImportType() {}

// RenamedImportOriginalName refers to [strings.Builder].
/*! [strings.Builder]: package "strings" is not imported */
func RenamedImportOriginalName() {}

// DotImport refers to [Sqrtt].
/*! [Sqrtt]: unknown symbol "Sqrtt" in current package */
func DotImport() {}

// DotImportNonType refers to [Sqrt.Method].
/*! [Sqrt.Method]: "Sqrt" is not a type */
func DotImportNonType() {}

// BuiltinMember refers to [error.Missing].
/*! [error.Missing]: type "error" has no method or field "Missing" */
func BuiltinMember() {}

// Several refers to [Missing1], [DocType] and [Missing2].
// Mentioning [Missing1] again is reported once.
/*! [Missing1]: unknown symbol "Missing1" in current package */
/*! [Missing2]: unknown symbol "Missing2" in current package */
func Several() {}

// InList has links in a list:
//   - [ListMissing]
//   - [DocType.Method]
/*! [ListMissing]: unknown symbol "ListMissing" in current package */
func InList() {}

/* BlockComment refers to [BlockMissing]. */
/*! [BlockMissing]: unknown symbol "BlockMissing" in current package */
func BlockComment() {}

// Doc refers to [DocType.Other].
/*! [DocType.Other]: type "DocType" has no method or field "Other" */
func (DocType) Doc() {}

// TypeDoc refers to [MissingType].
/*! [MissingType]: unknown symbol "MissingType" in current package */
type TypeDoc struct {
	// Field refers to [MissingFieldRef].
	/*! [MissingFieldRef]: unknown symbol "MissingFieldRef" in current package */
	Field int
}

// IfaceDoc is an interface.
type IfaceDoc interface {
	// Method refers to [IfaceDoc.Missing].
	/*! [IfaceDoc.Missing]: type "IfaceDoc" has no method or field "Missing" */
	Method()
}

// Group is a grouped declaration, see [GroupMissing].
/*! [GroupMissing]: unknown symbol "GroupMissing" in current package */
const (
	// GroupA refers to [GroupAMissing].
	/*! [GroupAMissing]: unknown symbol "GroupAMissing" in current package */
	GroupA = 1

	// GroupB refers to [fmt.GroupBMissing].
	/*! [fmt.GroupBMissing]: "GroupBMissing" not found in package "fmt" */
	GroupB = 2
)

// VarDoc refers to [VarMissing].
/*! [VarMissing]: unknown symbol "VarMissing" in current package */
var VarDoc int

var (
	// VarSpecDoc refers to [VarSpecMissing].
	/*! [VarSpecMissing]: unknown symbol "VarSpecMissing" in current package */
	VarSpecDoc int
)

type (
	// TypeSpecDoc refers to [TypeSpecMissing].
	/*! [TypeSpecMissing]: unknown symbol "TypeSpecMissing" in current package */
	TypeSpecDoc int
)
