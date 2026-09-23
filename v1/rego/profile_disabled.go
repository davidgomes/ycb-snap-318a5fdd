// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

//go:build !profile

package rego

import "github.com/open-policy-agent/opa/v1/topdown"

// EvalProfile is only populated when OPA is built with the "profile" build tag.
type EvalProfile struct{}

type ruleProfileOpts struct{}

func (ruleProfileOpts) attach(q *topdown.Query) (*topdown.Query, func(ResultSet)) {
	return q, func(ResultSet) {}
}
