package checker_test

import (
	// See [NoSuchImport].
	/*! [NoSuchImport]: unknown symbol "NoSuchImport" in current package */
	"context"
	j "encoding/json"
	"fmt"
	fmt2 "fmt"

	. "io"
)

var (
	_ = context.Background
	_ = fmt.Println
	_ = fmt2.Sprint
	_ = j.Marshal
	_ = EOF
)

// See [NoSuch].
/*! [NoSuch]: unknown symbol "NoSuch" in current package */
func UnknownLocal() {}

// See [fmt.NoSuch].
/*! [fmt.NoSuch]: "NoSuch" not found in package "fmt" */
func UnknownQualified() {}

// See [fmt2.NoSuch].
/*! [fmt2.NoSuch]: "NoSuch" not found in package "fmt2" */
func UnknownRenamed() {}

// See [NoSuchType.Method].
/*! [NoSuchType.Method]: type "NoSuchType" not found in current package */
func MissingLocalType() {}

// See [fmt.NoSuchType.Method].
/*! [fmt.NoSuchType.Method]: type "NoSuchType" not found in package "fmt" */
func MissingQualifiedType() {}

// See [Widget.Missing].
/*! [Widget.Missing]: type "Widget" has no method or field "Missing" */
func MissingMember() {}

// See [Helper.Nope].
/*! [Helper.Nope]: "Helper" is not a type */
func FuncReceiver() {}

// See [Answer.Nope].
/*! [Answer.Nope]: "Answer" is not a type */
func ConstReceiver() {}

// See [Global.Nope].
/*! [Global.Nope]: "Global" is not a type */
func VarReceiver() {}

// See [fmt.Println.Nope].
/*! [fmt.Println.Nope]: "Println" is not a type */
func QualifiedNonType() {}

// See [os.Exit].
/*! [os.Exit]: package "os" is not imported */
func NotImported() {}

// See [os].
/*! [os]: package "os" is not imported */
func NotImportedBare() {}

// See [math/rand.Int].
/*! [math/rand.Int]: package "math/rand" is not imported */
func NotImportedPath() {}

// See [encoding/json.NoSuch].
/*! [encoding/json.NoSuch]: "NoSuch" not found in package "j" */
func FullPathAlias() {}

// See [j.NoSuch].
/*! [j.NoSuch]: "NoSuch" not found in package "j" */
func AliasQualifier() {}

// See [EOF.Nope].
/*! [EOF.Nope]: "EOF" is not a type */
func DotNonType() {}

// See [Reader.Missing].
/*! [Reader.Missing]: type "Reader" has no method or field "Missing" */
func DotMissingMember() {}

// See [NotFromIO].
/*! [NotFromIO]: unknown symbol "NotFromIO" in current package */
func DotUnknown() {}

// See [*NoSuch].
/*! [*NoSuch]: unknown symbol "NoSuch" in current package */
func StarUnknown() {}

// See [*Widget.Missing].
/*! [*Widget.Missing]: type "Widget" has no method or field "Missing" */
func StarMissing() {}

// See [checker_test.NoSuch].
/*! [checker_test.NoSuch]: "NoSuch" not found in package "checker_test" */
func CurrentPkgQualifier() {}

// See [fmt.Stringer.Missing].
/*! [fmt.Stringer.Missing]: type "Stringer" has no method or field "Missing" */
func QualifiedMissingMember() {}

// See [Box.Missing].
/*! [Box.Missing]: type "Box" has no method or field "Missing" */
func GenericMissing() {}

// See [Alias.Missing].
/*! [Alias.Missing]: type "Alias" has no method or field "Missing" */
func AliasMissing() {}

// See [L3.Missing].
/*! [L3.Missing]: type "L3" has no method or field "Missing" */
func DeepMissing() {}

// [Widget] resolves, [NoSuchA] and [fmt.NoSuchB] do not.
/*! [NoSuchA]: unknown symbol "NoSuchA" in current package */
/*! [fmt.NoSuchB]: "NoSuchB" not found in package "fmt" */
func TwoProblems() {}

/* See [BlockMissing]. */
/*! [BlockMissing]: unknown symbol "BlockMissing" in current package */
func BlockComment() {}

// Items:
//
//   - [ListMissing]
/*! [ListMissing]: unknown symbol "ListMissing" in current package */
func ListComment() {}

// See [ExtMissing].
/*! [ExtMissing]: unknown symbol "ExtMissing" in current package */
func ExternalMissing() {}

// See [NoSuchMethod].
/*! [NoSuchMethod]: unknown symbol "NoSuchMethod" in current package */
func (Widget) PositiveMethod() {}

type PositiveStruct struct {
	// See [NoSuchField].
	/*! [NoSuchField]: unknown symbol "NoSuchField" in current package */
	Field int
}

const (
	// See [NoSuchConst].
	/*! [NoSuchConst]: unknown symbol "NoSuchConst" in current package */
	PositiveConst = 1
)

var (
	// See [NoSuchVar].
	/*! [NoSuchVar]: unknown symbol "NoSuchVar" in current package */
	PositiveVar int
)

// See [L4.Missing] and [StringerWrap.Missing] and [ReaderIface.Missing].
/*! [L4.Missing]: type "L4" has no method or field "Missing" */
/*! [StringerWrap.Missing]: type "StringerWrap" has no method or field "Missing" */
/*! [ReaderIface.Missing]: type "ReaderIface" has no method or field "Missing" */
func MoreMembers() {}
