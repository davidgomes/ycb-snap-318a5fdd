//go:build analyze

package participle

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/alecthomas/participle/v2/lexer"
)

// ConflictType classifies a static grammar ambiguity.
type ConflictType int

const (
	// ConflictFirstFirst means two alternatives of a disjunction share a first token.
	ConflictFirstFirst ConflictType = iota
	// ConflictFirstFollow means a quantified group's first tokens overlap what can follow it.
	ConflictFirstFollow
	// ConflictUnreachable means a later alternative is shadowed by an earlier identical one.
	ConflictUnreachable
)

// String returns "first/first", "first/follow", or "unreachable".
func (t ConflictType) String() string {
	switch t {
	case ConflictFirstFirst:
		return "first/first"
	case ConflictFirstFollow:
		return "first/follow"
	case ConflictUnreachable:
		return "unreachable"
	default:
		return "unknown"
	}
}

// Severity classifies how serious a conflict is.
type Severity int

const (
	// SeverityWarning is reported for first/first and first/follow conflicts.
	SeverityWarning Severity = iota
	// SeverityError is reported for unreachable alternatives.
	SeverityError
)

// String returns "warning" or "error".
func (s Severity) String() string {
	switch s {
	case SeverityWarning:
		return "warning"
	case SeverityError:
		return "error"
	default:
		return "unknown"
	}
}

// ConflictLocation identifies the Go type, and optionally field, that contains a conflict.
type ConflictLocation struct {
	// TypeName is the struct type where the conflict originates.
	// For nested grammars this is the innermost struct.
	TypeName string
	// FieldName is the field that contains the conflict, when there is one.
	FieldName string
}

// String returns "TypeName" or "TypeName.FieldName".
func (l ConflictLocation) String() string {
	if l.FieldName == "" {
		return l.TypeName
	}
	return l.TypeName + "." + l.FieldName
}

// Conflict is one ambiguity found in a grammar.
type Conflict struct {
	Type           ConflictType
	Severity       Severity
	Message        string
	Location       ConflictLocation
	GrammarSnippet string
	Example        string
	Suggestion     string
}

// String returns "[severity] type at location: message".
func (c Conflict) String() string {
	return fmt.Sprintf("[%s] %s at %s: %s", c.Severity, c.Type, c.Location, c.Message)
}

// AnalysisReport is the set of conflicts found in a grammar.
type AnalysisReport struct {
	Conflicts []Conflict
}

// Errors returns conflicts with error severity. The result is a new slice.
func (r *AnalysisReport) Errors() []Conflict {
	return r.collect(func(c Conflict) bool { return c.Severity == SeverityError })
}

// Warnings returns conflicts with warning severity. The result is a new slice.
func (r *AnalysisReport) Warnings() []Conflict {
	return r.collect(func(c Conflict) bool { return c.Severity == SeverityWarning })
}

func (r *AnalysisReport) collect(keep func(Conflict) bool) []Conflict {
	if r == nil {
		return nil
	}
	out := make([]Conflict, 0, len(r.Conflicts))
	for _, c := range r.Conflicts {
		if keep(c) {
			out = append(out, c)
		}
	}
	return out
}

// FilterByType returns a new report containing conflicts of type t, in original order.
func (r *AnalysisReport) FilterByType(t ConflictType) *AnalysisReport {
	return r.FilterWith(func(c Conflict) bool { return c.Type == t })
}

// FilterWith returns a new report containing conflicts for which keep is true, in original order.
func (r *AnalysisReport) FilterWith(keep func(Conflict) bool) *AnalysisReport {
	out := &AnalysisReport{}
	if r == nil || keep == nil {
		return out
	}
	for _, c := range r.Conflicts {
		if keep(c) {
			out.Conflicts = append(out.Conflicts, c)
		}
	}
	return out
}

// ConflictCount returns how many conflicts have the given type.
func (r *AnalysisReport) ConflictCount(t ConflictType) int {
	if r == nil {
		return 0
	}
	n := 0
	for _, c := range r.Conflicts {
		if c.Type == t {
			n++
		}
	}
	return n
}

// HasType reports whether the report contains at least one conflict of type t.
func (r *AnalysisReport) HasType(t ConflictType) bool {
	return r.ConflictCount(t) > 0
}

// IsClean reports whether no conflicts were found.
func (r *AnalysisReport) IsClean() bool {
	return r == nil || len(r.Conflicts) == 0
}

// Summary describes the report in one line.
// A clean report is "no conflicts detected". Otherwise the line always includes
// first/first, first/follow, and unreachable counts.
func (r *AnalysisReport) Summary() string {
	if r.IsClean() {
		return "no conflicts detected"
	}
	return fmt.Sprintf("%d conflict(s): %d first/first, %d first/follow, %d unreachable",
		len(r.Conflicts),
		r.ConflictCount(ConflictFirstFirst),
		r.ConflictCount(ConflictFirstFollow),
		r.ConflictCount(ConflictUnreachable),
	)
}

// String returns a multi-line description of the report, including each conflict's type and location.
func (r *AnalysisReport) String() string {
	var b strings.Builder
	b.WriteString(r.Summary())
	b.WriteByte('\n')
	if r == nil {
		return b.String()
	}
	for _, c := range r.Conflicts {
		fmt.Fprintf(&b, "%s at %s\n", c.Type, c.Location)
		b.WriteString(c.String())
		b.WriteByte('\n')
	}
	return b.String()
}

// Merge returns a new report combining r and other.
// Conflicts are deduplicated by type, location, and grammar snippet, keeping the first occurrence.
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

// Dedup returns a new report with duplicate conflicts removed.
// Identity is (Type, Location.String(), GrammarSnippet). Order of first occurrences is preserved.
func (r *AnalysisReport) Dedup() *AnalysisReport {
	out := &AnalysisReport{}
	if r == nil {
		return out
	}
	seen := make(map[dedupKey]struct{}, len(r.Conflicts))
	for _, c := range r.Conflicts {
		k := dedupKey{typ: c.Type, loc: c.Location.String(), snip: c.GrammarSnippet}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out.Conflicts = append(out.Conflicts, c)
	}
	return out
}

type dedupKey struct {
	typ  ConflictType
	loc  string
	snip string
}

// AnalysisOption adjusts AnalyzeWithOptions.
type AnalysisOption func(*analysisOptions)

type analysisOptions struct {
	suppress map[ConflictType]bool
}

// SuppressConflictType drops conflicts of type t from an analysis report.
// It does not affect StrictMode.
func SuppressConflictType(t ConflictType) AnalysisOption {
	return func(o *analysisOptions) {
		if o.suppress == nil {
			o.suppress = map[ConflictType]bool{}
		}
		o.suppress[t] = true
	}
}

// Analyze reports static ambiguities in the grammar.
func (p *Parser[G]) Analyze() (*AnalysisReport, error) {
	return p.AnalyzeWithOptions()
}

// AnalyzeWithOptions reports static ambiguities, honoring options such as SuppressConflictType.
func (p *Parser[G]) AnalyzeWithOptions(opts ...AnalysisOption) (*AnalysisReport, error) {
	if p == nil {
		return nil, fmt.Errorf("analyze: nil parser")
	}
	root := p.typeNodes[p.rootType]
	if root == nil {
		return nil, fmt.Errorf("analyze: grammar has no root")
	}
	cfg := &analysisOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	report := analyzeGrammar(root)
	if len(cfg.suppress) == 0 {
		return report, nil
	}
	return report.FilterWith(func(c Conflict) bool { return !cfg.suppress[c.Type] }), nil
}

func init() {
	strictAnalyze = func(root node) error {
		report := analyzeGrammar(root)
		if report.IsClean() {
			return nil
		}
		return fmt.Errorf("grammar conflict: %s", report.Summary())
	}
}

type termKind int

const (
	kindLit termKind = iota
	kindTok
	kindOpaque
)

// term is one concrete first-set element.
// Literals and token types never overlap with each other.
type term struct {
	kind   termKind
	lit    string
	tok    lexer.TokenType
	opaque string
	name   string
}

func (t term) equal(o term) bool {
	return t.kind == o.kind && t.lit == o.lit && t.tok == o.tok && t.opaque == o.opaque
}

func (t term) overlaps(o term) bool {
	if t.kind != o.kind {
		return false
	}
	switch t.kind {
	case kindLit:
		if t.lit != o.lit {
			return false
		}
		return t.tok == lexer.EOF || o.tok == lexer.EOF || t.tok == o.tok
	case kindTok:
		return t.tok == o.tok
	case kindOpaque:
		return t.opaque != "" && t.opaque == o.opaque
	default:
		return false
	}
}

func (t term) example() string {
	switch t.kind {
	case kindLit:
		if t.lit != "" {
			return t.lit
		}
		return "empty"
	case kindTok:
		if t.name != "" {
			return t.name
		}
		return "token"
	default:
		if t.name != "" {
			return t.name
		}
		if t.opaque != "" {
			return t.opaque
		}
		return "value"
	}
}

type nodeFacts struct {
	first    []term
	nullable bool
}

type loc struct {
	typeName  string
	fieldName string
}

func (l loc) conflictLocation() ConflictLocation {
	return ConflictLocation{TypeName: l.typeName, FieldName: l.fieldName}
}

type grammarAnalyzer struct {
	facts  map[node]*nodeFacts
	follow map[node][]term
	seen   map[node]bool
	out    []Conflict
}

func analyzeGrammar(root node) *AnalysisReport {
	a := &grammarAnalyzer{
		facts:  map[node]*nodeFacts{},
		follow: map[node][]term{},
		seen:   map[node]bool{},
	}
	var nodes []node
	walkNodes(root, map[node]struct{}{}, func(n node) {
		nodes = append(nodes, n)
		a.facts[n] = &nodeFacts{}
	})
	if len(nodes) == 0 {
		return &AnalysisReport{}
	}
	for iter := 0; iter < len(nodes)+2; iter++ {
		changed := false
		for _, n := range nodes {
			first, nullable := a.evalFirst(n)
			cur := a.facts[n]
			if nullable != cur.nullable || !sameTerms(first, cur.first) {
				cur.first = first
				cur.nullable = nullable
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	a.propagateFollow(root)
	a.check(root, loc{})
	return &AnalysisReport{Conflicts: a.out}
}

func walkNodes(n node, seen map[node]struct{}, fn func(node)) {
	if n == nil {
		return
	}
	if _, ok := seen[n]; ok {
		return
	}
	seen[n] = struct{}{}
	fn(n)
	for _, c := range nodeChildren(n) {
		walkNodes(c, seen, fn)
	}
}

func nodeChildren(n node) []node {
	switch n := n.(type) {
	case *disjunction:
		return append([]node(nil), n.nodes...)
	case *sequence:
		out := []node{}
		if n.node != nil {
			out = append(out, n.node)
		}
		if n.next != nil {
			out = append(out, n.next)
		}
		return out
	case *strct:
		if n.expr == nil {
			return nil
		}
		return []node{n.expr}
	case *capture:
		if n.node == nil {
			return nil
		}
		return []node{n.node}
	case *group:
		if n.expr == nil {
			return nil
		}
		return []node{n.expr}
	case *lookaheadGroup:
		if n.expr == nil {
			return nil
		}
		return []node{n.expr}
	case *negation:
		if n.node == nil {
			return nil
		}
		return []node{n.node}
	case *union:
		return []node{&n.disjunction}
	default:
		return nil
	}
}

func (a *grammarAnalyzer) factsOf(n node) *nodeFacts {
	if n == nil {
		return &nodeFacts{}
	}
	if f := a.facts[n]; f != nil {
		return f
	}
	return &nodeFacts{}
}

func (a *grammarAnalyzer) evalFirst(n node) ([]term, bool) {
	switch n := n.(type) {
	case *literal:
		return []term{{kind: kindLit, lit: n.s, tok: n.t, name: n.s}}, false
	case *reference:
		return []term{{kind: kindTok, tok: n.typ, name: n.identifier}}, false
	case *negation:
		return nil, false
	case *lookaheadGroup:
		return nil, true
	case *parseable:
		name := n.t.Name()
		if name == "" {
			name = n.t.String()
		}
		return []term{{kind: kindOpaque, name: name, opaque: "parseable:" + n.t.String()}}, false
	case *custom:
		name := n.typ.Name()
		if name == "" {
			name = n.typ.String()
		}
		return []term{{kind: kindOpaque, name: name, opaque: "custom:" + n.typ.String()}}, false
	case *capture:
		f := a.factsOf(n.node)
		return cloneTerms(f.first), f.nullable
	case *strct:
		f := a.factsOf(n.expr)
		return cloneTerms(f.first), f.nullable
	case *union:
		f := a.factsOf(&n.disjunction)
		return cloneTerms(f.first), f.nullable
	case *group:
		f := a.factsOf(n.expr)
		switch n.mode {
		case groupMatchZeroOrOne, groupMatchZeroOrMore:
			return cloneTerms(f.first), true
		case groupMatchNonEmpty:
			return cloneTerms(f.first), false
		default:
			return cloneTerms(f.first), f.nullable
		}
	case *disjunction:
		var ts []term
		nullable := false
		for _, c := range n.nodes {
			f := a.factsOf(c)
			ts = unionTerms(ts, f.first)
			if f.nullable {
				nullable = true
			}
		}
		return ts, nullable
	case *sequence:
		var ts []term
		for s := n; s != nil; s = s.next {
			f := a.factsOf(s.node)
			ts = unionTerms(ts, f.first)
			if !f.nullable {
				return ts, false
			}
		}
		return ts, true
	default:
		return nil, false
	}
}

func cloneTerms(ts []term) []term {
	if len(ts) == 0 {
		return nil
	}
	out := make([]term, len(ts))
	copy(out, ts)
	return out
}

func unionTerms(a, b []term) []term {
	out := cloneTerms(a)
	for _, t := range b {
		if !containsTerm(out, t) {
			out = append(out, t)
		}
	}
	return out
}

func containsTerm(ts []term, t term) bool {
	for _, e := range ts {
		if e.equal(t) {
			return true
		}
	}
	return false
}

func sameTerms(a, b []term) bool {
	if len(a) != len(b) {
		return false
	}
	for _, t := range a {
		if !containsTerm(b, t) {
			return false
		}
	}
	return true
}

func intersectTerms(a, b []term) []term {
	var out []term
	for _, x := range a {
		for _, y := range b {
			if x.overlaps(y) && !containsOverlap(out, x) {
				out = append(out, x)
				break
			}
		}
	}
	return out
}

func containsOverlap(ts []term, t term) bool {
	for _, e := range ts {
		if e.overlaps(t) && e.kind == t.kind {
			return true
		}
	}
	return false
}

type followSeed struct {
	n node
	f []term
}

func (a *grammarAnalyzer) propagateFollow(root node) {
	type item struct {
		n node
		f []term
	}
	queue := []item{{n: root}}
	steps := 0
	limit := (len(a.facts) + 2) * (len(a.facts) + 2) * (len(a.facts) + 2)
	if limit < 64 {
		limit = 64
	}
	for len(queue) > 0 && steps < limit {
		steps++
		cur := queue[0]
		queue = queue[1:]
		prev, ok := a.follow[cur.n]
		merged := unionTerms(prev, cur.f)
		grew := !ok || !sameTerms(prev, merged)
		if ok && !grew {
			continue
		}
		a.follow[cur.n] = merged
		for _, seed := range a.seeds(cur.n, merged) {
			if seed.n == nil {
				continue
			}
			queue = append(queue, item{n: seed.n, f: seed.f})
		}
	}
}

func (a *grammarAnalyzer) seeds(n node, follow []term) []followSeed {
	switch n := n.(type) {
	case *lookaheadGroup, *negation, *literal, *reference, *custom, *parseable:
		return nil
	case *capture:
		return []followSeed{{n: n.node, f: follow}}
	case *strct:
		return []followSeed{{n: n.expr, f: follow}}
	case *union:
		return []followSeed{{n: &n.disjunction, f: follow}}
	case *disjunction:
		out := make([]followSeed, 0, len(n.nodes))
		for _, c := range n.nodes {
			out = append(out, followSeed{n: c, f: follow})
		}
		return out
	case *group:
		childFollow := follow
		if n.mode == groupMatchZeroOrMore || n.mode == groupMatchOneOrMore {
			childFollow = unionTerms(follow, a.factsOf(n.expr).first)
		}
		return []followSeed{{n: n.expr, f: childFollow}}
	case *sequence:
		var out []followSeed
		nodeFollow := follow
		if n.next != nil {
			tail := a.factsOf(n.next)
			nodeFollow = cloneTerms(tail.first)
			if tail.nullable {
				nodeFollow = unionTerms(nodeFollow, follow)
			}
			out = append(out, followSeed{n: n.next, f: follow})
		}
		if n.node != nil {
			out = append(out, followSeed{n: n.node, f: nodeFollow})
		}
		return out
	default:
		return nil
	}
}

func nodeIsNil(n node) bool {
	if n == nil {
		return true
	}
	v := reflect.ValueOf(n)
	return v.Kind() == reflect.Ptr && v.IsNil()
}

func (a *grammarAnalyzer) check(n node, l loc) {
	if nodeIsNil(n) || a.seen[n] {
		return
	}
	a.seen[n] = true
	switch n := n.(type) {
	case *lookaheadGroup, *negation:
		return
	case *strct:
		name := n.typ.Name()
		if name == "" {
			name = n.typ.String()
		}
		a.check(n.expr, loc{typeName: name})
	case *union:
		name := n.typ.Name()
		if name == "" {
			name = n.typ.String()
		}
		a.check(&n.disjunction, loc{typeName: name})
	case *capture:
		l.fieldName = n.field.Name
		a.check(n.node, l)
	case *disjunction:
		if l.fieldName == "" {
			if f := commonCaptureField(n); f != "" {
				l.fieldName = f
			}
		}
		a.reportFirstFirst(n, l)
		a.reportUnreachable(n, l)
		for _, c := range n.nodes {
			a.check(c, l)
		}
	case *group:
		if l.fieldName == "" {
			if f := nearestField(n.expr); f != "" {
				l.fieldName = f
			}
		}
		if n.mode == groupMatchZeroOrOne || n.mode == groupMatchZeroOrMore || n.mode == groupMatchOneOrMore {
			a.reportFirstFollow(n, l)
		}
		a.check(n.expr, l)
	case *sequence:
		a.check(n.node, l)
		a.check(n.next, l)
	default:
		for _, c := range nodeChildren(n) {
			a.check(c, l)
		}
	}
}

func commonCaptureField(d *disjunction) string {
	field := ""
	for _, n := range d.nodes {
		fields := map[string]struct{}{}
		collectCaptureFields(n, fields)
		if len(fields) != 1 {
			return ""
		}
		var only string
		for f := range fields {
			only = f
		}
		if field == "" {
			field = only
			continue
		}
		if field != only {
			return ""
		}
	}
	return field
}

func collectCaptureFields(n node, acc map[string]struct{}) {
	switch n := n.(type) {
	case nil, *strct, *union, *lookaheadGroup, *negation:
		return
	case *capture:
		acc[n.field.Name] = struct{}{}
		collectCaptureFields(n.node, acc)
	case *disjunction:
		for _, c := range n.nodes {
			collectCaptureFields(c, acc)
		}
	case *sequence:
		for s := n; s != nil; s = s.next {
			collectCaptureFields(s.node, acc)
		}
	case *group:
		collectCaptureFields(n.expr, acc)
	default:
		for _, c := range nodeChildren(n) {
			collectCaptureFields(c, acc)
		}
	}
}

func nearestField(n node) string {
	switch n := n.(type) {
	case *capture:
		return n.field.Name
	case *group:
		return nearestField(n.expr)
	case *sequence:
		if n == nil {
			return ""
		}
		if f := nearestField(n.node); f != "" {
			return f
		}
		return nearestField(n.next)
	default:
		return ""
	}
}

func (a *grammarAnalyzer) reportFirstFirst(d *disjunction, l loc) {
	var overlap []term
	for i := 0; i < len(d.nodes); i++ {
		if isSuppressedAlt(d.nodes[i]) {
			continue
		}
		fi := a.factsOf(d.nodes[i]).first
		for j := i + 1; j < len(d.nodes); j++ {
			if isSuppressedAlt(d.nodes[j]) {
				continue
			}
			overlap = unionOverlap(overlap, intersectTerms(fi, a.factsOf(d.nodes[j]).first))
		}
	}
	if len(overlap) == 0 {
		return
	}
	snippet := grammarSnippet(d)
	a.add(Conflict{
		Type:           ConflictFirstFirst,
		Severity:       SeverityWarning,
		Message:        fmt.Sprintf("alternatives share first token %q", overlap[0].example()),
		Location:       l.conflictLocation(),
		GrammarSnippet: snippet,
		Example:        exampleSequence(overlap),
		Suggestion:     "Reorder alternatives or add a distinguishing token so their first sets do not overlap",
	})
}

func (a *grammarAnalyzer) reportUnreachable(d *disjunction, l loc) {
	for i := 0; i < len(d.nodes); i++ {
		if isSuppressedAlt(d.nodes[i]) {
			continue
		}
		earlier := a.factsOf(d.nodes[i])
		earlierEBNF := ebnf(d.nodes[i])
		for j := i + 1; j < len(d.nodes); j++ {
			if isSuppressedAlt(d.nodes[j]) {
				continue
			}
			later := a.factsOf(d.nodes[j])
			laterEBNF := ebnf(d.nodes[j])
			if earlier.nullable != later.nullable || !sameTerms(earlier.first, later.first) || earlierEBNF != laterEBNF {
				continue
			}
			ex := "empty"
			if len(later.first) > 0 {
				ex = exampleSequence(later.first)
			}
			a.add(Conflict{
				Type:           ConflictUnreachable,
				Severity:       SeverityError,
				Message:        "alternative is unreachable because an earlier alternative has the same form and first set",
				Location:       l.conflictLocation(),
				GrammarSnippet: grammarSnippet(d.nodes[j]),
				Example:        ex,
				Suggestion:     "Remove the duplicate alternative or change the tokens it matches",
			})
			break
		}
	}
}

func (a *grammarAnalyzer) reportFirstFollow(g *group, l loc) {
	first := a.factsOf(g.expr).first
	follow := a.follow[g]
	overlap := intersectTerms(first, follow)
	if len(overlap) == 0 {
		return
	}
	snippet := grammarSnippet(g)
	a.add(Conflict{
		Type:           ConflictFirstFollow,
		Severity:       SeverityWarning,
		Message:        fmt.Sprintf("quantified expression shares first token %q with the tokens that can follow it", overlap[0].example()),
		Location:       l.conflictLocation(),
		GrammarSnippet: snippet,
		Example:        exampleSequence(overlap),
		Suggestion:     "Add a delimiter or narrow the quantifier so the group does not share tokens with what follows",
	})
}

func (a *grammarAnalyzer) add(c Conflict) {
	if c.Location.TypeName == "" {
		c.Location.TypeName = "grammar"
	}
	if c.Message == "" {
		c.Message = "ambiguous grammar fragment"
	}
	if c.Suggestion == "" {
		c.Suggestion = "Rewrite the grammar so this construct is unambiguous"
	}
	if c.Example == "" {
		c.Example = "token"
	}
	if len(c.GrammarSnippet) < 4 {
		c.GrammarSnippet = grammarSnippetText(c.GrammarSnippet)
	}
	a.out = append(a.out, c)
}

func isSuppressedAlt(n node) bool {
	_, ok := n.(*negation)
	return ok
}

func exampleSequence(ts []term) string {
	if len(ts) == 0 {
		return "empty"
	}
	parts := make([]string, 0, len(ts))
	for _, t := range ts {
		parts = append(parts, t.example())
	}
	return strings.Join(parts, " ")
}

func unionOverlap(dst, src []term) []term {
	for _, t := range src {
		if !containsOverlap(dst, t) {
			dst = append(dst, t)
		}
	}
	return dst
}

func grammarSnippet(n node) string {
	if n == nil {
		return "expr"
	}
	return grammarSnippetText(ebnf(n))
}

func grammarSnippetText(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 4 {
		return s
	}
	if s == "" {
		s = "expr"
	}
	s = "(" + s + ")"
	if len(s) >= 4 {
		return s
	}
	return s + " ..."
}
