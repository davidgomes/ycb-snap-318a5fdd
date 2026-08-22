package checker_test

import (
	"fmt"
	printing "fmt"
	"strings"
)

type ExistingType struct{}

func existingFunc() {}

type EmbedBase struct{}

func (EmbedBase) EmbeddedMethod() {}

type WithEmbed struct {
	EmbedBase
}

type WithField struct {
	ExportedField int
}

var ExistingVar = 1

// Uses [MissingType].
/*! [MissingType]: unknown symbol "MissingType" in current package */
type brokenLocalType int

// Calls [MissingFunc].
/*! [MissingFunc]: unknown symbol "MissingFunc" in current package */
func brokenLocalFuncRef() {}

// See [ExistingType.MissingMethod].
/*! [ExistingType.MissingMethod]: type "ExistingType" has no method or field "MissingMethod" */
type brokenMethodRef int

// See [WithField.MissingField].
/*! [WithField.MissingField]: type "WithField" has no method or field "MissingField" */
type brokenFieldRef int

// Uses [missingAlias.MissingSym].
/*! [missingAlias.MissingSym]: package "missingAlias" is not imported */
type brokenPkgRef int

// Package [notpkg] docs.
/*! [notpkg]: package "notpkg" is not imported */
type brokenPkgOnlyRef int

// See [fmt.MissingName].
/*! [fmt.MissingName]: "MissingName" not found in package "fmt" */
func brokenImportedSymbolRef() {}

// Method on [ExistingVar.Method].
/*! [ExistingVar.Method]: "ExistingVar" is not a type */
func brokenNonTypeReceiverRef() {}

// See [MissingType.Method].
/*! [MissingType.Method]: type "MissingType" not found in current package */
type brokenMissingReceiver int

// See [strings.MissingType.Method].
/*! [strings.MissingType.Method]: type "MissingType" not found in package "strings" */
func brokenQualifiedMethodRef() {}

// See [printing.MissingSym].
/*! [printing.MissingSym]: "MissingSym" not found in package "printing" */
func brokenRenamedImportRef() {}

func init() {
	_ = fmt.Sprintf
	_ = strings.Contains
	_, _ = printing.Println, printing.Sprintf
}
