// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

//go:build profile

package rego

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/topdown"
)

// EvalRuleProfile enables or disables rule evaluation profiling for a Prepared
// Query's evaluation, overriding EnableRuleProfile. Profiling is only
// supported by the default (topdown) evaluation target.
func EvalRuleProfile(yes bool) EvalOption {
	return func(e *EvalContext) {
		e.ruleProfile = yes
	}
}

// EnableRuleProfile returns an argument that enables rule evaluation profiling
// for evaluations of r, including evaluations of queries prepared from r. Each
// Result of a profiled evaluation carries the EvalProfile of that evaluation.
func EnableRuleProfile(yes bool) func(r *Rego) {
	return func(r *Rego) {
		r.ruleProfile = yes
	}
}

// EvalProfile holds the statistics of every rule entered during an
// evaluation, keyed by the fully qualified path of the rule, e.g.
// "data.authz.allow".
type EvalProfile struct {
	Rules map[string]*RuleStat `json:"rules"`
}

// RuleStat holds the evaluation counts of a rule. Evals is the number of times
// a definition of the rule was entered, and Successes is the number of those
// entries that produced at least one result.
type RuleStat struct {
	Evals     int `json:"evals"`
	Successes int `json:"successes"`
}

// ProfileDiff describes how one EvalProfile differs from another. Each field
// is nil when there is nothing to report.
type ProfileDiff struct {
	Added   map[string]*RuleStat      `json:"added,omitempty"`
	Removed map[string]*RuleStat      `json:"removed,omitempty"`
	Changed map[string]*RuleStatDelta `json:"changed,omitempty"`
}

// RuleStatDelta holds the change in the counts of a rule between two profiles.
type RuleStatDelta struct {
	EvalsDelta     int `json:"evals_delta"`
	SuccessesDelta int `json:"successes_delta"`
}

// SuccessRate returns the fraction of evaluations of the rule that succeeded,
// or 0 if the rule was never evaluated.
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

func (s *RuleStat) add(other *RuleStat) {
	s.Evals += other.Evals
	s.Successes += other.Successes
}

func (s *RuleStat) clone() *RuleStat {
	cpy := *s
	return &cpy
}

// Stat returns the statistics of rule, or nil if rule is not in the profile.
func (p *EvalProfile) Stat(rule string) *RuleStat {
	if p == nil {
		return nil
	}
	return p.Rules[rule]
}

// ContainsRule reports whether the rule at path is in the profile.
func (p *EvalProfile) ContainsRule(path string) bool {
	if p == nil {
		return false
	}
	_, ok := p.Rules[path]
	return ok
}

// RulePaths returns the sorted paths of all rules in the profile, or nil if
// there are none.
func (p *EvalProfile) RulePaths() []string {
	return p.paths(func(*RuleStat) bool { return true })
}

// HotRules returns the sorted paths of the rules evaluated at least minEvals
// times, or nil if there are none.
func (p *EvalProfile) HotRules(minEvals int) []string {
	return p.paths(func(s *RuleStat) bool { return s.Evals >= minEvals })
}

// FailedRules returns the sorted paths of the rules that were evaluated but
// never succeeded, or nil if there are none.
func (p *EvalProfile) FailedRules() []string {
	return p.paths(func(s *RuleStat) bool { return s.Evals > 0 && s.Successes == 0 })
}

// SucceededRules returns the sorted paths of the rules that succeeded at least
// once, or nil if there are none.
func (p *EvalProfile) SucceededRules() []string {
	return p.paths(func(s *RuleStat) bool { return s.Successes > 0 })
}

func (p *EvalProfile) paths(keep func(*RuleStat) bool) []string {
	if p == nil {
		return nil
	}
	var paths []string
	for path, s := range p.Rules {
		if keep(s) {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	return paths
}

// SuccessRate returns the success rate of rule, or 0 if rule is not in the
// profile or was never evaluated.
func (p *EvalProfile) SuccessRate(rule string) float64 {
	return p.Stat(rule).SuccessRate()
}

// OverallSuccessRate returns the success rate across all rules in the profile.
func (p *EvalProfile) OverallSuccessRate() float64 {
	return p.total().SuccessRate()
}

func (p *EvalProfile) total() *RuleStat {
	var total RuleStat
	if p != nil {
		for _, s := range p.Rules {
			total.add(s)
		}
	}
	return &total
}

// Packages returns the sorted names of the packages of the rules in the
// profile, e.g. "data.authz" for "data.authz.allow".
func (p *EvalProfile) Packages() []string {
	return slices.Sorted(maps.Keys(p.PackageStats()))
}

// PackageStats returns the statistics of the rules in the profile summed per
// package.
func (p *EvalProfile) PackageStats() map[string]*RuleStat {
	if p == nil || len(p.Rules) == 0 {
		return nil
	}
	stats := make(map[string]*RuleStat)
	for path, s := range p.Rules {
		pkg := packageOf(path)
		if _, ok := stats[pkg]; !ok {
			stats[pkg] = &RuleStat{}
		}
		stats[pkg].add(s)
	}
	return stats
}

// FilterByPackage returns a new profile containing copies of the statistics
// of the rules in package pkg.
func (p *EvalProfile) FilterByPackage(pkg string) *EvalProfile {
	if p == nil {
		return nil
	}
	filtered := &EvalProfile{Rules: map[string]*RuleStat{}}
	for path, s := range p.Rules {
		if packageOf(path) == pkg {
			filtered.Rules[path] = s.clone()
		}
	}
	return filtered
}

// packageOf returns the package of a rule path by dropping its last element.
func packageOf(path string) string {
	if ref, err := ast.ParseRef(path); err == nil && len(ref) > 1 {
		return ref[:len(ref)-1].String()
	}
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		return path[:i]
	}
	return ""
}

// Merge returns a new profile with the counts of p and other summed. If either
// profile is nil, the other one is returned.
func (p *EvalProfile) Merge(other *EvalProfile) *EvalProfile {
	if p == nil {
		return other
	}
	if other == nil {
		return p
	}
	merged := &EvalProfile{Rules: make(map[string]*RuleStat, len(p.Rules))}
	for _, src := range []*EvalProfile{p, other} {
		for path, s := range src.Rules {
			merged.stat(path).add(s)
		}
	}
	return merged
}

// stat returns the statistics of rule, adding empty ones if missing.
func (p *EvalProfile) stat(rule string) *RuleStat {
	s, ok := p.Rules[rule]
	if !ok {
		s = &RuleStat{}
		p.Rules[rule] = s
	}
	return s
}

// Diff reports the rules only in other as added, the rules only in p as
// removed, and the rules whose counts differ as changed, with deltas computed
// as other's counts minus p's.
func (p *EvalProfile) Diff(other *EvalProfile) *ProfileDiff {
	if p == nil {
		return nil
	}
	diff := &ProfileDiff{}
	for path, s := range p.Rules {
		o := other.Stat(path)
		switch {
		case o == nil:
			diff.Removed = insert(diff.Removed, path, s.clone())
		case *o != *s:
			diff.Changed = insert(diff.Changed, path, &RuleStatDelta{
				EvalsDelta:     o.Evals - s.Evals,
				SuccessesDelta: o.Successes - s.Successes,
			})
		}
	}
	if other != nil {
		for path, o := range other.Rules {
			if !p.ContainsRule(path) {
				diff.Added = insert(diff.Added, path, o.clone())
			}
		}
	}
	return diff
}

func insert[V any](m map[string]V, k string, v V) map[string]V {
	if m == nil {
		m = map[string]V{}
	}
	m[k] = v
	return m
}

// HasChanges reports whether any rule was added, removed or changed.
func (d *ProfileDiff) HasChanges() bool {
	return d != nil && (len(d.Added) > 0 || len(d.Removed) > 0 || len(d.Changed) > 0)
}

// Equal reports whether p and other hold the same rules with the same counts.
func (p *EvalProfile) Equal(other *EvalProfile) bool {
	if p == nil || other == nil {
		return p == other
	}
	return maps.EqualFunc(p.Rules, other.Rules, func(a, b *RuleStat) bool {
		return *a == *b
	})
}

// Summary returns a one-line summary of the profile.
func (p *EvalProfile) Summary() string {
	if p == nil {
		return "profile: disabled"
	}
	total := p.total()
	return fmt.Sprintf("profile: %d rules, %d evals, %d successes", len(p.Rules), total.Evals, total.Successes)
}

func (p *EvalProfile) String() string {
	if p == nil {
		return "<nil>"
	}
	var b strings.Builder
	b.WriteString("Profile:\n")
	for _, path := range p.RulePaths() {
		fmt.Fprintf(&b, "  %s: %s\n", path, p.Rules[path])
	}
	return b.String()
}

// ruleProfiler builds an EvalProfile from the trace events of an evaluation.
type ruleProfiler struct {
	profile *EvalProfile
	stats   map[*ast.Rule]*RuleStat

	// pending holds the statistics of the rule bodies that were entered but
	// have not produced a result yet, keyed by the query ID of the body.
	pending map[uint64]*RuleStat
}

func withRuleProfile(q *topdown.Query, enabled bool) (*topdown.Query, func(ResultSet)) {
	if !enabled {
		return q, func(ResultSet) {}
	}
	p := &ruleProfiler{
		profile: &EvalProfile{Rules: map[string]*RuleStat{}},
		stats:   map[*ast.Rule]*RuleStat{},
		pending: map[uint64]*RuleStat{},
	}
	return q.WithQueryTracer(p), p.attach
}

func (*ruleProfiler) Enabled() bool {
	return true
}

func (*ruleProfiler) Config() topdown.TraceConfig {
	return topdown.TraceConfig{}
}

func (p *ruleProfiler) TraceEvent(evt topdown.Event) {
	rule, ok := evt.Node.(*ast.Rule)
	if !ok {
		return
	}
	switch evt.Op {
	case topdown.EnterOp:
		s, ok := p.stats[rule]
		if !ok {
			s = p.profile.stat(rule.Ref().GroundPrefix().String())
			p.stats[rule] = s
		}
		s.Evals++
		p.pending[evt.QueryID] = s
	case topdown.ExitOp:
		// A rule body exits once per result, but only counts as one success.
		if s, ok := p.pending[evt.QueryID]; ok {
			s.Successes++
			delete(p.pending, evt.QueryID)
		}
	}
}

func (p *ruleProfiler) attach(rs ResultSet) {
	for i := range rs {
		rs[i].Profile = p.profile
	}
}
