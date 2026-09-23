//go:build profile

package rego

import (
	"fmt"
	"slices"
	"strings"

	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/topdown"
)

// EvalProfile records how often each rule was entered and succeeded during
// an evaluation. Rules are keyed by their fully qualified path, e.g.
// "data.authz.allow". A rule with multiple definitions is counted once per
// definition entered.
type EvalProfile struct {
	Rules map[string]*RuleStat `json:"rules"`
}

// RuleStat holds the evaluation counts for a single rule path.
type RuleStat struct {
	Evals     int `json:"evals"`
	Successes int `json:"successes"`
}

// ProfileDiff describes the differences between two profiles. Each field is
// nil when there are no entries of that kind.
type ProfileDiff struct {
	Added   map[string]*RuleStat      `json:"added,omitempty"`
	Removed map[string]*RuleStat      `json:"removed,omitempty"`
	Changed map[string]*RuleStatDelta `json:"changed,omitempty"`
}

// RuleStatDelta holds the change in counts for a rule present in both
// profiles of a diff.
type RuleStatDelta struct {
	EvalsDelta     int `json:"evals_delta"`
	SuccessesDelta int `json:"successes_delta"`
}

// EvalRuleProfile enables or disables rule profiling for a Prepared Query's
// evaluation. When enabled, each Result carries the evaluation's profile.
func EvalRuleProfile(enabled bool) EvalOption {
	return func(e *EvalContext) {
		e.ruleProfile = enabled
	}
}

// EnableRuleProfile enables or disables rule profiling by default for
// evaluations of this Rego object. EvalRuleProfile overrides it per evaluation.
func EnableRuleProfile(enabled bool) func(r *Rego) {
	return func(r *Rego) {
		r.ruleProfile = enabled
	}
}

func withRuleProfiler(q *topdown.Query, ectx *EvalContext) (*topdown.Query, *EvalProfile) {
	if !ectx.ruleProfile {
		return q, nil
	}
	p := &ruleProfiler{
		profile: newEvalProfile(),
		stats:   map[*ast.Rule]*RuleStat{},
		pending: map[uint64]*RuleStat{},
	}
	return q.WithQueryTracer(p), p.profile
}

// ruleProfiler is a query tracer that counts rule entries and successes.
type ruleProfiler struct {
	profile *EvalProfile
	stats   map[*ast.Rule]*RuleStat
	// pending maps the query ID of each entered rule body to its stat until
	// the body first succeeds, so a rule producing multiple values is only
	// counted as one success per entry.
	pending map[uint64]*RuleStat
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
		stat := p.statFor(rule)
		stat.Evals++
		p.pending[evt.QueryID] = stat
	case topdown.ExitOp:
		if stat, ok := p.pending[evt.QueryID]; ok {
			stat.Successes++
			delete(p.pending, evt.QueryID)
		}
	}
}

func (p *ruleProfiler) statFor(rule *ast.Rule) *RuleStat {
	if stat, ok := p.stats[rule]; ok {
		return stat
	}
	var path string
	if rule.Module != nil {
		path = rule.Path().String()
	} else {
		path = rule.Head.Ref().GroundPrefix().String()
	}
	stat, ok := p.profile.Rules[path]
	if !ok {
		stat = &RuleStat{}
		p.profile.Rules[path] = stat
	}
	p.stats[rule] = stat
	return stat
}

func newEvalProfile() *EvalProfile {
	return &EvalProfile{Rules: map[string]*RuleStat{}}
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
	if s == nil {
		return &RuleStat{}
	}
	c := *s
	return &c
}

func (s *RuleStat) equal(other *RuleStat) bool {
	if s == nil || other == nil {
		return s == other
	}
	return *s == *other
}

// Stat returns the stats recorded for rule, or nil if it is not tracked.
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

// RulePaths returns the sorted tracked rule paths, or nil if there are none.
func (p *EvalProfile) RulePaths() []string {
	return p.sortedPaths(func(*RuleStat) bool { return true })
}

// SuccessRate returns Successes/Evals for rule, or 0 if the rule is not
// tracked or was never evaluated.
func (p *EvalProfile) SuccessRate(rule string) float64 {
	return p.Stat(rule).SuccessRate()
}

// OverallSuccessRate returns the aggregate Successes/Evals across all rules.
func (p *EvalProfile) OverallSuccessRate() float64 {
	return p.total().SuccessRate()
}

// HotRules returns the sorted paths of rules evaluated at least minEvals
// times, or nil if none qualify.
func (p *EvalProfile) HotRules(minEvals int) []string {
	return p.sortedPaths(func(s *RuleStat) bool { return s != nil && s.Evals >= minEvals })
}

// FailedRules returns the sorted paths of rules that were evaluated but never
// succeeded, or nil if there are none.
func (p *EvalProfile) FailedRules() []string {
	return p.sortedPaths(func(s *RuleStat) bool { return s != nil && s.Evals > 0 && s.Successes == 0 })
}

// SucceededRules returns the sorted paths of rules that succeeded at least
// once, or nil if there are none.
func (p *EvalProfile) SucceededRules() []string {
	return p.sortedPaths(func(s *RuleStat) bool { return s != nil && s.Successes > 0 })
}

// Packages returns the sorted unique package paths of the tracked rules, e.g.
// "data.authz" for "data.authz.allow", or nil if there are none.
func (p *EvalProfile) Packages() []string {
	if p == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var pkgs []string
	for path := range p.Rules {
		pkg := packageOf(path)
		if _, ok := seen[pkg]; pkg == "" || ok {
			continue
		}
		seen[pkg] = struct{}{}
		pkgs = append(pkgs, pkg)
	}
	slices.Sort(pkgs)
	return pkgs
}

// FilterByPackage returns a new profile holding copies of the stats for rules
// in package pkg.
func (p *EvalProfile) FilterByPackage(pkg string) *EvalProfile {
	if p == nil {
		return nil
	}
	filtered := newEvalProfile()
	for path, stat := range p.Rules {
		if packageOf(path) == pkg {
			filtered.Rules[path] = stat.clone()
		}
	}
	return filtered
}

// PackageStats returns the stats of the tracked rules aggregated per package,
// or nil if there are none.
func (p *EvalProfile) PackageStats() map[string]*RuleStat {
	if p == nil {
		return nil
	}
	var stats map[string]*RuleStat
	for path, stat := range p.Rules {
		pkg := packageOf(path)
		if pkg == "" || stat == nil {
			continue
		}
		if stats == nil {
			stats = map[string]*RuleStat{}
		}
		agg, ok := stats[pkg]
		if !ok {
			agg = &RuleStat{}
			stats[pkg] = agg
		}
		agg.Evals += stat.Evals
		agg.Successes += stat.Successes
	}
	return stats
}

// Merge returns a new profile summing the counts of p and other. If only one
// of them is non-nil, that profile is returned as is.
func (p *EvalProfile) Merge(other *EvalProfile) *EvalProfile {
	switch {
	case p == nil:
		return other
	case other == nil:
		return p
	}
	merged := newEvalProfile()
	for _, src := range []*EvalProfile{p, other} {
		for path, stat := range src.Rules {
			dst, ok := merged.Rules[path]
			if !ok {
				merged.Rules[path] = stat.clone()
				continue
			}
			if stat != nil {
				dst.Evals += stat.Evals
				dst.Successes += stat.Successes
			}
		}
	}
	return merged
}

// Diff compares p against other. Counts in Changed are other minus p.
func (p *EvalProfile) Diff(other *EvalProfile) *ProfileDiff {
	if p == nil {
		return nil
	}
	var otherRules map[string]*RuleStat
	if other != nil {
		otherRules = other.Rules
	}

	d := &ProfileDiff{}
	for path, stat := range p.Rules {
		otherStat, ok := otherRules[path]
		if !ok {
			if d.Removed == nil {
				d.Removed = map[string]*RuleStat{}
			}
			d.Removed[path] = stat.clone()
			continue
		}
		before, after := stat.clone(), otherStat.clone()
		if *before != *after {
			if d.Changed == nil {
				d.Changed = map[string]*RuleStatDelta{}
			}
			d.Changed[path] = &RuleStatDelta{
				EvalsDelta:     after.Evals - before.Evals,
				SuccessesDelta: after.Successes - before.Successes,
			}
		}
	}
	for path, stat := range otherRules {
		if _, ok := p.Rules[path]; !ok {
			if d.Added == nil {
				d.Added = map[string]*RuleStat{}
			}
			d.Added[path] = stat.clone()
		}
	}
	return d
}

// HasChanges reports whether the diff contains any added, removed or changed
// rules.
func (d *ProfileDiff) HasChanges() bool {
	if d == nil {
		return false
	}
	return len(d.Added) > 0 || len(d.Removed) > 0 || len(d.Changed) > 0
}

// Equal reports whether p and other track the same rules with the same
// counts. Two nil profiles are equal.
func (p *EvalProfile) Equal(other *EvalProfile) bool {
	if p == nil || other == nil {
		return p == other
	}
	if len(p.Rules) != len(other.Rules) {
		return false
	}
	for path, stat := range p.Rules {
		otherStat, ok := other.Rules[path]
		if !ok || !stat.equal(otherStat) {
			return false
		}
	}
	return true
}

// Summary returns a one-line description of the profile's totals.
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
	var sb strings.Builder
	sb.WriteString("Profile:\n")
	for _, path := range p.RulePaths() {
		stat := p.Rules[path].clone()
		fmt.Fprintf(&sb, "  %s: evals=%d successes=%d\n", path, stat.Evals, stat.Successes)
	}
	return sb.String()
}

func (p *EvalProfile) total() *RuleStat {
	total := &RuleStat{}
	if p == nil {
		return total
	}
	for _, stat := range p.Rules {
		if stat != nil {
			total.Evals += stat.Evals
			total.Successes += stat.Successes
		}
	}
	return total
}

func (p *EvalProfile) sortedPaths(include func(*RuleStat) bool) []string {
	if p == nil {
		return nil
	}
	var paths []string
	for path, stat := range p.Rules {
		if include(stat) {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	return paths
}

// packageOf returns the package portion of a rule path, i.e. the path without
// its last segment.
func packageOf(path string) string {
	if ref, err := ast.ParseRef(path); err == nil {
		if len(ref) < 2 {
			return ""
		}
		return ref[:len(ref)-1].String()
	}
	if i := strings.LastIndexByte(path, '.'); i > 0 {
		return path[:i]
	}
	return ""
}
