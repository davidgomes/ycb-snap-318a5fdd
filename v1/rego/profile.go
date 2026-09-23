// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package rego

import (
	"fmt"
	"sort"
	"strings"
)

// EvalProfile maps fully qualified rule paths to evaluation statistics.
// A nil *EvalProfile means rule profiling is disabled.
type EvalProfile struct {
	rules map[string]*RuleStat
}

// RuleStat counts how many times a rule was entered and how many of those
// entries succeeded.
type RuleStat struct {
	Evals     int
	Successes int
}

// ProfileDiff is the result of comparing two evaluation profiles.
// Empty categories are nil rather than empty maps.
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

// Stat returns the statistics recorded for rule, or nil when rule is untracked.
// A nil profile returns nil.
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
	return sortedPaths(p.rules, func(string, *RuleStat) bool { return true })
}

// SuccessRate returns Successes/Evals for rule.
// Untracked rules, rules with zero evaluations, and a nil profile return 0.
func (p *EvalProfile) SuccessRate(rule string) float64 {
	if p == nil {
		return 0
	}
	return p.rules[rule].SuccessRate()
}

// OverallSuccessRate returns the aggregate Successes/Evals across every tracked
// rule. A nil profile or a profile with zero evaluations returns 0.
func (p *EvalProfile) OverallSuccessRate() float64 {
	if p == nil {
		return 0
	}
	var evals, successes int
	for _, st := range p.rules {
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
// It returns nil when the profile is nil or no rule qualifies.
func (p *EvalProfile) HotRules(minEvals int) []string {
	if p == nil {
		return nil
	}
	return sortedPaths(p.rules, func(_ string, st *RuleStat) bool {
		return st != nil && st.Evals >= minEvals
	})
}

// FailedRules returns sorted rule paths that were entered and never succeeded.
// It returns nil when the profile is nil or no rule qualifies.
func (p *EvalProfile) FailedRules() []string {
	if p == nil {
		return nil
	}
	return sortedPaths(p.rules, func(_ string, st *RuleStat) bool {
		return st != nil && st.Evals > 0 && st.Successes == 0
	})
}

// SucceededRules returns sorted rule paths that succeeded at least once.
// It returns nil when the profile is nil or no rule qualifies.
func (p *EvalProfile) SucceededRules() []string {
	if p == nil {
		return nil
	}
	return sortedPaths(p.rules, func(_ string, st *RuleStat) bool {
		return st != nil && st.Successes > 0
	})
}

// Packages returns the sorted unique package names derived from tracked rule
// paths. The package of "data.authz.allow" is "data.authz".
// A nil profile returns nil.
func (p *EvalProfile) Packages() []string {
	if p == nil || len(p.rules) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(p.rules))
	for path := range p.rules {
		pkg, ok := rulePackage(path)
		if !ok {
			continue
		}
		seen[pkg] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for pkg := range seen {
		out = append(out, pkg)
	}
	sort.Strings(out)
	return out
}

// FilterByPackage returns a new profile containing deep-copied stats for rules
// whose package is pkg. A nil profile returns nil.
func (p *EvalProfile) FilterByPackage(pkg string) *EvalProfile {
	if p == nil {
		return nil
	}
	out := &EvalProfile{}
	for path, st := range p.rules {
		rulePkg, ok := rulePackage(path)
		if !ok || rulePkg != pkg || st == nil {
			continue
		}
		if out.rules == nil {
			out.rules = make(map[string]*RuleStat)
		}
		cp := *st
		out.rules[path] = &cp
	}
	return out
}

// Merge returns a profile whose counts are the sum of p and other.
// Both nil yields nil. When only one side is nil, that side is returned as-is.
func (p *EvalProfile) Merge(other *EvalProfile) *EvalProfile {
	if p == nil {
		return other
	}
	if other == nil {
		return p
	}
	out := &EvalProfile{rules: make(map[string]*RuleStat, len(p.rules)+len(other.rules))}
	for path, st := range p.rules {
		if st == nil {
			continue
		}
		cp := *st
		out.rules[path] = &cp
	}
	for path, st := range other.rules {
		if st == nil {
			continue
		}
		if existing := out.rules[path]; existing != nil {
			existing.Evals += st.Evals
			existing.Successes += st.Successes
			continue
		}
		cp := *st
		out.rules[path] = &cp
	}
	return out
}

// PackageStats aggregates Evals and Successes by package.
// A nil profile returns nil.
func (p *EvalProfile) PackageStats() map[string]*RuleStat {
	if p == nil || len(p.rules) == 0 {
		return nil
	}
	out := make(map[string]*RuleStat, len(p.rules))
	for path, st := range p.rules {
		if st == nil {
			continue
		}
		pkg, ok := rulePackage(path)
		if !ok {
			continue
		}
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
	st, ok := p.rules[path]
	return ok && st != nil
}

// Summary returns a one-line description of the profile.
// A nil profile returns "profile: disabled".
func (p *EvalProfile) Summary() string {
	if p == nil {
		return "profile: disabled"
	}
	var evals, successes int
	for _, st := range p.rules {
		if st == nil {
			continue
		}
		evals += st.Evals
		successes += st.Successes
	}
	return fmt.Sprintf("profile: %d rules, %d evals, %d successes", len(p.rules), evals, successes)
}

// Equal reports whether p and other contain the same rule paths and counts.
// Two nil profiles are equal. A nil profile is not equal to a non-nil profile.
func (p *EvalProfile) Equal(other *EvalProfile) bool {
	if p == nil || other == nil {
		return p == other
	}
	if len(p.rules) != len(other.rules) {
		return false
	}
	for path, st := range p.rules {
		ot := other.rules[path]
		if st == nil || ot == nil {
			if st != ot {
				return false
			}
			continue
		}
		if st.Evals != ot.Evals || st.Successes != ot.Successes {
			return false
		}
	}
	return true
}

// String formats the profile with a header and one sorted line per rule.
// A nil profile returns "<nil>".
func (p *EvalProfile) String() string {
	if p == nil {
		return "<nil>"
	}
	var b strings.Builder
	b.WriteString("Profile:\n")
	for _, path := range p.RulePaths() {
		fmt.Fprintf(&b, "  %s: %s\n", path, p.rules[path].String())
	}
	return b.String()
}

// Diff compares p with other. Added rules exist only in other, removed rules
// exist only in p, and changed rules exist in both with different counts.
// Deltas are other minus p. A nil receiver returns nil.
func (p *EvalProfile) Diff(other *EvalProfile) *ProfileDiff {
	if p == nil {
		return nil
	}
	diff := &ProfileDiff{}
	if other == nil {
		other = &EvalProfile{}
	}
	for path, st := range p.rules {
		if st == nil {
			continue
		}
		ot := other.rules[path]
		if ot == nil {
			if diff.Removed == nil {
				diff.Removed = make(map[string]*RuleStat)
			}
			diff.Removed[path] = st
			continue
		}
		if st.Evals != ot.Evals || st.Successes != ot.Successes {
			if diff.Changed == nil {
				diff.Changed = make(map[string]*RuleStatDelta)
			}
			diff.Changed[path] = &RuleStatDelta{
				EvalsDelta:     ot.Evals - st.Evals,
				SuccessesDelta: ot.Successes - st.Successes,
			}
		}
	}
	for path, st := range other.rules {
		if st == nil {
			continue
		}
		if p.rules[path] == nil {
			if diff.Added == nil {
				diff.Added = make(map[string]*RuleStat)
			}
			diff.Added[path] = st
		}
	}
	return diff
}

// HasChanges reports whether the diff records any added, removed, or changed
// rules. A nil diff returns false.
func (d *ProfileDiff) HasChanges() bool {
	if d == nil {
		return false
	}
	return len(d.Added) > 0 || len(d.Removed) > 0 || len(d.Changed) > 0
}

// SuccessRate returns Successes/Evals. It returns 0 when the stat is nil or
// Evals is 0.
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
	return fmt.Sprintf("evals=%d successes=%d", s.Evals, s.Successes)
}

func (p *EvalProfile) observe(path string, success bool) {
	if p == nil || path == "" {
		return
	}
	if p.rules == nil {
		p.rules = make(map[string]*RuleStat)
	}
	st := p.rules[path]
	if st == nil {
		st = &RuleStat{}
		p.rules[path] = st
	}
	if success {
		st.Successes++
		return
	}
	st.Evals++
}

func sortedPaths(rules map[string]*RuleStat, keep func(string, *RuleStat) bool) []string {
	if len(rules) == 0 {
		return nil
	}
	out := make([]string, 0, len(rules))
	for path, st := range rules {
		if keep(path, st) {
			out = append(out, path)
		}
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

func rulePackage(path string) (string, bool) {
	i := strings.LastIndex(path, ".")
	if i <= 0 {
		return "", false
	}
	return path[:i], true
}
