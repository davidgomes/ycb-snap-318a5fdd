//go:build analyze

package participle

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/alecthomas/participle/v2/lexer"
)

func init() {
	strictAnalysisHook = func(root node) error {
		report := analyzeGrammar(root)
		if report.IsClean() {
			return nil
		}
		return fmt.Errorf("strict mode: grammar has %s\n%s", report.Summary(), report)
	}
}

// An AnalysisOption modifies the behaviour of Parser.AnalyzeWithOptions.
type AnalysisOption func(o *analysisOptions)

type analysisOptions struct {
	suppressed map[ConflictType]bool
}

// SuppressConflictType excludes conflicts of type t from the analysis report.
func SuppressConflictType(t ConflictType) AnalysisOption {
	return func(o *analysisOptions) {
		o.suppressed[t] = true
	}
}

// Analyze statically analyses the grammar for ambiguities.
//
// Note that participle resolves ambiguity by taking the first matching
// alternative, so conflicts are not necessarily bugs, but they frequently
// indicate that the grammar will not parse what was intended.
func (p *Parser[G]) Analyze() (*AnalysisReport, error) {
	return p.AnalyzeWithOptions()
}

// AnalyzeWithOptions is like Analyze but accepts options controlling the report.
func (p *Parser[G]) AnalyzeWithOptions(opts ...AnalysisOption) (*AnalysisReport, error) {
	root := p.typeNodes[p.rootType]
	if root == nil {
		return nil, fmt.Errorf("parser has no grammar for %s", p.rootType)
	}
	o := &analysisOptions{suppressed: map[ConflictType]bool{}}
	for _, opt := range opts {
		opt(o)
	}
	return analyzeGrammar(root).FilterWith(func(c Conflict) bool { return !o.suppressed[c.Type] }), nil
}

func analyzeGrammar(root node) *AnalysisReport {
	a := &analyzer{
		first:     map[node]*firstInfo{},
		follow:    map[node]*termSet{},
		negations: map[*negation]int{},
		report:    &AnalysisReport{Conflicts: []Conflict{}},
	}
	a.collectTypes(root)
	a.computeFirstSets()
	if a.follow[root] != nil {
		a.follow[root].add(terminal{kind: termEOF})
	}
	a.computeFollowSets()
	for _, t := range a.types {
		a.walk(typeExpr(t), a.follow[t], walkContext{detect: true, typeName: typeNodeName(t)})
	}
	return a.report.Dedup()
}

type termKind int

const (
	termLiteral termKind = iota
	termToken
	termOpaque
	termNegation
	termEOF
)

// A terminal that may appear in a FIRST or FOLLOW set.
type terminal struct {
	kind  termKind
	value string // Literal value, token identifier, or opaque type name.
	typ   lexer.TokenType
	id    int // Distinguishes negations.
}

func (t terminal) String() string {
	switch t.kind {
	case termLiteral:
		return strconv.Quote(t.value)
	case termToken:
		return "<" + strings.ToLower(t.value) + ">"
	case termNegation:
		return "~" + t.value
	case termEOF:
		return "EOF"
	case termOpaque:
	}
	return t.value
}

// Literals and token types are deliberately treated as distinct, and
// negations never overlap anything.
func (t terminal) overlaps(o terminal) bool {
	if t.kind != o.kind {
		return false
	}
	switch t.kind {
	case termLiteral:
		typesMatch := t.typ == lexer.EOF || o.typ == lexer.EOF || t.typ == o.typ
		return typesMatch && (t.value == o.value || t.value == "" || o.value == "")
	case termToken:
		return t.typ == o.typ
	case termOpaque:
		return t.value == o.value
	case termEOF:
		return true
	case termNegation:
	}
	return false
}

// An insertion-ordered set of terminals.
type termSet struct {
	items []terminal
	has   map[terminal]bool
}

func newTermSet(terms ...terminal) *termSet {
	s := &termSet{has: map[terminal]bool{}}
	for _, t := range terms {
		s.add(t)
	}
	return s
}

func (s *termSet) add(t terminal) bool {
	if s.has[t] {
		return false
	}
	s.has[t] = true
	s.items = append(s.items, t)
	return true
}

func (s *termSet) addAll(o *termSet) (changed bool) {
	for _, t := range o.items {
		if s.add(t) {
			changed = true
		}
	}
	return changed
}

func (s *termSet) union(o *termSet) *termSet {
	out := newTermSet(s.items...)
	out.addAll(o)
	return out
}

func (s *termSet) equal(o *termSet) bool {
	if len(s.items) != len(o.items) {
		return false
	}
	for _, t := range s.items {
		if !o.has[t] {
			return false
		}
	}
	return true
}

// overlap returns the first pair of overlapping terminals from s and o.
func (s *termSet) overlap(o *termSet) (terminal, terminal, bool) {
	for _, a := range s.items {
		for _, b := range o.items {
			if a.overlaps(b) {
				return a, b, true
			}
		}
	}
	return terminal{}, terminal{}, false
}

type firstInfo struct {
	terms    *termSet
	nullable bool
}

type analyzer struct {
	types     []node // Reachable *strct and *union nodes, in discovery order.
	first     map[node]*firstInfo
	memo      map[node]*firstInfo // Only populated once FIRST sets reach a fixpoint.
	follow    map[node]*termSet
	negations map[*negation]int
	report    *AnalysisReport
}

func typeExpr(n node) node {
	switch n := n.(type) {
	case *strct:
		return n.expr
	case *union:
		return &n.disjunction
	}
	panic(fmt.Sprintf("unsupported type node %T", n))
}

func typeNodeName(n node) string {
	switch n := n.(type) {
	case *strct:
		return reflectTypeName(n.typ)
	case *union:
		return reflectTypeName(n.typ)
	case *custom:
		return reflectTypeName(n.typ)
	case *parseable:
		return reflectTypeName(n.t)
	}
	return fmt.Sprintf("%T", n)
}

func reflectTypeName(t reflect.Type) string {
	if t.Name() != "" {
		return t.Name()
	}
	return t.String()
}

// Lookahead and negation subtrees consume no input from the perspective of
// the enclosing grammar, so types reachable only through them are skipped.
func (a *analyzer) collectTypes(root node) {
	seen := map[node]bool{}
	_ = visit(root, func(n node, next func() error) error {
		switch n.(type) {
		case *lookaheadGroup, *negation:
			return nil
		case *strct, *union:
			if seen[n] {
				return nil
			}
			seen[n] = true
			a.types = append(a.types, n)
			a.first[n] = &firstInfo{terms: newTermSet()}
			a.follow[n] = newTermSet()
		}
		return next()
	})
}

func (a *analyzer) computeFirstSets() {
	for changed := true; changed; {
		changed = false
		for _, t := range a.types {
			fi := a.firstOf(typeExpr(t))
			cur := a.first[t]
			if cur.terms.addAll(fi.terms) {
				changed = true
			}
			if fi.nullable && !cur.nullable {
				cur.nullable = true
				changed = true
			}
		}
	}
	a.memo = map[node]*firstInfo{}
}

func (a *analyzer) computeFollowSets() {
	for changed := true; changed; {
		changed = false
		for _, t := range a.types {
			if a.walk(typeExpr(t), a.follow[t], walkContext{}) {
				changed = true
			}
		}
	}
}

// The returned value must not be mutated.
func (a *analyzer) firstOf(n node) *firstInfo {
	if a.memo != nil {
		if fi, ok := a.memo[n]; ok {
			return fi
		}
	}
	fi := a.computeFirst(n)
	if a.memo != nil {
		a.memo[n] = fi
	}
	return fi
}

func (a *analyzer) computeFirst(n node) *firstInfo {
	switch n := n.(type) {
	case *strct, *union:
		if fi, ok := a.first[n]; ok {
			return fi
		}
		return &firstInfo{terms: newTermSet()}
	case *literal:
		return &firstInfo{terms: newTermSet(terminal{kind: termLiteral, value: n.s, typ: n.t})}
	case *reference:
		return &firstInfo{terms: newTermSet(terminal{kind: termToken, value: n.identifier, typ: n.typ})}
	case *custom, *parseable:
		return &firstInfo{terms: newTermSet(terminal{kind: termOpaque, value: typeNodeName(n)})}
	case *negation:
		id, ok := a.negations[n]
		if !ok {
			id = len(a.negations) + 1
			a.negations[n] = id
		}
		return &firstInfo{terms: newTermSet(terminal{kind: termNegation, value: fragmentEBNF(n.node), id: id})}
	case *lookaheadGroup:
		return &firstInfo{terms: newTermSet(), nullable: true}
	case *capture:
		return a.firstOf(n.node)
	case *group:
		inner := a.firstOf(n.expr)
		if n.mode == groupMatchZeroOrOne || n.mode == groupMatchZeroOrMore {
			return &firstInfo{terms: inner.terms, nullable: true}
		}
		return inner
	case *sequence:
		out := &firstInfo{terms: newTermSet(), nullable: true}
		for s := n; s != nil; s = s.next {
			fi := a.firstOf(s.node)
			out.terms.addAll(fi.terms)
			if !fi.nullable {
				out.nullable = false
				break
			}
		}
		return out
	case *disjunction:
		out := &firstInfo{terms: newTermSet()}
		for _, alt := range n.nodes {
			fi := a.firstOf(alt)
			out.terms.addAll(fi.terms)
			out.nullable = out.nullable || fi.nullable
		}
		return out
	}
	panic(fmt.Sprintf("unsupported node type %T", n))
}

type walkContext struct {
	detect   bool // Report conflicts rather than propagating FOLLOW sets.
	typeName string
	field    string // Field of the enclosing capture, if any.
}

// walk traverses n, whose FOLLOW set is fol. When not detecting it propagates
// FOLLOW sets into nested type nodes and reports whether any changed.
func (a *analyzer) walk(n node, fol *termSet, ctx walkContext) (changed bool) {
	switch n := n.(type) {
	case *strct, *union:
		if !ctx.detect && a.follow[n] != nil {
			return a.follow[n].addAll(fol)
		}
	case *sequence:
		var elems []node
		for s := n; s != nil; s = s.next {
			elems = append(elems, s.node)
		}
		follows := make([]*termSet, len(elems))
		cur := fol
		for i := len(elems) - 1; i >= 0; i-- {
			follows[i] = cur
			if fi := a.firstOf(elems[i]); fi.nullable {
				cur = fi.terms.union(cur)
			} else {
				cur = fi.terms
			}
		}
		for i, elem := range elems {
			if a.walk(elem, follows[i], ctx) {
				changed = true
			}
		}
	case *disjunction:
		if ctx.detect {
			a.checkDisjunction(n.nodes, ctx)
		}
		for _, alt := range n.nodes {
			if a.walk(alt, fol, ctx) {
				changed = true
			}
		}
	case *group:
		inner := fol
		switch n.mode {
		case groupMatchZeroOrMore, groupMatchOneOrMore:
			inner = fol.union(a.firstOf(n.expr).terms)
			fallthrough
		case groupMatchZeroOrOne:
			if ctx.detect {
				a.checkFirstFollow(n, fol, ctx)
			}
		case groupMatchOnce, groupMatchNonEmpty:
		}
		return a.walk(n.expr, inner, ctx)
	case *capture:
		ctx.field = n.field.Name
		return a.walk(n.node, fol, ctx)
	case *lookaheadGroup, *negation, *literal, *reference, *custom, *parseable:
	}
	return changed
}

func (a *analyzer) checkDisjunction(alts []node, ctx walkContext) {
	firsts := make([]*firstInfo, len(alts))
	guarded := make([]bool, len(alts))
	for i, alt := range alts {
		firsts[i] = a.firstOf(alt)
		guarded[i] = startsWithLookahead(alt)
	}
	for j := 1; j < len(alts); j++ {
		if guarded[j] {
			continue
		}
		reportedOverlap := false
		for i := 0; i < j; i++ {
			if guarded[i] {
				continue
			}
			tok, _, ok := firsts[i].terms.overlap(firsts[j].terms)
			if !ok {
				continue
			}
			earlier, later := fragmentEBNF(alts[i]), fragmentEBNF(alts[j])
			loc := ConflictLocation{TypeName: ctx.typeName, FieldName: disjunctionField(ctx, alts[i], alts[j])}
			snippet := earlier + " | " + later
			if !reportedOverlap {
				reportedOverlap = true
				a.report.Conflicts = append(a.report.Conflicts, Conflict{
					Type:           ConflictFirstFirst,
					Severity:       SeverityWarning,
					Message:        fmt.Sprintf("alternatives %d (%s) and %d (%s) can both start with %s", i+1, earlier, j+1, later, tok),
					Location:       loc,
					GrammarSnippet: snippet,
					Example:        tok.String(),
					Suggestion: "reorder the alternatives so the most specific comes first, factor out the common prefix, " +
						"or add a lookahead group such as (?= ...) to disambiguate them",
				})
			}
			if earlier == later && firsts[i].nullable == firsts[j].nullable && firsts[i].terms.equal(firsts[j].terms) {
				a.report.Conflicts = append(a.report.Conflicts, Conflict{
					Type:           ConflictUnreachable,
					Severity:       SeverityError,
					Message:        fmt.Sprintf("alternative %d (%s) can never match because it is shadowed by identical alternative %d", j+1, later, i+1),
					Location:       loc,
					GrammarSnippet: snippet,
					Example:        tok.String(),
					Suggestion:     "remove the duplicate alternative, or change it so that it can be distinguished from the earlier one",
				})
				break
			}
		}
	}
}

func (a *analyzer) checkFirstFollow(g *group, fol *termSet, ctx walkContext) {
	if startsWithLookahead(g.expr) {
		return
	}
	tok, next, ok := a.firstOf(g.expr).terms.overlap(fol)
	if !ok {
		return
	}
	var kind string
	switch g.mode {
	case groupMatchZeroOrOne:
		kind = "optional"
	case groupMatchZeroOrMore:
		kind = "zero-or-more"
	default:
		kind = "one-or-more"
	}
	field := ctx.field
	if field == "" {
		field = firstCaptureField(g)
	}
	snippet := ebnf(g)
	if len(snippet) < 4 {
		snippet = "(" + fragmentEBNF(g.expr) + ")" + strings.TrimPrefix(g.mode.String(), "n")
	}
	a.report.Conflicts = append(a.report.Conflicts, Conflict{
		Type:           ConflictFirstFollow,
		Severity:       SeverityWarning,
		Message:        fmt.Sprintf("%s group %s can start with %s, which can also follow it", kind, snippet, tok),
		Location:       ConflictLocation{TypeName: ctx.typeName, FieldName: field},
		GrammarSnippet: snippet,
		Example:        tok.String() + " " + next.String(),
		Suggestion: "make the first token of the group distinct from what follows it, add a terminating delimiter, " +
			"or add a lookahead group such as (?= ...) to decide when the group applies",
	})
}

func disjunctionField(ctx walkContext, earlier, later node) string {
	if ctx.field != "" {
		return ctx.field
	}
	if field := firstCaptureField(later); field != "" {
		return field
	}
	return firstCaptureField(earlier)
}

func firstCaptureField(n node) (name string) {
	_ = visit(n, func(n node, next func() error) error {
		if name != "" {
			return nil
		}
		switch n := n.(type) {
		case *capture:
			name = n.field.Name
			return nil
		case *strct, *union:
			return nil
		}
		return next()
	})
	return name
}

// Alternatives led by a lookahead group are explicitly disambiguated by the user.
func startsWithLookahead(n node) bool {
	switch n := n.(type) {
	case *lookaheadGroup:
		return true
	case *sequence:
		return startsWithLookahead(n.node)
	case *capture:
		return startsWithLookahead(n.node)
	case *group:
		return startsWithLookahead(n.expr)
	case *disjunction:
		for _, alt := range n.nodes {
			if !startsWithLookahead(alt) {
				return false
			}
		}
		return len(n.nodes) > 0
	}
	return false
}

// fragmentEBNF renders n as an inline EBNF fragment, naming (rather than
// expanding) productions.
func fragmentEBNF(n node) string {
	switch n := n.(type) {
	case *strct, *union:
		name := typeNodeName(n)
		return strings.ToUpper(name[:1]) + name[1:]
	}
	return ebnf(n)
}
