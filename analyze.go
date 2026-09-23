//go:build analyze

package participle

import (
	"fmt"
	"sort"
	"strings"

	"github.com/alecthomas/participle/v2/lexer"
)

// ConflictType classifies a grammar conflict.
type ConflictType int

const (
	ConflictFirstFirst ConflictType = iota
	ConflictFirstFollow
	ConflictUnreachable
)

func (c ConflictType) String() string {
	switch c {
	case ConflictFirstFirst:
		return "first/first"
	case ConflictFirstFollow:
		return "first/follow"
	case ConflictUnreachable:
		return "unreachable"
	}
	return fmt.Sprintf("ConflictType(%d)", int(c))
}

// Severity of a conflict.
type Severity int

const (
	SeverityWarning Severity = iota
	SeverityError
)

func (s Severity) String() string {
	switch s {
	case SeverityWarning:
		return "warning"
	case SeverityError:
		return "error"
	}
	return fmt.Sprintf("Severity(%d)", int(s))
}

// ConflictLocation identifies where in the grammar a conflict originates.
type ConflictLocation struct {
	TypeName  string
	FieldName string
}

func (l ConflictLocation) String() string {
	if l.FieldName == "" {
		return l.TypeName
	}
	return l.TypeName + "." + l.FieldName
}

// Conflict describes a single grammar ambiguity.
type Conflict struct {
	Type           ConflictType
	Severity       Severity
	Message        string
	Location       ConflictLocation
	GrammarSnippet string
	Example        string
	Suggestion     string
}

func (c Conflict) String() string {
	return fmt.Sprintf("[%s] %s at %s: %s", c.Severity, c.Type, c.Location, c.Message)
}

// AnalysisReport is the result of analysing a grammar.
type AnalysisReport struct {
	Conflicts []Conflict
}

// FilterWith returns a new report containing conflicts for which keep returns true.
func (r *AnalysisReport) FilterWith(keep func(Conflict) bool) *AnalysisReport {
	out := &AnalysisReport{Conflicts: []Conflict{}}
	if r == nil {
		return out
	}
	for _, c := range r.Conflicts {
		if keep(c) {
			out.Conflicts = append(out.Conflicts, c)
		}
	}
	return out
}

// FilterByType returns a new report containing only conflicts of type t.
func (r *AnalysisReport) FilterByType(t ConflictType) *AnalysisReport {
	return r.FilterWith(func(c Conflict) bool { return c.Type == t })
}

// Errors returns conflicts with SeverityError.
func (r *AnalysisReport) Errors() []Conflict {
	return r.FilterWith(func(c Conflict) bool { return c.Severity == SeverityError }).Conflicts
}

// Warnings returns conflicts with SeverityWarning.
func (r *AnalysisReport) Warnings() []Conflict {
	return r.FilterWith(func(c Conflict) bool { return c.Severity == SeverityWarning }).Conflicts
}

// ConflictCount returns the number of conflicts of type t.
func (r *AnalysisReport) ConflictCount(t ConflictType) int {
	return len(r.FilterByType(t).Conflicts)
}

// HasType returns true if any conflict of type t exists.
func (r *AnalysisReport) HasType(t ConflictType) bool { return r.ConflictCount(t) > 0 }

// IsClean returns true if there are no conflicts.
func (r *AnalysisReport) IsClean() bool { return r == nil || len(r.Conflicts) == 0 }

// Summary returns a one-line summary of the report.
func (r *AnalysisReport) Summary() string {
	if r.IsClean() {
		return "no conflicts detected"
	}
	return fmt.Sprintf("%d conflict(s): %d first/first, %d first/follow, %d unreachable",
		len(r.Conflicts), r.ConflictCount(ConflictFirstFirst), r.ConflictCount(ConflictFirstFollow), r.ConflictCount(ConflictUnreachable))
}

func (r *AnalysisReport) String() string {
	w := &strings.Builder{}
	w.WriteString(r.Summary())
	w.WriteString("\n")
	if r == nil {
		return w.String()
	}
	for _, c := range r.Conflicts {
		fmt.Fprintf(w, "  %s\n    grammar: %s\n    example: %s\n    suggestion: %s\n", c, c.GrammarSnippet, c.Example, c.Suggestion)
	}
	return w.String()
}

// Merge returns a new deduplicated report combining r and other.
func (r *AnalysisReport) Merge(other *AnalysisReport) *AnalysisReport {
	out := &AnalysisReport{}
	if r != nil {
		out.Conflicts = append(out.Conflicts, r.Conflicts...)
	}
	if other != nil {
		out.Conflicts = append(out.Conflicts, other.Conflicts...)
	}
	return out.Dedup()
}

// Dedup returns a new report with duplicate conflicts removed, keyed by (Type, Location, GrammarSnippet).
func (r *AnalysisReport) Dedup() *AnalysisReport {
	type key struct {
		t        ConflictType
		loc, snp string
	}
	seen := map[key]bool{}
	return r.FilterWith(func(c Conflict) bool {
		k := key{c.Type, c.Location.String(), c.GrammarSnippet}
		if seen[k] {
			return false
		}
		seen[k] = true
		return true
	})
}

// AnalysisOption configures grammar analysis.
type AnalysisOption func(*analysisOptions)

type analysisOptions struct {
	suppress map[ConflictType]bool
}

// SuppressConflictType excludes conflicts of type t from the report.
func SuppressConflictType(t ConflictType) AnalysisOption {
	return func(o *analysisOptions) { o.suppress[t] = true }
}

// Analyze performs static analysis of the grammar for ambiguities.
func (p *Parser[G]) Analyze() (*AnalysisReport, error) { return p.AnalyzeWithOptions() }

// AnalyzeWithOptions performs static analysis of the grammar with options.
func (p *Parser[G]) AnalyzeWithOptions(opts ...AnalysisOption) (*AnalysisReport, error) {
	o := &analysisOptions{suppress: map[ConflictType]bool{}}
	for _, opt := range opts {
		opt(o)
	}
	root, ok := p.typeNodes[p.rootType]
	if !ok {
		return nil, fmt.Errorf("parser has no root production")
	}
	report := analyzeGrammar(root)
	return report.FilterWith(func(c Conflict) bool { return !o.suppress[c.Type] }), nil
}

func init() {
	strictAnalysisHook = func(root node) error {
		report := analyzeGrammar(root)
		if !report.IsClean() {
			return fmt.Errorf("strict mode: grammar conflict detected: %s", report)
		}
		return nil
	}
}

// terminal is a single element of a FIRST/FOLLOW set.
type terminal struct {
	lit  bool
	s    string
	typ  lexer.TokenType
	name string
	wild bool // opaque (custom, parseable, negation) - never overlaps
}

func (t terminal) key() string {
	switch {
	case t.wild:
		return "*" + t.name
	case t.lit:
		return fmt.Sprintf("L%d:%s", t.typ, t.s)
	}
	return fmt.Sprintf("T%d", t.typ)
}

func (t terminal) display() string {
	if t.lit {
		return fmt.Sprintf("%q", t.s)
	}
	return "<" + strings.ToLower(t.name) + ">"
}

func (t terminal) overlaps(o terminal) bool {
	if t.wild || o.wild || t.lit != o.lit {
		return false
	}
	if t.lit {
		return t.s == o.s && (t.typ == lexer.EOF || o.typ == lexer.EOF || t.typ == o.typ)
	}
	return t.typ == o.typ
}

type termSet map[string]terminal

func (s termSet) add(o termSet) {
	for k, v := range o {
		s[k] = v
	}
}

func (s termSet) overlap(o termSet) []terminal {
	var out []terminal
	for _, a := range s {
		for _, b := range o {
			if a.overlaps(b) {
				out = append(out, a)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key() < out[j].key() })
	return out
}

func (s termSet) equal(o termSet) bool {
	if len(s) != len(o) {
		return false
	}
	for k := range s {
		if _, ok := o[k]; !ok {
			return false
		}
	}
	return true
}

type firstInfo struct {
	set      termSet
	nullable bool
}

type analyzer struct {
	first     map[node]*firstInfo
	inFirst   map[node]bool
	visited   map[node]bool
	conflicts []Conflict
}

func analyzeGrammar(root node) *AnalysisReport {
	a := &analyzer{first: map[node]*firstInfo{}, inFirst: map[node]bool{}, visited: map[node]bool{}}
	a.walk(root, termSet{}, ConflictLocation{TypeName: typeNameOf(root)}, "")
	return (&AnalysisReport{Conflicts: a.conflicts}).Dedup()
}

func typeNameOf(n node) string {
	switch n := n.(type) {
	case *strct:
		return n.typ.Name()
	case *union:
		return n.typ.Name()
	case *custom:
		return n.typ.Name()
	case *parseable:
		return n.t.Name()
	}
	return "Grammar"
}

func (a *analyzer) firstOf(n node) *firstInfo {
	if fi, ok := a.first[n]; ok {
		return fi
	}
	if a.inFirst[n] {
		return &firstInfo{set: termSet{}}
	}
	a.inFirst[n] = true
	defer delete(a.inFirst, n)
	fi := &firstInfo{set: termSet{}}
	switch n := n.(type) {
	case *literal:
		t := terminal{lit: true, s: n.s, typ: n.t, name: n.tt}
		if n.s == "" {
			t = terminal{typ: n.t, name: n.tt}
		}
		fi.set[t.key()] = t
	case *reference:
		t := terminal{typ: n.typ, name: n.identifier}
		fi.set[t.key()] = t
	case *negation, *custom, *parseable:
		t := terminal{wild: true, name: fmt.Sprintf("%p", n)}
		fi.set[t.key()] = t
	case *lookaheadGroup:
		fi.nullable = true
	case *capture:
		c := a.firstOf(n.node)
		fi.set.add(c.set)
		fi.nullable = c.nullable
	case *strct:
		c := a.firstOf(n.expr)
		fi.set.add(c.set)
		fi.nullable = c.nullable
	case *group:
		c := a.firstOf(n.expr)
		fi.set.add(c.set)
		fi.nullable = c.nullable || n.mode == groupMatchZeroOrOne || n.mode == groupMatchZeroOrMore
	case *disjunction:
		for _, alt := range n.nodes {
			c := a.firstOf(alt)
			fi.set.add(c.set)
			fi.nullable = fi.nullable || c.nullable
		}
	case *union:
		for _, alt := range n.disjunction.nodes {
			c := a.firstOf(alt)
			fi.set.add(c.set)
			fi.nullable = fi.nullable || c.nullable
		}
	case *sequence:
		fi.nullable = true
		for s := n; s != nil; s = s.next {
			c := a.firstOf(s.node)
			fi.set.add(c.set)
			if !c.nullable {
				fi.nullable = false
				break
			}
		}
	}
	a.first[n] = fi
	return fi
}

// firstField finds the first captured field name within n without descending into nested structs.
func firstField(n node) string {
	switch n := n.(type) {
	case *capture:
		return n.field.Name
	case *group:
		return firstField(n.expr)
	case *sequence:
		for s := n; s != nil; s = s.next {
			if f := firstField(s.node); f != "" {
				return f
			}
		}
	case *disjunction:
		for _, alt := range n.nodes {
			if f := firstField(alt); f != "" {
				return f
			}
		}
	}
	return ""
}

func snippet(n node) string {
	s := ebnf(n)
	if len(s) < 4 {
		s = "( " + s + " )"
	}
	return s
}

func displayTerms(ts []terminal) string {
	parts := make([]string, len(ts))
	for i, t := range ts {
		parts[i] = t.display()
	}
	return strings.Join(parts, ", ")
}

func (a *analyzer) locate(loc ConflictLocation, field string, n node) ConflictLocation {
	if field == "" {
		field = firstField(n)
	}
	loc.FieldName = field
	return loc
}

// walk visits n with the FOLLOW set of its context.
func (a *analyzer) walk(n node, follow termSet, loc ConflictLocation, field string) {
	switch n := n.(type) {
	case *strct:
		if a.visited[n] {
			return
		}
		a.visited[n] = true
		a.walk(n.expr, follow, ConflictLocation{TypeName: n.typ.Name()}, "")
	case *union:
		if a.visited[n] {
			return
		}
		a.visited[n] = true
		a.checkAlternatives(n.disjunction.nodes, &n.disjunction, ConflictLocation{TypeName: n.typ.Name()}, "")
		for _, alt := range n.disjunction.nodes {
			a.walk(alt, follow, ConflictLocation{TypeName: n.typ.Name()}, "")
		}
	case *capture:
		a.walk(n.node, follow, loc, n.field.Name)
	case *lookaheadGroup, *negation, *literal, *reference, *custom, *parseable:
	case *disjunction:
		a.checkAlternatives(n.nodes, n, loc, field)
		for _, alt := range n.nodes {
			a.walk(alt, follow, loc, field)
		}
	case *sequence:
		for s := n; s != nil; s = s.next {
			f := termSet{}
			nullable := true
			for r := s.next; r != nil; r = r.next {
				c := a.firstOf(r.node)
				f.add(c.set)
				if !c.nullable {
					nullable = false
					break
				}
			}
			if nullable {
				f.add(follow)
			}
			a.walk(s.node, f, loc, field)
		}
	case *group:
		inner := follow
		if n.mode == groupMatchZeroOrMore || n.mode == groupMatchOneOrMore {
			inner = termSet{}
			inner.add(follow)
			inner.add(a.firstOf(n.expr).set)
		}
		if n.mode == groupMatchZeroOrOne || n.mode == groupMatchZeroOrMore || n.mode == groupMatchOneOrMore {
			if ov := a.firstOf(n.expr).set.overlap(follow); len(ov) > 0 {
				a.conflicts = append(a.conflicts, Conflict{
					Type:           ConflictFirstFollow,
					Severity:       SeverityWarning,
					Message:        fmt.Sprintf("optional/repeated group %s can start with %s, which may also follow it", ebnf(n), displayTerms(ov)),
					Location:       a.locate(loc, field, n),
					GrammarSnippet: snippet(n),
					Example:        ov[0].display(),
					Suggestion:     "make the token that follows the group distinct from the group's first token, or add a lookahead group (?= ...) to disambiguate",
				})
			}
		}
		a.walk(n.expr, inner, loc, field)
	}
}

func (a *analyzer) checkAlternatives(alts []node, d node, loc ConflictLocation, field string) {
	for j := 1; j < len(alts); j++ {
		fj := a.firstOf(alts[j]).set
		for i := 0; i < j; i++ {
			fi := a.firstOf(alts[i]).set
			if fi.equal(fj) && len(fi) > 0 && ebnf(alts[i]) == ebnf(alts[j]) {
				example := displayTerms(fj.overlap(fi))
				if example == "" {
					example = ebnf(alts[j])
				}
				a.conflicts = append(a.conflicts, Conflict{
					Type:           ConflictUnreachable,
					Severity:       SeverityError,
					Message:        fmt.Sprintf("alternative %d (%s) is shadowed by identical alternative %d", j+1, ebnf(alts[j]), i+1),
					Location:       a.locate(loc, field, alts[j]),
					GrammarSnippet: snippet(d),
					Example:        example,
					Suggestion:     "remove the duplicate alternative or change it so it can match different input",
				})
			}
			if ov := fi.overlap(fj); len(ov) > 0 {
				a.conflicts = append(a.conflicts, Conflict{
					Type:           ConflictFirstFirst,
					Severity:       SeverityWarning,
					Message:        fmt.Sprintf("alternatives %d (%s) and %d (%s) can both start with %s", i+1, ebnf(alts[i]), j+1, ebnf(alts[j]), displayTerms(ov)),
					Location:       a.locate(loc, field, alts[i]),
					GrammarSnippet: snippet(d),
					Example:        ov[0].display(),
					Suggestion:     "factor out the common prefix of the alternatives, reorder them, or increase lookahead with UseLookahead",
				})
			}
		}
	}
}
