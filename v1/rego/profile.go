// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

//go:build profile

package rego

import (
	"slices"
	"strconv"
	"strings"

	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/topdown"
)

// ruleProfileResult is embedded in Result so the Profile field exists only
// when OPA is built with the "profile" tag.
type ruleProfileResult struct {
	// Profile holds per-rule evaluation counts for this query.
	// It is nil when rule profiling is not enabled.
	Profile *EvalProfile `json:"profile,omitempty"`
}

// ruleProfileConfig is embedded in Rego and EvalContext.
type ruleProfileConfig struct {
	ruleProfile bool
}

// EvalProfile maps each fully qualified rule path to evaluation counts.
// A rule with multiple definitions shares one path and is counted once per
// definition that evaluation enters.
type EvalProfile struct {
	Rules map[string]*RuleStat `json:"rules,omitempty"`
}

// RuleStat counts how many times a rule was entered and how many of those
// entries produced a value.
type RuleStat struct {
	Evals     int `json:"evals"`
	Successes int `json:"successes"`
}

// ProfileDiff is the result of comparing two evaluation profiles.
// Added rules are present only in the other profile, Removed rules are present
// only in the receiver, and Changed rules are present in both with different
// counts. Empty fields are nil rather than empty maps.
type ProfileDiff struct {
	Added   map[string]*RuleStat      `json:"added,omitempty"`
	Removed map[string]*RuleStat      `json:"removed,omitempty"`
	Changed map[string]*RuleStatDelta `json:"changed,omitempty"`
}

// RuleStatDelta is the change in counts from the receiver profile to the other
// profile (other minus receiver).
type RuleStatDelta struct {
	EvalsDelta     int `json:"evals_delta"`
	SuccessesDelta int `json:"successes_delta"`
}

// EnableRuleProfile turns rule-evaluation profiling on or off for queries
// prepared from this Rego object. EvalRuleProfile overrides it for one
// evaluation. The option is available when OPA is built with the "profile" tag.
func EnableRuleProfile(yes bool) func(*Rego) {
	return func(r *Rego) {
		if r == nil {
			return
		}
		r.ruleProfile = yes
	}
}

// EvalRuleProfile turns rule-evaluation profiling on or off for one
// evaluation. It overrides the value set by EnableRuleProfile.
func EvalRuleProfile(yes bool) EvalOption {
	return func(e *EvalContext) {
		if e == nil {
			return
		}
		e.ruleProfile = yes
	}
}

func applyStoredRuleProfile(ectx *EvalContext, r *Rego) {
	if ectx == nil || r == nil {
		return
	}
	ectx.ruleProfile = r.ruleProfile
}

// Stat returns the counts recorded for rule, or nil when rule was not tracked.
func (p *EvalProfile) Stat(rule string) *RuleStat {
	if p == nil {
		return nil
	}
	return p.Rules[rule]
}

// RulePaths returns the tracked rule paths in sorted order.
// It returns nil when p is nil or no rules were tracked.
func (p *EvalProfile) RulePaths() []string {
	if p == nil || len(p.Rules) == 0 {
		return nil
	}
	paths := make([]string, 0, len(p.Rules))
	for path := range p.Rules {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths
}

// SuccessRate returns Successes/Evals for rule.
// It returns 0 when p is nil, rule is untracked, or the rule was never evaluated.
func (p *EvalProfile) SuccessRate(rule string) float64 {
	if p == nil {
		return 0
	}
	return p.Rules[rule].SuccessRate()
}

// OverallSuccessRate returns the aggregate Successes/Evals across every tracked rule.
// It returns 0 when p is nil or no rule was evaluated.
func (p *EvalProfile) OverallSuccessRate() float64 {
	if p == nil {
		return 0
	}
	var evals, successes int
	for _, st := range p.Rules {
		if st == nil {
			continue
		}
		evals += st.Evals
		successes += st.Successes
	}
	if evals == 0 {
		return 0
	}
	return float64(successes) / float64(evals)
}

// HotRules returns sorted rule paths whose Evals count is at least minEvals.
// It returns nil when p is nil or no rule qualifies.
func (p *EvalProfile) HotRules(minEvals int) []string {
	return p.pathsWhere(func(st *RuleStat) bool {
		return st.Evals >= minEvals
	})
}

// FailedRules returns sorted rule paths that were evaluated and never succeeded.
// It returns nil when p is nil or no rule qualifies.
func (p *EvalProfile) FailedRules() []string {
	return p.pathsWhere(func(st *RuleStat) bool {
		return st.Evals > 0 && st.Successes == 0
	})
}

// SucceededRules returns sorted rule paths that succeeded at least once.
// It returns nil when p is nil or no rule qualifies.
func (p *EvalProfile) SucceededRules() []string {
	return p.pathsWhere(func(st *RuleStat) bool {
		return st.Successes > 0
	})
}

// Packages returns the sorted unique package names derived from tracked rule paths.
// The package is the path with its final dot-separated segment removed, so
// "data.authz.allow" yields "data.authz". It returns nil when p is nil or empty.
func (p *EvalProfile) Packages() []string {
	if p == nil || len(p.Rules) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(p.Rules))
	pkgs := make([]string, 0, len(p.Rules))
	for path := range p.Rules {
		pkg := packageName(path)
		if _, ok := seen[pkg]; ok {
			continue
		}
		seen[pkg] = struct{}{}
		pkgs = append(pkgs, pkg)
	}
	if len(pkgs) == 0 {
		return nil
	}
	slices.Sort(pkgs)
	return pkgs
}

// FilterByPackage returns a new profile containing deep-copied stats for rules
// in pkg. It returns nil when p is nil.
func (p *EvalProfile) FilterByPackage(pkg string) *EvalProfile {
	if p == nil {
		return nil
	}
	out := &EvalProfile{}
	for path, st := range p.Rules {
		if packageName(path) != pkg {
			continue
		}
		if out.Rules == nil {
			out.Rules = make(map[string]*RuleStat)
		}
		out.Rules[path] = cloneStat(st)
	}
	return out
}

// Merge returns a profile whose counts are the sum of p and other.
// It returns nil when both profiles are nil, and returns the non-nil profile
// unchanged when the other side is nil.
func (p *EvalProfile) Merge(other *EvalProfile) *EvalProfile {
	if p == nil {
		return other
	}
	if other == nil {
		return p
	}
	out := &EvalProfile{
		Rules: make(map[string]*RuleStat, len(p.Rules)+len(other.Rules)),
	}
	for path, st := range p.Rules {
		out.addStat(path, st)
	}
	for path, st := range other.Rules {
		out.addStat(path, st)
	}
	return out
}

// PackageStats aggregates rule counts by package name.
// It returns nil when p is nil or no rules are tracked.
func (p *EvalProfile) PackageStats() map[string]*RuleStat {
	if p == nil || len(p.Rules) == 0 {
		return nil
	}
	out := make(map[string]*RuleStat)
	for path, st := range p.Rules {
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

// ContainsRule reports whether path is tracked. It returns false when p is nil.
func (p *EvalProfile) ContainsRule(path string) bool {
	if p == nil {
		return false
	}
	_, ok := p.Rules[path]
	return ok
}

// Summary returns a one-line description of the profile.
// A nil profile reports "profile: disabled".
func (p *EvalProfile) Summary() string {
	if p == nil {
		return "profile: disabled"
	}
	var evals, successes int
	for _, st := range p.Rules {
		if st == nil {
			continue
		}
		evals += st.Evals
		successes += st.Successes
	}
	return "profile: " + strconv.Itoa(len(p.Rules)) + " rules, " + strconv.Itoa(evals) + " evals, " + strconv.Itoa(successes) + " successes"
}

// Equal reports whether p and other record the same rule counts.
// Two nil profiles are equal. A nil profile is not equal to a non-nil profile.
func (p *EvalProfile) Equal(other *EvalProfile) bool {
	if p == nil || other == nil {
		return p == nil && other == nil
	}
	if len(p.Rules) != len(other.Rules) {
		return false
	}
	for path, st := range p.Rules {
		ost, ok := other.Rules[path]
		if !ok || !ruleStatsEqual(st, ost) {
			return false
		}
	}
	return true
}

// String renders the profile with one sorted line per rule.
// A nil profile renders as "<nil>".
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
		b.WriteString(p.Rules[path].String())
		b.WriteByte('\n')
	}
	return b.String()
}

// Diff compares p with other. Rules only in other are Added, rules only in p
// are Removed, and shared rules with different counts are Changed. Deltas are
// other minus p. A nil receiver returns nil.
func (p *EvalProfile) Diff(other *EvalProfile) *ProfileDiff {
	if p == nil {
		return nil
	}

	var otherRules map[string]*RuleStat
	if other != nil {
		otherRules = other.Rules
	}

	diff := &ProfileDiff{}
	seen := make(map[string]struct{}, len(p.Rules))
	for path, st := range p.Rules {
		seen[path] = struct{}{}
		ost, ok := otherRules[path]
		if !ok {
			if diff.Removed == nil {
				diff.Removed = make(map[string]*RuleStat)
			}
			diff.Removed[path] = st
			continue
		}
		e1, s1 := statCounts(st)
		e2, s2 := statCounts(ost)
		if e1 == e2 && s1 == s2 {
			continue
		}
		if diff.Changed == nil {
			diff.Changed = make(map[string]*RuleStatDelta)
		}
		diff.Changed[path] = &RuleStatDelta{
			EvalsDelta:     e2 - e1,
			SuccessesDelta: s2 - s1,
		}
	}
	for path, ost := range otherRules {
		if _, ok := seen[path]; ok {
			continue
		}
		if diff.Added == nil {
			diff.Added = make(map[string]*RuleStat)
		}
		diff.Added[path] = ost
	}
	return diff
}

// HasChanges reports whether the diff recorded any added, removed, or changed rules.
// It returns false when d is nil.
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

// String returns "evals=N successes=N". A nil stat renders as "<nil>".
func (s *RuleStat) String() string {
	if s == nil {
		return "<nil>"
	}
	return "evals=" + strconv.Itoa(s.Evals) + " successes=" + strconv.Itoa(s.Successes)
}

func (p *EvalProfile) pathsWhere(match func(*RuleStat) bool) []string {
	if p == nil || len(p.Rules) == 0 {
		return nil
	}
	paths := make([]string, 0, len(p.Rules))
	for path, st := range p.Rules {
		if st == nil || !match(st) {
			continue
		}
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		return nil
	}
	slices.Sort(paths)
	return paths
}

func (p *EvalProfile) addStat(path string, st *RuleStat) {
	if p.Rules == nil {
		p.Rules = make(map[string]*RuleStat)
	}
	existing := p.Rules[path]
	if existing == nil {
		existing = &RuleStat{}
		p.Rules[path] = existing
	}
	evals, successes := statCounts(st)
	existing.Evals += evals
	existing.Successes += successes
}

func packageName(path string) string {
	if i := strings.LastIndex(path, "."); i > 0 {
		return path[:i]
	}
	return path
}

func cloneStat(st *RuleStat) *RuleStat {
	if st == nil {
		return nil
	}
	cpy := *st
	return &cpy
}

func ruleStatsEqual(a, b *RuleStat) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Evals == b.Evals && a.Successes == b.Successes
}

func statCounts(st *RuleStat) (int, int) {
	if st == nil {
		return 0, 0
	}
	return st.Evals, st.Successes
}

// ruleProfiler records Enter and Exit events for rules while a query runs.
// Enter counts an evaluation of one rule definition, including definitions
// whose body fails. Exit counts a successful solution of that definition.
type ruleProfiler struct {
	profile *EvalProfile
}

func beginRuleProfile(ectx *EvalContext) *ruleProfiler {
	if ectx == nil || !ectx.ruleProfile {
		return nil
	}
	return &ruleProfiler{
		profile: &EvalProfile{Rules: map[string]*RuleStat{}},
	}
}

func attachRuleProfiler(q *topdown.Query, p *ruleProfiler) *topdown.Query {
	if q == nil || p == nil {
		return q
	}
	return q.WithQueryTracer(p)
}

func finishRuleProfile(rs ResultSet, p *ruleProfiler) ResultSet {
	if p == nil || p.profile == nil {
		if len(rs) == 0 {
			return nil
		}
		return rs
	}
	prof := p.profile
	if len(rs) == 0 {
		// The query is undefined, but rules were still entered. Surface the
		// profile on a single empty result so failed rules remain observable.
		if len(prof.Rules) == 0 {
			return nil
		}
		result := newResult()
		result.Profile = prof
		return ResultSet{result}
	}
	for i := range rs {
		rs[i].Profile = prof
	}
	return rs
}

func (p *ruleProfiler) Enabled() bool { return p != nil }

func (*ruleProfiler) Config() topdown.TraceConfig {
	return topdown.TraceConfig{PlugLocalVars: false}
}

func (p *ruleProfiler) TraceEvent(evt topdown.Event) {
	rule, ok := evt.Node.(*ast.Rule)
	if !ok || rule == nil || p == nil || p.profile == nil {
		return
	}
	switch evt.Op {
	case topdown.EnterOp:
		p.stat(ast.RulePath(rule)).Evals++
	case topdown.ExitOp:
		p.stat(ast.RulePath(rule)).Successes++
	}
}

func (p *ruleProfiler) stat(path string) *RuleStat {
	if p.profile.Rules == nil {
		p.profile.Rules = make(map[string]*RuleStat)
	}
	st := p.profile.Rules[path]
	if st == nil {
		st = &RuleStat{}
		p.profile.Rules[path] = st
	}
	return st
}
