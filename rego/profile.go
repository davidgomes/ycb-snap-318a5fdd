package rego

import (
	v1 "github.com/open-policy-agent/opa/v1/rego"
)

// EvalProfile maps fully qualified rule paths to their evaluation counters.
type EvalProfile = v1.EvalProfile

// RuleStat holds evaluation counters for a single rule.
type RuleStat = v1.RuleStat

// ProfileDiff describes the differences between two profiles.
type ProfileDiff = v1.ProfileDiff

// RuleStatDelta holds the difference in counters between two profiles.
type RuleStatDelta = v1.RuleStatDelta
