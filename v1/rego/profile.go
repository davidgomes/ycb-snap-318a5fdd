package rego

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/topdown"
)

// RuleStat holds evaluation counters for a single rule.
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

// EvalProfile maps fully qualified rule paths to their evaluation counters.
type EvalProfile struct {
	Rules map[string]*RuleStat `json:"rules"`
}

func newEvalProfile() *EvalProfile {
	return &EvalProfile{Rules: map[string]*RuleStat{}}
}

// Stat returns the counters for rule, or nil if it is not tracked.
func (p *EvalProfile) Stat(rule string) *RuleStat {
	if p == nil {
		return nil
	}
	return p.Rules[rule]
}

func (p *EvalProfile) sortedPaths(keep func(*RuleStat) bool) []string {
	if p == nil {
		return nil
	}
	var out []string
	for k, v := range p.Rules {
		if keep(v) {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// RulePaths returns the sorted tracked rule paths.
func (p *EvalProfile) RulePaths() []string {
	return p.sortedPaths(func(*RuleStat) bool { return true })
}

// SuccessRate returns the success rate of rule, or 0 if untracked.
func (p *EvalProfile) SuccessRate(rule string) float64 {
	return p.Stat(rule).SuccessRate()
}

// OverallSuccessRate returns aggregate Successes/Evals across all rules.
func (p *EvalProfile) OverallSuccessRate() float64 {
	if p == nil {
		return 0
	}
	total := &RuleStat{}
	for _, v := range p.Rules {
		total.Evals += v.Evals
		total.Successes += v.Successes
	}
	return total.SuccessRate()
}

// HotRules returns sorted rules evaluated at least minEvals times.
func (p *EvalProfile) HotRules(minEvals int) []string {
	return p.sortedPaths(func(s *RuleStat) bool { return s.Evals >= minEvals })
}

// FailedRules returns sorted rules that were evaluated but never succeeded.
func (p *EvalProfile) FailedRules() []string {
	return p.sortedPaths(func(s *RuleStat) bool { return s.Evals > 0 && s.Successes == 0 })
}

// SucceededRules returns sorted rules that succeeded at least once.
func (p *EvalProfile) SucceededRules() []string {
	return p.sortedPaths(func(s *RuleStat) bool { return s.Successes > 0 })
}

func packageOf(path string) string {
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[:i]
	}
	return path
}

// Packages returns the sorted unique package names of tracked rules.
func (p *EvalProfile) Packages() []string {
	if p == nil {
		return nil
	}
	set := map[string]struct{}{}
	for k := range p.Rules {
		set[packageOf(k)] = struct{}{}
	}
	if len(set) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(set))
}

// FilterByPackage returns a new profile containing copies of the stats for
// rules in package pkg.
func (p *EvalProfile) FilterByPackage(pkg string) *EvalProfile {
	if p == nil {
		return nil
	}
	out := newEvalProfile()
	for k, v := range p.Rules {
		if packageOf(k) == pkg {
			c := *v
			out.Rules[k] = &c
		}
	}
	return out
}

// Merge returns a new profile with the counts of p and other summed.
func (p *EvalProfile) Merge(other *EvalProfile) *EvalProfile {
	switch {
	case p == nil:
		return other
	case other == nil:
		return p
	}
	out := newEvalProfile()
	for _, src := range []*EvalProfile{p, other} {
		for k, v := range src.Rules {
			s, ok := out.Rules[k]
			if !ok {
				s = &RuleStat{}
				out.Rules[k] = s
			}
			s.Evals += v.Evals
			s.Successes += v.Successes
		}
	}
	return out
}

// PackageStats returns aggregated stats per package.
func (p *EvalProfile) PackageStats() map[string]*RuleStat {
	if p == nil {
		return nil
	}
	out := map[string]*RuleStat{}
	for k, v := range p.Rules {
		pkg := packageOf(k)
		s, ok := out[pkg]
		if !ok {
			s = &RuleStat{}
			out[pkg] = s
		}
		s.Evals += v.Evals
		s.Successes += v.Successes
	}
	return out
}

// ContainsRule reports whether path is tracked.
func (p *EvalProfile) ContainsRule(path string) bool {
	return p.Stat(path) != nil
}

// Summary returns a one-line summary of the profile.
func (p *EvalProfile) Summary() string {
	if p == nil {
		return "profile: disabled"
	}
	var evals, successes int
	for _, v := range p.Rules {
		evals += v.Evals
		successes += v.Successes
	}
	return fmt.Sprintf("profile: %d rules, %d evals, %d successes", len(p.Rules), evals, successes)
}

// Equal reports whether p and other hold the same stats.
func (p *EvalProfile) Equal(other *EvalProfile) bool {
	if p == nil || other == nil {
		return p == nil && other == nil
	}
	return maps.EqualFunc(p.Rules, other.Rules, func(a, b *RuleStat) bool { return *a == *b })
}

func (p *EvalProfile) String() string {
	if p == nil {
		return "<nil>"
	}
	var sb strings.Builder
	sb.WriteString("Profile:\n")
	for _, k := range p.RulePaths() {
		fmt.Fprintf(&sb, "  %s: %s\n", k, p.Rules[k])
	}
	return sb.String()
}

// RuleStatDelta holds the difference in counters (other minus receiver).
type RuleStatDelta struct {
	EvalsDelta     int `json:"evals_delta"`
	SuccessesDelta int `json:"successes_delta"`
}

// ProfileDiff describes the differences between two profiles.
type ProfileDiff struct {
	Added   map[string]*RuleStat      `json:"added,omitempty"`
	Removed map[string]*RuleStat      `json:"removed,omitempty"`
	Changed map[string]*RuleStatDelta `json:"changed,omitempty"`
}

// HasChanges reports whether the diff contains any differences.
func (d *ProfileDiff) HasChanges() bool {
	return d != nil && (d.Added != nil || d.Removed != nil || d.Changed != nil)
}

// Diff compares p against other.
func (p *EvalProfile) Diff(other *EvalProfile) *ProfileDiff {
	if p == nil {
		return nil
	}
	var otherRules map[string]*RuleStat
	if other != nil {
		otherRules = other.Rules
	}
	d := &ProfileDiff{}
	for k, v := range p.Rules {
		o, ok := otherRules[k]
		if !ok {
			if d.Removed == nil {
				d.Removed = map[string]*RuleStat{}
			}
			c := *v
			d.Removed[k] = &c
			continue
		}
		if *o != *v {
			if d.Changed == nil {
				d.Changed = map[string]*RuleStatDelta{}
			}
			d.Changed[k] = &RuleStatDelta{EvalsDelta: o.Evals - v.Evals, SuccessesDelta: o.Successes - v.Successes}
		}
	}
	for k, v := range otherRules {
		if _, ok := p.Rules[k]; !ok {
			if d.Added == nil {
				d.Added = map[string]*RuleStat{}
			}
			c := *v
			d.Added[k] = &c
		}
	}
	return d
}

// ruleProfiler is a query tracer that records rule entries and successes.
type ruleProfiler struct {
	profile   *EvalProfile
	enters    map[uint64]*RuleStat
	succeeded map[uint64]struct{}
}

func newRuleProfiler() *ruleProfiler {
	return &ruleProfiler{
		profile:   newEvalProfile(),
		enters:    map[uint64]*RuleStat{},
		succeeded: map[uint64]struct{}{},
	}
}

func (*ruleProfiler) Enabled() bool { return true }

func (*ruleProfiler) Config() topdown.TraceConfig { return topdown.TraceConfig{} }

func (rp *ruleProfiler) TraceEvent(evt topdown.Event) {
	rule, ok := evt.Node.(*ast.Rule)
	if !ok {
		return
	}
	switch evt.Op {
	case topdown.EnterOp:
		path := rule.Path().String()
		s, ok := rp.profile.Rules[path]
		if !ok {
			s = &RuleStat{}
			rp.profile.Rules[path] = s
		}
		s.Evals++
		rp.enters[evt.QueryID] = s
		delete(rp.succeeded, evt.QueryID)
	case topdown.ExitOp:
		s, ok := rp.enters[evt.QueryID]
		if !ok {
			return
		}
		if _, done := rp.succeeded[evt.QueryID]; !done {
			rp.succeeded[evt.QueryID] = struct{}{}
			s.Successes++
		}
	}
}
