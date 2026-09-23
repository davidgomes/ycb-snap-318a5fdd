// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import (
	"slices"
	"strconv"
	"strings"

	"github.com/open-policy-agent/opa/v1/topdown"
)

// EvalProfile maps fully qualified rule paths to evaluation statistics.
// A nil profile means rule profiling is disabled.
//
// Each time the evaluator enters a rule, that rule's Evals count increases by
// one, including rules that fail. A rule with multiple definitions is entered
// once per definition that is tried. Successes counts entries that produced at
// least one result. Repeated solutions from a single entry count once.
type EvalProfile struct {
	rules map[string]*RuleStat
	stack []ruleFrame
}

type ruleFrame struct {
	path      string
	succeeded bool
}

// RuleStat counts how many times a rule was entered and how many of those
// entries produced a result.
type RuleStat struct {
	Evals     int
	Successes int
}

// ProfileDiff is the difference between two evaluation profiles.
// Added contains rules present only in the other profile, Removed contains
// rules present only in the receiver, and Changed contains rules present in
// both whose counts differ. Empty categories are nil rather than empty maps.
type ProfileDiff struct {
	Added   map[string]*RuleStat
	Removed map[string]*RuleStat
	Changed map[string]*RuleStatDelta
}

// RuleStatDelta is the change in counts from the receiver profile to the other
// profile (other minus receiver).
type RuleStatDelta struct {
	EvalsDelta     int
	SuccessesDelta int
}

// EvalRuleProfile enables or disables per-rule evaluation profiling for one
// evaluation. It overrides EnableRuleProfile. The collected counts are
// attached to each Result. Profiling is recorded only when OPA is built with
// the "profile" tag; otherwise Profile stays nil.
func EvalRuleProfile(enabled bool) EvalOption {
	return func(e *EvalContext) {
		e.ruleProfile = enabled
	}
}

// EnableRuleProfile enables per-rule evaluation profiling for evaluations of
// the Rego object. Prepared queries inherit the setting unless EvalRuleProfile
// overrides it. Profiling is recorded only when OPA is built with the
// "profile" tag; otherwise Profile stays nil.
func EnableRuleProfile(enabled bool) func(*Rego) {
	return func(r *Rego) {
		r.ruleProfile = enabled
	}
}

func newEvalProfile() *EvalProfile {
	return &EvalProfile{rules: map[string]*RuleStat{}}
}

func configureRuleProfile(q *topdown.Query, enabled bool) (*topdown.Query, *EvalProfile) {
	if !enabled || !ruleProfileSupported {
		return q, nil
	}
	p := newEvalProfile()
	return q.WithRuleProfiler(p.enter, p.succeed, p.leave), p
}

func stampRuleProfile(rs ResultSet, profile *EvalProfile) {
	if profile == nil {
		return
	}
	for i := range rs {
		rs[i].Profile = profile
	}
}

func (p *EvalProfile) enter(path string) {
	if p == nil || path == "" {
		return
	}
	if p.rules == nil {
		p.rules = map[string]*RuleStat{}
	}
	st := p.rules[path]
	if st == nil {
		st = &RuleStat{}
		p.rules[path] = st
	}
	st.Evals++
	p.stack = append(p.stack, ruleFrame{path: path})
}

func (p *EvalProfile) succeed(path string) {
	if p == nil || path == "" {
		return
	}
	for i := len(p.stack) - 1; i >= 0; i-- {
		if p.stack[i].path != path {
			continue
		}
		if !p.stack[i].succeeded {
			p.stack[i].succeeded = true
			if st := p.rules[path]; st != nil {
				st.Successes++
			}
		}
		return
	}
}

func (p *EvalProfile) leave(path string) {
	if p == nil || path == "" || len(p.stack) == 0 {
		return
	}
	for i := len(p.stack) - 1; i >= 0; i-- {
		if p.stack[i].path == path {
			p.stack = slices.Delete(p.stack, i, i+1)
			return
		}
	}
}

// Stat returns the statistics recorded for rule, or nil when rule was not
// tracked. A nil profile returns nil.
func (p *EvalProfile) Stat(rule string) *RuleStat {
	if p == nil {
		return nil
	}
	return p.rules[rule]
}

// RulePaths returns the tracked rule paths in sorted order.
// It returns nil when the profile is nil or tracks no rules.
func (p *EvalProfile) RulePaths() []string {
	if p == nil || len(p.rules) == 0 {
		return nil
	}
	paths := make([]string, 0, len(p.rules))
	for path := range p.rules {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths
}

// SuccessRate returns Successes/Evals for rule.
// It returns 0 when the profile is nil, rule was not tracked, or Evals is 0.
func (p *EvalProfile) SuccessRate(rule string) float64 {
	if p == nil {
		return 0
	}
	return p.rules[rule].SuccessRate()
}

// OverallSuccessRate returns aggregate Successes/Evals across every tracked rule.
// It returns 0 when the profile is nil or no rules were evaluated.
func (p *EvalProfile) OverallSuccessRate() float64 {
	if p == nil {
		return 0
	}
	evals, successes := p.totals()
	if evals == 0 {
		return 0
	}
	return float64(successes) / float64(evals)
}

// HotRules returns sorted rule paths whose Evals count is at least minEvals.
// It returns nil when the profile is nil or no rule qualifies.
func (p *EvalProfile) HotRules(minEvals int) []string {
	return p.pathsWhere(func(st *RuleStat) bool {
		return st.Evals >= minEvals
	})
}

// FailedRules returns sorted rule paths that were entered and never succeeded.
// It returns nil when the profile is nil or no rule qualifies.
func (p *EvalProfile) FailedRules() []string {
	return p.pathsWhere(func(st *RuleStat) bool {
		return st.Evals > 0 && st.Successes == 0
	})
}

// SucceededRules returns sorted rule paths that succeeded at least once.
// It returns nil when the profile is nil or no rule qualifies.
func (p *EvalProfile) SucceededRules() []string {
	return p.pathsWhere(func(st *RuleStat) bool {
		return st.Successes > 0
	})
}

// Packages returns the sorted unique package names derived from tracked rule
// paths. The package is the path without its final segment, so
// "data.authz.allow" yields "data.authz". A nil profile returns nil.
func (p *EvalProfile) Packages() []string {
	if p == nil || len(p.rules) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(p.rules))
	for path := range p.rules {
		seen[packageName(path)] = struct{}{}
	}
	pkgs := make([]string, 0, len(seen))
	for pkg := range seen {
		pkgs = append(pkgs, pkg)
	}
	slices.Sort(pkgs)
	return pkgs
}

// FilterByPackage returns a new profile containing deep-copied stats for rules
// in pkg. A nil profile returns nil. When no rule matches, the result is a
// non-nil empty profile.
func (p *EvalProfile) FilterByPackage(pkg string) *EvalProfile {
	if p == nil {
		return nil
	}
	out := newEvalProfile()
	for path, st := range p.rules {
		if packageName(path) == pkg {
			out.rules[path] = st.clone()
		}
	}
	return out
}

// Merge returns a profile whose counts are the sum of p and other.
// It returns nil when both profiles are nil, and returns the non-nil profile
// when the other side is nil.
func (p *EvalProfile) Merge(other *EvalProfile) *EvalProfile {
	if p == nil {
		return other
	}
	if other == nil {
		return p
	}
	out := newEvalProfile()
	for path, st := range p.rules {
		out.rules[path] = st.clone()
	}
	for path, st := range other.rules {
		if existing := out.rules[path]; existing != nil {
			existing.Evals += st.Evals
			existing.Successes += st.Successes
			continue
		}
		out.rules[path] = st.clone()
	}
	return out
}

// PackageStats returns aggregated counts for each package derived from tracked
// rule paths. A nil profile returns nil. An empty profile returns nil.
func (p *EvalProfile) PackageStats() map[string]*RuleStat {
	if p == nil || len(p.rules) == 0 {
		return nil
	}
	out := make(map[string]*RuleStat, len(p.rules))
	for path, st := range p.rules {
		if st == nil {
			continue
		}
		pkg := packageName(path)
		agg := out[pkg]
		if agg == nil {
			agg = &RuleStat{}
			out[pkg] = agg
		}
		agg.Evals += st.Evals
		agg.Successes += st.Successes
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ContainsRule reports whether path is tracked. A nil profile returns false.
func (p *EvalProfile) ContainsRule(path string) bool {
	if p == nil {
		return false
	}
	_, ok := p.rules[path]
	return ok
}

// Summary returns "profile: N rules, N evals, N successes".
// A nil profile returns "profile: disabled".
func (p *EvalProfile) Summary() string {
	if p == nil {
		return "profile: disabled"
	}
	evals, successes := p.totals()
	return "profile: " + strconv.Itoa(len(p.rules)) + " rules, " + strconv.Itoa(evals) + " evals, " + strconv.Itoa(successes) + " successes"
}

// Equal reports whether p and other record the same rule counts.
// Two nil profiles are equal. A nil profile is not equal to a non-nil profile.
func (p *EvalProfile) Equal(other *EvalProfile) bool {
	if p == nil || other == nil {
		return p == other
	}
	if len(p.rules) != len(other.rules) {
		return false
	}
	for path, st := range p.rules {
		ost := other.rules[path]
		if st == nil || ost == nil || st.Evals != ost.Evals || st.Successes != ost.Successes {
			return false
		}
	}
	return true
}

// String returns a multiline listing of tracked rules sorted by path.
// A nil profile returns "<nil>".
func (p *EvalProfile) String() string {
	if p == nil {
		return "<nil>"
	}
	var b strings.Builder
	b.WriteString("Profile:\n")
	for _, path := range p.RulePaths() {
		b.WriteString("  ")
		b.WriteString(path)
		b.WriteString(": ")
		b.WriteString(p.rules[path].String())
		b.WriteByte('\n')
	}
	return b.String()
}

// Diff compares p with other. Rules only in other are added, rules only in p
// are removed, and rules in both with different counts are changed. Deltas are
// other minus p. A nil receiver returns nil. A nil other is treated as empty,
// so every rule in p is removed.
func (p *EvalProfile) Diff(other *EvalProfile) *ProfileDiff {
	if p == nil {
		return nil
	}
	diff := &ProfileDiff{}
	var otherRules map[string]*RuleStat
	if other != nil {
		otherRules = other.rules
	}
	for path, st := range p.rules {
		ost, ok := otherRules[path]
		if !ok {
			if diff.Removed == nil {
				diff.Removed = map[string]*RuleStat{}
			}
			diff.Removed[path] = st.clone()
			continue
		}
		ev, su := statCounts(st)
		oev, osu := statCounts(ost)
		if ev != oev || su != osu {
			if diff.Changed == nil {
				diff.Changed = map[string]*RuleStatDelta{}
			}
			diff.Changed[path] = &RuleStatDelta{
				EvalsDelta:     oev - ev,
				SuccessesDelta: osu - su,
			}
		}
	}
	for path, ost := range otherRules {
		if _, ok := p.rules[path]; ok {
			continue
		}
		if diff.Added == nil {
			diff.Added = map[string]*RuleStat{}
		}
		diff.Added[path] = ost.clone()
	}
	return diff
}

// HasChanges reports whether any rules were added, removed, or changed.
// A nil diff returns false.
func (d *ProfileDiff) HasChanges() bool {
	if d == nil {
		return false
	}
	return len(d.Added) > 0 || len(d.Removed) > 0 || len(d.Changed) > 0
}

// SuccessRate returns Successes/Evals. It returns 0 when s is nil or Evals is 0.
func (s *RuleStat) SuccessRate() float64 {
	if s == nil || s.Evals == 0 {
		return 0
	}
	return float64(s.Successes) / float64(s.Evals)
}

// String returns "evals=N successes=N". A nil stat returns "<nil>".
func (s *RuleStat) String() string {
	if s == nil {
		return "<nil>"
	}
	return "evals=" + strconv.Itoa(s.Evals) + " successes=" + strconv.Itoa(s.Successes)
}

func (s *RuleStat) clone() *RuleStat {
	if s == nil {
		return nil
	}
	c := *s
	return &c
}

func (p *EvalProfile) totals() (evals, successes int) {
	for _, st := range p.rules {
		if st == nil {
			continue
		}
		evals += st.Evals
		successes += st.Successes
	}
	return evals, successes
}

func (p *EvalProfile) pathsWhere(match func(*RuleStat) bool) []string {
	if p == nil || len(p.rules) == 0 {
		return nil
	}
	paths := make([]string, 0, len(p.rules))
	for path, st := range p.rules {
		if st != nil && match(st) {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	slices.Sort(paths)
	return paths
}

func packageName(path string) string {
	i := strings.LastIndexByte(path, '.')
	if i < 0 {
		return ""
	}
	return path[:i]
}

func statCounts(st *RuleStat) (evals, successes int) {
	if st == nil {
		return 0, 0
	}
	return st.Evals, st.Successes
}
