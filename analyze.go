//go:build analyze

package participle

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/alecthomas/participle/v2/lexer"
)

func init() {
	strictAnalysis = func(root node, syms map[lexer.TokenType]string) error {
		report := analyzeRoot(root, syms)
		if report.IsClean() {
			return nil
		}
		return fmt.Errorf("grammar conflict: %s", report.Summary())
	}
}

// ConflictType classifies a static grammar ambiguity.
type ConflictType int

const (
	// ConflictFirstFirst means two alternatives can start with the same token.
	ConflictFirstFirst ConflictType = iota
	// ConflictFirstFollow means a nullable or repeated group shares tokens with what follows it.
	ConflictFirstFollow
	// ConflictUnreachable means an alternative is fully shadowed by an earlier identical one.
	ConflictUnreachable
)

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

// Severity is how serious a conflict is.
type Severity int

const (
	// SeverityWarning marks ambiguities that can still be parsed by ordered choice.
	SeverityWarning Severity = iota
	// SeverityError marks alternatives that can never be reached.
	SeverityError
)

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

// ConflictLocation identifies the struct (and field, when there is one) that owns a conflict.
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

func (c Conflict) String() string {
	return fmt.Sprintf("[%s] %s at %s: %s", c.Severity, c.Type, c.Location, c.Message)
}

// AnalysisReport is the result of static grammar analysis.
type AnalysisReport struct {
	Conflicts []Conflict
}

// Errors returns a copy of the error-severity conflicts, in original order.
func (r *AnalysisReport) Errors() []Conflict {
	return r.filter(func(c Conflict) bool { return c.Severity == SeverityError })
}

// Warnings returns a copy of the warning-severity conflicts, in original order.
func (r *AnalysisReport) Warnings() []Conflict {
	return r.filter(func(c Conflict) bool { return c.Severity == SeverityWarning })
}

// FilterByType returns a new report containing only conflicts of type t.
func (r *AnalysisReport) FilterByType(t ConflictType) *AnalysisReport {
	return r.FilterWith(func(c Conflict) bool { return c.Type == t })
}

// FilterWith returns a new report containing conflicts for which pred is true.
func (r *AnalysisReport) FilterWith(pred func(Conflict) bool) *AnalysisReport {
	return &AnalysisReport{Conflicts: r.filter(pred)}
}

func (r *AnalysisReport) filter(pred func(Conflict) bool) []Conflict {
	out := []Conflict{}
	if r == nil {
		return out
	}
	for _, c := range r.Conflicts {
		if pred(c) {
			out = append(out, c)
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

// IsClean reports whether the report contains no conflicts.
func (r *AnalysisReport) IsClean() bool {
	return r == nil || len(r.Conflicts) == 0
}

// Summary describes the conflict counts.
func (r *AnalysisReport) Summary() string {
	if r.IsClean() {
		return "no conflicts detected"
	}
	return fmt.Sprintf("%d conflict(s): %d first/first, %d first/follow, %d unreachable",
		len(r.Conflicts),
		r.ConflictCount(ConflictFirstFirst),
		r.ConflictCount(ConflictFirstFollow),
		r.ConflictCount(ConflictUnreachable))
}

// String renders the report on multiple lines.
func (r *AnalysisReport) String() string {
	if r.IsClean() {
		return "no conflicts detected\n"
	}
	var b strings.Builder
	b.WriteString(r.Summary())
	b.WriteByte('\n')
	for _, c := range r.Conflicts {
		b.WriteString(c.String())
		b.WriteByte('\n')
	}
	return b.String()
}

// Merge returns a new report combining r and other, deduplicated.
func (r *AnalysisReport) Merge(other *AnalysisReport) *AnalysisReport {
	var combined []Conflict
	if r != nil {
		combined = append(combined, r.Conflicts...)
	}
	if other != nil {
		combined = append(combined, other.Conflicts...)
	}
	return (&AnalysisReport{Conflicts: combined}).Dedup()
}

// Dedup returns a new report with duplicate conflicts removed.
// Conflicts match when their type, location, and grammar snippet are equal.
func (r *AnalysisReport) Dedup() *AnalysisReport {
	out := []Conflict{}
	if r == nil {
		return &AnalysisReport{Conflicts: out}
	}
	seen := make(map[string]struct{}, len(r.Conflicts))
	for _, c := range r.Conflicts {
		key := c.Type.String() + "\x00" + c.Location.String() + "\x00" + c.GrammarSnippet
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, c)
	}
	return &AnalysisReport{Conflicts: out}
}

// AnalysisOption adjusts AnalyzeWithOptions.
type AnalysisOption func(*analysisConfig)

type analysisConfig struct {
	suppress map[ConflictType]bool
}

// SuppressConflictType drops conflicts of type t from an analysis report.
func SuppressConflictType(t ConflictType) AnalysisOption {
	return func(c *analysisConfig) {
		if c.suppress == nil {
			c.suppress = map[ConflictType]bool{}
		}
		c.suppress[t] = true
	}
}

// Analyze reports ambiguities in the parser grammar.
func (p *Parser[G]) Analyze() (*AnalysisReport, error) {
	return p.AnalyzeWithOptions()
}

// AnalyzeWithOptions reports ambiguities, honoring options such as SuppressConflictType.
func (p *Parser[G]) AnalyzeWithOptions(opts ...AnalysisOption) (*AnalysisReport, error) {
	cfg := analysisConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	report := analyzeRoot(p.typeNodes[p.rootType], lexer.SymbolsByRune(p.lex))
	if len(cfg.suppress) == 0 {
		return report, nil
	}
	return report.FilterWith(func(c Conflict) bool { return !cfg.suppress[c.Type] }), nil
}

type terminal struct {
	kind byte // 'L' literal text, 'R' token type
	lit  string
	typ  lexer.TokenType
	name string
}

func (t terminal) key() string {
	if t.kind == 'L' {
		return "L\x00" + t.lit
	}
	return fmt.Sprintf("R\x00%d", t.typ)
}

func (t terminal) example() string {
	if t.kind == 'L' {
		if t.lit != "" {
			return t.lit
		}
		return "token"
	}
	switch strings.ToLower(t.name) {
	case "ident":
		return "foo"
	case "int":
		return "1"
	case "float":
		return "1.0"
	case "string":
		return `"str"`
	case "char":
		return "a"
	case "rawstring":
		return "raw"
	default:
		if t.name != "" {
			return strings.ToLower(t.name)
		}
		return "foo"
	}
}

type firstResult struct {
	terms   map[string]terminal
	epsilon bool
}

type analyzer struct {
	syms      map[lexer.TokenType]string
	firstMemo map[node]firstResult
	computing map[node]bool
	conflicts []Conflict
}

func analyzeRoot(root node, syms map[lexer.TokenType]string) *AnalysisReport {
	a := &analyzer{
		syms:      syms,
		firstMemo: map[node]firstResult{},
		computing: map[node]bool{},
	}
	a.walk(root, map[string]terminal{}, ConflictLocation{}, map[node]bool{})
	return (&AnalysisReport{Conflicts: a.conflicts}).Dedup()
}

func (a *analyzer) first(n node) firstResult {
	if n == nil {
		return firstResult{epsilon: true, terms: map[string]terminal{}}
	}
	if r, ok := a.firstMemo[n]; ok {
		return r
	}
	if a.computing[n] {
		return firstResult{terms: map[string]terminal{}}
	}
	a.computing[n] = true
	r := a.computeFirst(n)
	delete(a.computing, n)
	if r.terms == nil {
		r.terms = map[string]terminal{}
	}
	a.firstMemo[n] = r
	return r
}

func (a *analyzer) computeFirst(n node) firstResult {
	switch n := n.(type) {
	case *literal:
		t := terminal{kind: 'L', lit: n.s, typ: n.t, name: n.s}
		return firstResult{terms: map[string]terminal{t.key(): t}}
	case *reference:
		name := n.identifier
		if name == "" && a.syms != nil {
			name = a.syms[n.typ]
		}
		t := terminal{kind: 'R', typ: n.typ, name: name}
		return firstResult{terms: map[string]terminal{t.key(): t}}
	case *negation:
		return firstResult{terms: map[string]terminal{}}
	case *lookaheadGroup:
		return firstResult{epsilon: true, terms: map[string]terminal{}}
	case *parseable, *custom:
		return firstResult{terms: map[string]terminal{}}
	case *capture:
		return a.first(n.node)
	case *strct:
		return a.first(n.expr)
	case *union:
		return a.first(&n.disjunction)
	case *group:
		inner := a.first(n.expr)
		terms := cloneTerms(inner.terms)
		eps := inner.epsilon
		if n.mode == groupMatchZeroOrOne || n.mode == groupMatchZeroOrMore {
			eps = true
		}
		return firstResult{terms: terms, epsilon: eps}
	case *sequence:
		terms := map[string]terminal{}
		eps := true
		for s := n; s != nil; s = s.next {
			fr := a.first(s.node)
			if eps {
				terms = unionTerms(terms, fr.terms)
			}
			if !fr.epsilon {
				eps = false
				break
			}
		}
		return firstResult{terms: terms, epsilon: eps}
	case *disjunction:
		terms := map[string]terminal{}
		eps := false
		for _, alt := range n.nodes {
			fr := a.first(alt)
			terms = unionTerms(terms, fr.terms)
			if fr.epsilon {
				eps = true
			}
		}
		return firstResult{terms: terms, epsilon: eps}
	default:
		return firstResult{terms: map[string]terminal{}}
	}
}

func (a *analyzer) walk(n node, follow map[string]terminal, loc ConflictLocation, stack map[node]bool) {
	if n == nil {
		return
	}
	switch n := n.(type) {
	case *lookaheadGroup, *negation, *literal, *reference, *parseable, *custom:
		return
	case *strct:
		if stack[n] {
			return
		}
		stack[n] = true
		loc = ConflictLocation{TypeName: typeLabel(n.typ)}
		a.walk(n.expr, follow, loc, stack)
		delete(stack, n)
	case *union:
		if stack[n] {
			return
		}
		stack[n] = true
		loc = ConflictLocation{TypeName: typeLabel(n.typ)}
		a.walk(&n.disjunction, follow, loc, stack)
		delete(stack, n)
	case *capture:
		loc.FieldName = n.field.Name
		a.walk(n.node, follow, loc, stack)
	case *group:
		if loc.FieldName == "" {
			if f := leadingField(n.expr); f != "" {
				loc.FieldName = f
			}
		}
		if n.mode == groupMatchZeroOrOne || n.mode == groupMatchZeroOrMore || n.mode == groupMatchOneOrMore {
			fr := a.first(n)
			if ov := overlap(fr.terms, follow); len(ov) > 0 {
				a.add(ConflictFirstFollow, SeverityWarning, loc, n, ov[0],
					"optional or repeated group shares first tokens with its follow set",
					"Add a delimiter or lookahead so this group does not share tokens with what follows it.")
			}
		}
		childFollow := follow
		if n.mode == groupMatchZeroOrMore || n.mode == groupMatchOneOrMore {
			childFollow = unionTerms(follow, a.first(n.expr).terms)
		}
		a.walk(n.expr, childFollow, loc, stack)
	case *disjunction:
		a.reportDisjunction(n, loc)
		for _, alt := range n.nodes {
			a.walk(alt, follow, loc, stack)
		}
	case *sequence:
		var nodes []node
		for s := n; s != nil; s = s.next {
			nodes = append(nodes, s.node)
		}
		follows := make([]map[string]terminal, len(nodes))
		foll := follow
		for i := len(nodes) - 1; i >= 0; i-- {
			follows[i] = foll
			fr := a.first(nodes[i])
			if fr.epsilon {
				foll = unionTerms(foll, fr.terms)
			} else {
				foll = cloneTerms(fr.terms)
			}
		}
		for i, child := range nodes {
			a.walk(child, follows[i], loc, stack)
		}
	}
}

func (a *analyzer) reportDisjunction(d *disjunction, loc ConflictLocation) {
	firsts := make([]firstResult, len(d.nodes))
	snips := make([]string, len(d.nodes))
	for i, alt := range d.nodes {
		firsts[i] = a.first(alt)
		snips[i] = grammarSnippet(alt)
	}
	shadowed := map[int]bool{}
	for i := 0; i < len(d.nodes); i++ {
		for j := i + 1; j < len(d.nodes); j++ {
			if rootedNegation(d.nodes[i]) || rootedNegation(d.nodes[j]) {
				continue
			}
			ov := overlap(firsts[i].terms, firsts[j].terms)
			if len(ov) > 0 {
				a.add(ConflictFirstFirst, SeverityWarning, loc, d, ov[0],
					"alternatives share overlapping first tokens",
					"Reorder alternatives or add a distinguishing prefix so their first tokens do not overlap.")
			}
			if !shadowed[j] && sameTerms(firsts[i], firsts[j]) && snips[i] == snips[j] {
				shadowed[j] = true
				ex := ov
				if len(ex) == 0 {
					ex = termList(firsts[j].terms)
				}
				sample := terminal{kind: 'R', name: "ident"}
				if len(ex) > 0 {
					sample = ex[0]
				}
				a.add(ConflictUnreachable, SeverityError, loc, d.nodes[j], sample,
					"alternative is shadowed by an earlier alternative with the same first set",
					"Remove the later duplicate alternative because an earlier alternative already matches the same input.")
			}
		}
	}
}

func (a *analyzer) add(kind ConflictType, sev Severity, loc ConflictLocation, n node, sample terminal, message, suggestion string) {
	if loc.TypeName == "" {
		loc.TypeName = "grammar"
	}
	snip := grammarSnippet(n)
	example := sample.example()
	if example == "" {
		example = "foo"
	}
	if message == "" {
		message = "grammar conflict"
	}
	if suggestion == "" {
		suggestion = "Revise the grammar so this conflict is resolved."
	}
	a.conflicts = append(a.conflicts, Conflict{
		Type:           kind,
		Severity:       sev,
		Message:        message,
		Location:       loc,
		GrammarSnippet: snip,
		Example:        example,
		Suggestion:     suggestion,
	})
}

func grammarSnippet(n node) string {
	if n == nil {
		return "empty"
	}
	s := n.String()
	s = strings.TrimSpace(s)
	if len(s) < 4 {
		s += " ..."
	}
	return s
}

func typeLabel(t reflect.Type) string {
	if t == nil {
		return "grammar"
	}
	if name := t.Name(); name != "" {
		return name
	}
	s := t.String()
	if s == "" {
		return "grammar"
	}
	return s
}

func rootedNegation(n node) bool {
	switch n := n.(type) {
	case *negation:
		return true
	case *capture:
		return rootedNegation(n.node)
	case *group:
		if n.mode == groupMatchOnce || n.mode == groupMatchNonEmpty {
			return rootedNegation(n.expr)
		}
	case *sequence:
		return n.next == nil && rootedNegation(n.node)
	}
	return false
}

func leadingField(n node) string {
	switch n := n.(type) {
	case *capture:
		return n.field.Name
	case *group:
		return leadingField(n.expr)
	case *sequence:
		if f := leadingField(n.node); f != "" {
			return f
		}
		if n.next != nil {
			return leadingField(n.next)
		}
	case *disjunction:
		if len(n.nodes) > 0 {
			return leadingField(n.nodes[0])
		}
	}
	return ""
}

func cloneTerms(in map[string]terminal) map[string]terminal {
	return unionTerms(nil, in)
}

func unionTerms(a, b map[string]terminal) map[string]terminal {
	out := make(map[string]terminal, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func overlap(a, b map[string]terminal) []terminal {
	var keys []string
	for k := range a {
		if _, ok := b[k]; ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]terminal, 0, len(keys))
	for _, k := range keys {
		out = append(out, a[k])
	}
	return out
}

func termList(m map[string]terminal) []terminal {
	return overlap(m, m)
}

func sameTerms(a, b firstResult) bool {
	if a.epsilon != b.epsilon || len(a.terms) != len(b.terms) {
		return false
	}
	for k := range a.terms {
		if _, ok := b.terms[k]; !ok {
			return false
		}
	}
	return true
}
