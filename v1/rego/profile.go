// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

//go:build profile

package rego

import (
	"fmt"
	"sort"
	"strings"

	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/topdown"
)

// RuleStat holds evaluation counters for a single rule path.
type RuleStat struct {
	Evals     int `json:"evals"`
	Successes int `json:"successes"`
}

// SuccessRate returns Successes/Evals, or 0 if the rule was never evaluated.
func (s *RuleStat) SuccessRate() float64 {
	if s == nil || s.Evals == 0 {
		return 0
	}
	return float64(s.Successes) / float64(s.Evals)
}

func (s *RuleStat) String() string {
	if s == nil {
		return "<nil>"
	}
	return fmt.Sprintf("evals=%d successes=%d", s.Evals, s.Successes)
}

func (s *RuleStat) clone() *RuleStat {
	c := *s
	return &c
}

// EvalProfile maps fully qualified rule paths to their evaluation counters.
type EvalProfile struct {
	Rules map[string]*RuleStat `json:"rules"`
}

func newEvalProfile() *EvalProfile {
	return &EvalProfile{Rules: map[string]*RuleStat{}}
}

// Stat returns the counters for rule, or nil if the rule is not tracked.
func (p *EvalProfile) Stat(rule string) *RuleStat {
	if p == nil {
		return nil
	}
	return p.Rules[rule]
}

// ContainsRule reports whether path is tracked by the profile.
func (p *EvalProfile) ContainsRule(path string) bool {
	if p == nil {
		return false
	}
	_, ok := p.Rules[path]
	return ok
}

// RulePaths returns the sorted list of tracked rule paths.
func (p *EvalProfile) RulePaths() []string {
	if p == nil {
		return nil
	}
	return p.sortedPaths(func(*RuleStat) bool { return true })
}

func (p *EvalProfile) sortedPaths(keep func(*RuleStat) bool) []string {
	var paths []string
	for path, s := range p.Rules {
		if keep(s) {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths
}

// SuccessRate returns Successes/Evals for rule, or 0 if untracked or never evaluated.
func (p *EvalProfile) SuccessRate(rule string) float64 {
	return p.Stat(rule).SuccessRate()
}

// OverallSuccessRate returns the aggregate Successes/Evals across all rules.
func (p *EvalProfile) OverallSuccessRate() float64 {
	if p == nil {
		return 0
	}
	return p.total().SuccessRate()
}

func (p *EvalProfile) total() *RuleStat {
	t := &RuleStat{}
	for _, s := range p.Rules {
		t.Evals += s.Evals
		t.Successes += s.Successes
	}
	return t
}

// HotRules returns the sorted rule paths evaluated at least minEvals times.
func (p *EvalProfile) HotRules(minEvals int) []string {
	if p == nil {
		return nil
	}
	return p.sortedPaths(func(s *RuleStat) bool { return s.Evals >= minEvals })
}

// FailedRules returns the sorted rule paths that were evaluated but never succeeded.
func (p *EvalProfile) FailedRules() []string {
	if p == nil {
		return nil
	}
	return p.sortedPaths(func(s *RuleStat) bool { return s.Evals > 0 && s.Successes == 0 })
}

// SucceededRules returns the sorted rule paths that succeeded at least once.
func (p *EvalProfile) SucceededRules() []string {
	if p == nil {
		return nil
	}
	return p.sortedPaths(func(s *RuleStat) bool { return s.Successes > 0 })
}

func packageOf(path string) string {
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		return path[:i]
	}
	return path
}

// Packages returns the sorted unique package names of the tracked rules.
func (p *EvalProfile) Packages() []string {
	if p == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var pkgs []string
	for path := range p.Rules {
		pkg := packageOf(path)
		if _, ok := seen[pkg]; !ok {
			seen[pkg] = struct{}{}
			pkgs = append(pkgs, pkg)
		}
	}
	sort.Strings(pkgs)
	return pkgs
}

// FilterByPackage returns a new profile containing copies of the stats for
// rules in package pkg.
func (p *EvalProfile) FilterByPackage(pkg string) *EvalProfile {
	if p == nil {
		return nil
	}
	out := newEvalProfile()
	for path, s := range p.Rules {
		if packageOf(path) == pkg {
			out.Rules[path] = s.clone()
		}
	}
	return out
}

// PackageStats returns the aggregated stats for each package.
func (p *EvalProfile) PackageStats() map[string]*RuleStat {
	if p == nil {
		return nil
	}
	out := map[string]*RuleStat{}
	for path, s := range p.Rules {
		pkg := packageOf(path)
		agg, ok := out[pkg]
		if !ok {
			agg = &RuleStat{}
			out[pkg] = agg
		}
		agg.Evals += s.Evals
		agg.Successes += s.Successes
	}
	return out
}

// Merge returns a new profile combining p and other by summing their counts.
// If only one side is non-nil it is returned as-is.
func (p *EvalProfile) Merge(other *EvalProfile) *EvalProfile {
	switch {
	case p == nil:
		return other
	case other == nil:
		return p
	}
	out := newEvalProfile()
	for _, src := range []*EvalProfile{p, other} {
		for path, s := range src.Rules {
			if agg, ok := out.Rules[path]; ok {
				agg.Evals += s.Evals
				agg.Successes += s.Successes
			} else {
				out.Rules[path] = s.clone()
			}
		}
	}
	return out
}

// Summary returns a one-line description of the profile.
func (p *EvalProfile) Summary() string {
	if p == nil {
		return "profile: disabled"
	}
	t := p.total()
	return fmt.Sprintf("profile: %d rules, %d evals, %d successes", len(p.Rules), t.Evals, t.Successes)
}

// Equal reports whether p and other track the same rules with the same counts.
func (p *EvalProfile) Equal(other *EvalProfile) bool {
	if p == nil || other == nil {
		return p == nil && other == nil
	}
	if len(p.Rules) != len(other.Rules) {
		return false
	}
	for path, s := range p.Rules {
		o, ok := other.Rules[path]
		if !ok || *o != *s {
			return false
		}
	}
	return true
}

func (p *EvalProfile) String() string {
	if p == nil {
		return "<nil>"
	}
	var sb strings.Builder
	sb.WriteString("Profile:\n")
	for _, path := range p.RulePaths() {
		s := p.Rules[path]
		fmt.Fprintf(&sb, "  %s: evals=%d successes=%d\n", path, s.Evals, s.Successes)
	}
	return sb.String()
}

// RuleStatDelta is the difference between two RuleStats (other minus receiver).
type RuleStatDelta struct {
	EvalsDelta     int `json:"evals_delta"`
	SuccessesDelta int `json:"successes_delta"`
}

// ProfileDiff describes the differences between two profiles. Fields are nil
// when there are no entries.
type ProfileDiff struct {
	Added   map[string]*RuleStat      `json:"added,omitempty"`
	Removed map[string]*RuleStat      `json:"removed,omitempty"`
	Changed map[string]*RuleStatDelta `json:"changed,omitempty"`
}

// HasChanges reports whether the diff contains any entries.
func (d *ProfileDiff) HasChanges() bool {
	return d != nil && (d.Added != nil || d.Removed != nil || d.Changed != nil)
}

// Diff compares p against other. Added holds rules only in other, Removed
// holds rules only in p, and Changed holds shared rules whose counts differ.
func (p *EvalProfile) Diff(other *EvalProfile) *ProfileDiff {
	if p == nil {
		return nil
	}
	d := &ProfileDiff{}
	var otherRules map[string]*RuleStat
	if other != nil {
		otherRules = other.Rules
	}
	for path, s := range p.Rules {
		o, ok := otherRules[path]
		if !ok {
			if d.Removed == nil {
				d.Removed = map[string]*RuleStat{}
			}
			d.Removed[path] = s.clone()
			continue
		}
		if *o != *s {
			if d.Changed == nil {
				d.Changed = map[string]*RuleStatDelta{}
			}
			d.Changed[path] = &RuleStatDelta{
				EvalsDelta:     o.Evals - s.Evals,
				SuccessesDelta: o.Successes - s.Successes,
			}
		}
	}
	for path, o := range otherRules {
		if _, ok := p.Rules[path]; !ok {
			if d.Added == nil {
				d.Added = map[string]*RuleStat{}
			}
			d.Added[path] = o.clone()
		}
	}
	return d
}

// EnableRuleProfile returns an argument that enables rule evaluation
// profiling. The collected profile is attached to each Result.
func EnableRuleProfile(yes bool) func(r *Rego) {
	return func(r *Rego) {
		r.ruleProfile.enabled = yes
	}
}

// EvalRuleProfile enables or disables rule evaluation profiling for a
// prepared query's evaluation, overriding EnableRuleProfile.
func EvalRuleProfile(yes bool) EvalOption {
	return func(e *EvalContext) {
		e.ruleProfile.enabled = yes
	}
}

type ruleProfileOpts struct {
	enabled bool
}

func (o ruleProfileOpts) attach(q *topdown.Query) (*topdown.Query, func(ResultSet)) {
	if !o.enabled {
		return q, func(ResultSet) {}
	}
	t := &ruleProfileTracer{
		profile: newEvalProfile(),
		pending: map[uint64]*RuleStat{},
	}
	return q.WithQueryTracer(t), func(rs ResultSet) {
		for i := range rs {
			rs[i].Profile = t.profile
		}
	}
}

// ruleProfileTracer counts one eval per rule EnterOp and at most one success
// per entered rule query (the first ExitOp, subsequent ones are redos).
type ruleProfileTracer struct {
	profile *EvalProfile
	pending map[uint64]*RuleStat
}

func (*ruleProfileTracer) Enabled() bool { return true }

func (*ruleProfileTracer) Config() topdown.TraceConfig { return topdown.TraceConfig{} }

func (t *ruleProfileTracer) TraceEvent(evt topdown.Event) {
	rule, ok := evt.Node.(*ast.Rule)
	if !ok {
		return
	}
	switch evt.Op {
	case topdown.EnterOp:
		if rule.Module == nil {
			return
		}
		path := rule.Ref().GroundPrefix().String()
		s, ok := t.profile.Rules[path]
		if !ok {
			s = &RuleStat{}
			t.profile.Rules[path] = s
		}
		s.Evals++
		t.pending[evt.QueryID] = s
	case topdown.ExitOp:
		if s, ok := t.pending[evt.QueryID]; ok {
			s.Successes++
			delete(t.pending, evt.QueryID)
		}
	case topdown.FailOp:
		delete(t.pending, evt.QueryID)
	}
}
