package checker_test

import . "strings"

// Dot-imported symbols count as local. See [ToUpper], [Builder] and [Builder.WriteString].
func DotOK() { _ = ToUpper("a") }

// See [NotAStringFunc].
/*! [NotAStringFunc]: unknown symbol "NotAStringFunc" in current package */
func DotMissing() {}

// See [Builder.Nope].
/*! [Builder.Nope]: type "Builder" has no method or field "Nope" */
func DotMissingMember() {}

// See [strings.Builder].
/*! [strings.Builder]: package "strings" is not imported */
func DotQualifierUnused() {}

// See [ToUpper.Nope].
/*! [ToUpper.Nope]: "ToUpper" is not a type */
func DotFuncReceiver() {}
