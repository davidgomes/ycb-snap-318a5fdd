package checker_test

import (
	jsonalias "encoding/json"
	fmtalias "fmt"
)

var (
	_ = fmtalias.Sprintf
	_ = jsonalias.Marshal
)

// See [fmtalias.Println] and [jsonalias.Marshal].
func RenamedOK() {}

// See [fmtalias.NotAFmtSymbol].
/*! [fmtalias.NotAFmtSymbol]: "NotAFmtSymbol" not found in package "fmtalias" */
func RenamedMissingSymbol() {}

// See [jsonalias.NoSuchType.Marshal].
/*! [jsonalias.NoSuchType.Marshal]: type "NoSuchType" not found in package "jsonalias" */
func RenamedMissingType() {}

// See [fmt.Println].
/*! [fmt.Println]: package "fmt" is not imported */
func RenamedOriginalName() {}

// See [encoding/json.Nope].
/*! [encoding/json.Nope]: "Nope" not found in package "jsonalias" */
func RenamedFullPath() {}

// See [encoding/json.Marshal].
func RenamedFullPathOK() {}

// See [fmtalias.Println.Extra].
/*! [fmtalias.Println.Extra]: "Println" is not a type */
func RenamedNotAType() {}
