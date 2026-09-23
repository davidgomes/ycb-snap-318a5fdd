package checker_test

import (
	fmt2 "fmt"
)

const ConstVal = 1

func Fn() {}

// Unknown local symbol.
/*! [Missing]: unknown symbol "Missing" in current package */
func UnknownLocal() {}

// Unknown member on a known type.
/*! [Named.Missing]: type "Named" has no method or field "Missing" */
func UnknownMember() {}

// Receiver is not a type.
/*! [Fn.Method]: "Fn" is not a type */
func FuncAsRecv() {}

// Const used as a receiver.
/*! [ConstVal.Method]: "ConstVal" is not a type */
func ConstAsRecv() {}

// Unknown type used as a receiver.
/*! [NoType.Method]: type "NoType" not found in current package */
func UnknownRecv() {}

// Package name is not imported.
/*! [fmt.Println]: package "fmt" is not imported */
func NotImported() {}

// Symbol missing from an imported package. fmt is imported as fmt2.
/*! [fmt2.Missing]: "Missing" not found in package "fmt2" */
func MissingInPkg() { fmt2.Print() }

// Type missing from an imported package.
/*! [fmt2.NoSuch.Method]: type "NoSuch" not found in package "fmt2" */
func MissingTypeInPkg() {}

// Non-type from another package used as a receiver.
/*! [fmt2.Println.Method]: "Println" is not a type */
func NonTypeInPkg() {}

// Known type, missing member, qualified.
/*! [fmt2.Stringer.Missing]: type "Stringer" has no method or field "Missing" */
func MissingQualifiedMember() {}

// Several broken links on one declaration.
/*! [Missing]: unknown symbol "Missing" in current package */
/*! [Other]: unknown symbol "Other" in current package */
func TwoLinks() {}

// Field doc is reported on the field, not on the comment line above it.
type WithField struct {
	// See the note below.
	/*! [Missing]: unknown symbol "Missing" in current package */
	Field int
}

// Spec doc is reported on the spec line.
type (
	// See the note below.
	/*! [Missing]: unknown symbol "Missing" in current package */
	SpecDoc int
)

// Full import path that is not imported.
/*! [encoding/json.Marshal]: package "encoding/json" is not imported */
func FullPathMissing() {}

// Pointer form keeps the star in the reported reference.
/*! [*NoType.Method]: type "NoType" not found in current package */
func PointerRecv() {}
