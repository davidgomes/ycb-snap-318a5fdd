package rego

import (
	"fmt"
	"sort"
	"strings"

	"github.com/open-policy-agent/opa/v1/topdown"
)

// RuleStat counts how many times a rule was entered and how many of those
// entries succeeded.
type RuleStat struct {
	Evals     int
	Successes int
}

// SuccessRate returns Successes/Evals. It is 0 when the receiver is nil or
// Evals is 0.
func (s *RuleStat) SuccessRate() float64 {
	if s == nil || s.Evals == 0 {
		return 0
	}
	return float64(s.Successes) / float64(s.Evals)
}

// String returns "evals=N successes=N", or "<nil>" for a nil receiver.
func (s *RuleStat) String() string {
	if s == nil {
		return "<nil>"
	}
	return fmt.Sprintf("evals=%d successes=%d", s.Evals, s.Successes)
}

func (s *RuleStat) clone() *RuleStat {
	if s == nil {
		return nil
	}
	c := *s
	return &c
}

// EvalProfile maps fully qualified rule paths to evaluation counts.
type EvalProfile struct {
	stats map[string]*RuleStat
}

func newEvalProfile() *EvalProfile {
	return &EvalProfile{stats: map[string]*RuleStat{}}
}

type profileRecorder struct {
	p *EvalProfile
}

func (r profileRecorder) EnterRule(path string) {
	if r.p == nil {
		return
	}
	if r.p.stats == nil {
		r.p.stats = map[string]*RuleStat{}
	}
	st := r.p.stats[path]
	if st == nil {
		st = &RuleStat{}
		r.p.stats[path] = st
	}
	st.Evals++
}

func (r profileRecorder) SucceedRule(path string) {
	if r.p == nil || r.p.stats == nil {
		return
	}
	st := r.p.stats[path]
	if st == nil {
		st = &RuleStat{}
		r.p.stats[path] = st
	}
	st.Successes++
}

// Stat returns the stats for rule, or nil when rule is not tracked or the
// receiver is nil.
func (p *EvalProfile) Stat(rule string) *RuleStat {
	if p == nil || p.stats == nil {
		return nil
	}
	return p.stats[rule]
}

// RulePaths returns the sorted tracked rule paths. It is nil when the
// receiver is nil or no rules are tracked.
func (p *EvalProfile) RulePaths() []string {
	if p == nil || len(p.stats) == 0 {
		return nil
	}
	paths := make([]string, 0, len(p.stats))
	for path := range p.stats {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// SuccessRate returns Successes/Evals for rule. It is 0 when the receiver is
// nil, the rule is untracked, or the rule has zero evaluations.
func (p *EvalProfile) SuccessRate(rule string) float64 {
	if p == nil {
		return 0
	}
	return p.Stat(rule).SuccessRate()
}

// OverallSuccessRate returns aggregate Successes/Evals across all rules.
// It is 0 when the receiver is nil or there are no evaluations.
func (p *EvalProfile) OverallSuccessRate() float64 {
	if p == nil {
		return 0
	}
	var evals, successes int
	for _, st := range p.stats {
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

// HotRules returns sorted rule paths with Evals >= minEvals. It is nil when
// the receiver is nil or no rule qualifies.
func (p *EvalProfile) HotRules(minEvals int) []string {
	if p == nil {
		return nil
	}
	var paths []string
	for path, st := range p.stats {
		if st != nil && st.Evals >= minEvals {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)
	return paths
}

// FailedRules returns sorted rule paths with Evals > 0 and Successes == 0.
// It is nil when the receiver is nil or no rule qualifies.
func (p *EvalProfile) FailedRules() []string {
	if p == nil {
		return nil
	}
	var paths []string
	for path, st := range p.stats {
		if st != nil && st.Evals > 0 && st.Successes == 0 {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)
	return paths
}

// SucceededRules returns sorted rule paths with Successes > 0.
// It is nil when the receiver is nil or no rule qualifies.
func (p *EvalProfile) SucceededRules() []string {
	if p == nil {
		return nil
	}
	var paths []string
	for path, st := range p.stats {
		if st != nil && st.Successes > 0 {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)
	return paths
}

// Packages returns sorted unique package names derived from rule paths.
// "data.authz.allow" yields "data.authz". It is nil when the receiver is nil
// or no rules are tracked.
func (p *EvalProfile) Packages() []string {
	if p == nil || len(p.stats) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	for path := range p.stats {
		seen[packageOfRule(path)] = struct{}{}
	}
	pkgs := make([]string, 0, len(seen))
	for pkg := range seen {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)
	return pkgs
}

// FilterByPackage returns a new profile containing deep-copied stats for rules
// in pkg. It is nil when the receiver is nil.
func (p *EvalProfile) FilterByPackage(pkg string) *EvalProfile {
	if p == nil {
		return nil
	}
	out := newEvalProfile()
	for path, st := range p.stats {
		if packageOfRule(path) == pkg {
			out.stats[path] = st.clone()
		}
	}
	return out
}

// Merge combines profiles by summing counts. It returns nil when both
// profiles are nil, and the non-nil profile when the other is nil.
func (p *EvalProfile) Merge(other *EvalProfile) *EvalProfile {
	if p == nil && other == nil {
		return nil
	}
	if p == nil {
		return other
	}
	if other == nil {
		return p
	}
	out := newEvalProfile()
	for path, st := range p.stats {
		if st == nil {
			continue
		}
		out.stats[path] = st.clone()
	}
	for path, st := range other.stats {
		if st == nil {
			continue
		}
		cur := out.stats[path]
		if cur == nil {
			out.stats[path] = st.clone()
			continue
		}
		cur.Evals += st.Evals
		cur.Successes += st.Successes
	}
	return out
}

// PackageStats returns aggregated stats per package. It is nil when the
// receiver is nil.
func (p *EvalProfile) PackageStats() map[string]*RuleStat {
	if p == nil {
		return nil
	}
	out := map[string]*RuleStat{}
	for path, st := range p.stats {
		if st == nil {
			continue
		}
		pkg := packageOfRule(path)
		agg := out[pkg]
		if agg == nil {
			agg = &RuleStat{}
			out[pkg] = agg
		}
		agg.Evals += st.Evals
		agg.Successes += st.Successes
	}
	return out
}

// ContainsRule reports whether path is tracked. It is false when the receiver
// is nil.
func (p *EvalProfile) ContainsRule(path string) bool {
	if p == nil || p.stats == nil {
		return false
	}
	_, ok := p.stats[path]
	return ok
}

// Summary returns "profile: N rules, N evals, N successes", or
// "profile: disabled" when the receiver is nil.
func (p *EvalProfile) Summary() string {
	if p == nil {
		return "profile: disabled"
	}
	var evals, successes int
	for _, st := range p.stats {
		if st == nil {
			continue
		}
		evals += st.Evals
		successes += st.Successes
	}
	return fmt.Sprintf("profile: %d rules, %d evals, %d successes", len(p.stats), evals, successes)
}

// Equal reports structural equality. Two nil profiles are equal. A nil
// receiver is equal only when other is also nil.
func (p *EvalProfile) Equal(other *EvalProfile) bool {
	if p == nil || other == nil {
		return p == nil && other == nil
	}
	if len(p.stats) != len(other.stats) {
		return false
	}
	for path, st := range p.stats {
		ot := other.stats[path]
		if !ruleStatEqual(st, ot) {
			return false
		}
	}
	return true
}

func ruleStatEqual(a, b *RuleStat) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Evals == b.Evals && a.Successes == b.Successes
}

// String returns a header and one sorted line per rule. A nil receiver
// returns "<nil>".
func (p *EvalProfile) String() string {
	if p == nil {
		return "<nil>"
	}
	var b strings.Builder
	b.WriteString("Profile:\n")
	for _, path := range p.RulePaths() {
		fmt.Fprintf(&b, "  %s: %s\n", path, p.stats[path].String())
	}
	return b.String()
}

// RuleStatDelta is the difference other-minus-receiver for one rule.
type RuleStatDelta struct {
	EvalsDelta     int
	SuccessesDelta int
}

// ProfileDiff is the result of comparing two evaluation profiles.
type ProfileDiff struct {
	Added   map[string]*RuleStat
	Removed map[string]*RuleStat
	Changed map[string]*RuleStatDelta
}

// HasChanges reports whether any field is populated. It is false when the
// receiver is nil.
func (d *ProfileDiff) HasChanges() bool {
	if d == nil {
		return false
	}
	return len(d.Added) > 0 || len(d.Removed) > 0 || len(d.Changed) > 0
}

// Diff compares p with other. Rules only in other are Added, rules only in p
// are Removed, and shared rules with different counts are Changed
// (other minus receiver). Nil maps are used when a field is empty. A nil
// receiver returns nil.
func (p *EvalProfile) Diff(other *EvalProfile) *ProfileDiff {
	if p == nil {
		return nil
	}
	d := &ProfileDiff{}
	otherStats := map[string]*RuleStat(nil)
	if other != nil {
		otherStats = other.stats
	}
	for path, st := range otherStats {
		if st == nil {
			continue
		}
		if p.stats[path] == nil {
			if d.Added == nil {
				d.Added = map[string]*RuleStat{}
			}
			d.Added[path] = st.clone()
		}
	}
	for path, st := range p.stats {
		if st == nil {
			continue
		}
		ot := otherStats[path]
		if ot == nil {
			if d.Removed == nil {
				d.Removed = map[string]*RuleStat{}
			}
			d.Removed[path] = st.clone()
			continue
		}
		if st.Evals != ot.Evals || st.Successes != ot.Successes {
			if d.Changed == nil {
				d.Changed = map[string]*RuleStatDelta{}
			}
			d.Changed[path] = &RuleStatDelta{
				EvalsDelta:     ot.Evals - st.Evals,
				SuccessesDelta: ot.Successes - st.Successes,
			}
		}
	}
	return d
}

func packageOfRule(path string) string {
	i := strings.LastIndex(path, ".")
	if i <= 0 {
		return path
	}
	return path[:i]
}

// EvalRuleProfile enables per-evaluation rule profiling.
func EvalRuleProfile(yes bool) EvalOption {
	return func(e *EvalContext) {
		e.ruleProfile = yes
	}
}

// EnableRuleProfile enables rule profiling for evaluations of this Rego object.
func EnableRuleProfile(yes bool) func(r *Rego) {
	return func(r *Rego) {
		r.ruleProfile = yes
	}
}

func applyRuleProfile(q *topdown.Query, ectx *EvalContext) (*topdown.Query, *EvalProfile) {
	return applyRuleProfileImpl(q, ectx)
}
