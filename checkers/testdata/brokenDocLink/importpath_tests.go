package checker_test

import "encoding/json"

var _ = json.Marshal

// See [encoding/json.Marshal] and [json.Marshal].
func ImportPathOK() {}

// See [encoding/json.Nope].
/*! [encoding/json.Nope]: "Nope" not found in package "json" */
func ImportPathMissing() {}

// See [json.Nope].
/*! [json.Nope]: "Nope" not found in package "json" */
func ImportNameMissing() {}

// See [json.Number.Nope].
/*! [json.Number.Nope]: type "Number" has no method or field "Nope" */
func ImportPathMember() {}
