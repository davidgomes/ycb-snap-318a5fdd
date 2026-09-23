package checker_test

import (
	"bufio"
	str "strings"
)

var _ = bufio.NewReader
var _ = str.NewReader

type Base struct {
	Embedded int
}

func (Base) BaseMethod() {}

type Derived struct {
	Base
	Own int
}

func (*Derived) Method() {}

func PlainFunc() {}

// UnknownLocal refers to [NoSuchThing].
/*! [NoSuchThing]: unknown symbol "NoSuchThing" in current package */
func UnknownLocal() {}

// UnknownLower refers to [nosuchthing].
/*! [nosuchthing]: unknown symbol "nosuchthing" in current package */
func UnknownLower() {}

// UnknownQualified refers to [bufio.NoSuchThing].
/*! [bufio.NoSuchThing]: "NoSuchThing" not found in package "bufio" */
func UnknownQualified() {}

// UnknownRenamed refers to [str.NoSuchThing].
/*! [str.NoSuchThing]: "NoSuchThing" not found in package "str" */
func UnknownRenamed() {}

// UnknownSelfQualified refers to [checker_test.NoSuchThing].
/*! [checker_test.NoSuchThing]: "NoSuchThing" not found in package "checker_test" */
func UnknownSelfQualified() {}

// UnknownLocalType refers to [NoType.Method].
/*! [NoType.Method]: type "NoType" not found in current package */
func UnknownLocalType() {}

// UnknownQualifiedType refers to [bufio.NoType.Method].
/*! [bufio.NoType.Method]: type "NoType" not found in package "bufio" */
func UnknownQualifiedType() {}

// UnknownMember refers to [Derived.NoMember] and [*Derived.Missing].
/*! [Derived.NoMember]: type "Derived" has no method or field "NoMember" */
/*! [*Derived.Missing]: type "Derived" has no method or field "Missing" */
func UnknownMember() {}

// UnknownQualifiedMember refers to [bufio.Reader.NoMember].
/*! [bufio.Reader.NoMember]: type "Reader" has no method or field "NoMember" */
func UnknownQualifiedMember() {}

// UnknownRenamedMember refers to [str.Builder.NoMember].
/*! [str.Builder.NoMember]: type "Builder" has no method or field "NoMember" */
func UnknownRenamedMember() {}

// NotAType refers to [PlainFunc.Method].
/*! [PlainFunc.Method]: "PlainFunc" is not a type */
func NotAType() {}

// NotATypeQualified refers to [bufio.NewReader.Method].
/*! [bufio.NewReader.Method]: "NewReader" is not a type */
func NotATypeQualified() {}

// NotImported refers to [bytes.Buffer].
/*! [bytes.Buffer]: package "bytes" is not imported */
func NotImported() {}

// NotImportedByOriginalName refers to [strings.Builder].
/*! [strings.Builder]: package "strings" is not imported */
func NotImportedByOriginalName() {}

// NotImportedPath refers to [encoding/json.Marshal].
/*! [encoding/json.Marshal]: package "encoding/json" is not imported */
func NotImportedPath() {}

// TypeDoc refers to [NoSuchThing] twice: [NoSuchThing].
/*! [NoSuchThing]: unknown symbol "NoSuchThing" in current package */
type TypeDoc int

type (
	// GroupedType refers to [Missing1].
	/*! [Missing1]: unknown symbol "Missing1" in current package */
	GroupedType int
)

const (
	// GroupedConst refers to [Missing2].
	/*! [Missing2]: unknown symbol "Missing2" in current package */
	GroupedConst = 1
)

type WithFields struct {
	// Field refers to [Missing3].
	/*! [Missing3]: unknown symbol "Missing3" in current package */
	Field int
}

// ListDoc has a list:
//   - item with [Missing4]
/*! [Missing4]: unknown symbol "Missing4" in current package */
func ListDoc() {}
