// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

//go:build !profile

package rego

// ruleProfileSupported is false when this binary is built without the profile tag.
const ruleProfileSupported = false
