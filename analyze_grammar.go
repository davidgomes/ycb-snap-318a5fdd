//go:build analyze

package participle

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/alecthomas/participle/v2/lexer"
)

const (
	minSnippetLength   = 4
	maxExamplePrefix   = 8
	maxDescribedTokens = 4
)

type terminalKey struct {
	literal bool
	value   string
	typ     lexer.TokenType // lexer.EOF for untyped literals.
}

// A terminal is a token the grammar can match: either a literal or a lexer token type.
type terminal struct {
	terminalKey
	name string // Symbolic name of typ.
}

func literalTerminal(l *literal) terminal {
	return terminal{terminalKey{literal: true, value: l.s, typ: l.t}, l.tt}
}

func referenceTerminal(r *reference) terminal {
	return terminal{terminalKey{typ: r.typ}, r.identifier}
}

func (t terminal) String() string {
	switch {
	case !t.literal:
		return "<" + strings.ToLower(t.name) + ">"
	case t.typ == lexer.EOF || t.name == "":
		return fmt.Sprintf("%q", t.value)
	default:
		return fmt.Sprintf("%q:%s", t.value, t.name)
	}
}

// sample returns concrete source text for a token matched by the terminal.
func (t terminal) sample() string {
	if t.literal && t.value != "" {
		return t.value
	}
	switch strings.ToLower(t.name) {
	case "ident", "identifier", "name":
		return "foo"
	case "int", "integer", "number":
		return "42"
	case "float":
		return "3.14"
	case "string":
		return `"text"`
	case "rawstring":
		return "`text`"
	case "char":
		return "'c'"
	}
	return "<" + strings.ToLower(t.name) + ">"
}

// A termSet is a set of terminals, plus epsilon if the empty sequence is included.
//
// Sets returned by grammarAnalyzer.firstOf are shared and must not be modified.
type termSet struct {
	terms   map[terminalKey]terminal
	epsilon bool
}

func newTermSet(terms ...terminal) *termSet {
	s := &termSet{terms: make(map[terminalKey]terminal, len(terms))}
	for _, t := range terms {
		s.terms[t.terminalKey] = t
	}
	return s
}

func (s *termSet) clone() *termSet {
	out := newTermSet()
	out.addAll(s, true)
	return out
}

// addAll adds the terminals of other to s, and its epsilon if withEpsilon is true, returning true if s changed.
func (s *termSet) addAll(other *termSet, withEpsilon bool) bool {
	changed := false
	for k, t := range other.terms {
		if _, ok := s.terms[k]; !ok {
			s.terms[k] = t
			changed = true
		}
	}
	if withEpsilon && other.epsilon && !s.epsilon {
		s.epsilon = true
		changed = true
	}
	return changed
}

func (s *termSet) equal(other *termSet) bool {
	if s.epsilon != other.epsilon || len(s.terms) != len(other.terms) {
		return false
	}
	for k := range s.terms {
		if _, ok := other.terms[k]; !ok {
			return false
		}
	}
	return true
}

func (s *termSet) sorted() []terminal {
	out := make([]terminal, 0, len(s.terms))
	for _, t := range s.terms {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].String(), out[j].String(); a != b {
			return a < b
		}
		return out[i].typ < out[j].typ
	})
	return out
}

func analyzeGrammar(p *parserOptions) (report *AnalysisReport, err error) {
	root := p.typeNodes[p.rootType]
	if root == nil {
		return nil, errors.New("participle: parser has no grammar to analyze")
	}
	defer func() {
		if r := recover(); r != nil {
			report, err = nil, fmt.Errorf("participle: grammar analysis failed: %v", r)
		}
	}()
	return newGrammarAnalyzer(root, p.caseInsensitiveTokens).run(), nil
}

// grammarAnalyzer detects LL(1) style ambiguities in a grammar.
//
// Productions (struct and union nodes) are shared between all of their uses, so FIRST
// sets, FOLLOW sets and example samples are computed for them by fixpoint iteration.
// Lookahead groups and negations are opaque: they match no known first token and are
// never descended into, so no conflicts are reported within them.
type grammarAnalyzer struct {
	caseInsensitive map[lexer.TokenType]bool
	// Productions reachable outside lookahead groups and negations, in depth first order.
	productions []node
	// All productions, so that EBNF snippets refer to productions by name rather than expanding them.
	named map[node]bool

	first     map[node]*termSet
	converged bool
	memo      map[node]*termSet

	follow      map[node]*termSet
	groupFollow map[*group]*termSet

	samples map[node][]string

	conflicts []Conflict
}

func newGrammarAnalyzer(root node, caseInsensitive map[lexer.TokenType]bool) *grammarAnalyzer {
	a := &grammarAnalyzer{
		caseInsensitive: caseInsensitive,
		named:           map[node]bool{},
		first:           map[node]*termSet{},
		memo:            map[node]*termSet{},
		follow:          map[node]*termSet{},
		groupFollow:     map[*group]*termSet{},
		samples:         map[node][]string{},
	}
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
			a.productions = append(a.productions, n)
		}
		return next()
	})
	_ = visit(root, func(n node, next func() error) error {
		switch n.(type) {
		case *strct, *union:
			if a.named[n] {
				return nil
			}
			a.named[n] = true
		}
		return next()
	})
	return a
}

func (a *grammarAnalyzer) run() *AnalysisReport {
	a.computeFirst()
	a.computeFollow()
	a.computeSamples()
	a.conflicts = []Conflict{}
	for _, prod := range a.productions {
		a.check(productionExpr(prod), checkContext{typeName: productionName(prod)})
	}
	return (&AnalysisReport{Conflicts: a.conflicts}).Dedup()
}

func productionExpr(prod node) node {
	switch prod := prod.(type) {
	case *strct:
		return prod.expr
	case *union:
		return &prod.disjunction
	}
	panic(fmt.Sprintf("unsupported production type %T", prod))
}

func productionName(prod node) string {
	var t reflect.Type
	switch prod := prod.(type) {
	case *strct:
		t = prod.typ
	case *union:
		t = prod.typ
	}
	if t.Name() != "" {
		return t.Name()
	}
	return t.String()
}

func (a *grammarAnalyzer) computeFirst() {
	for changed := true; changed; {
		changed = false
		for _, prod := range a.productions {
			first := a.firstOf(productionExpr(prod))
			if old := a.first[prod]; old == nil || !old.equal(first) {
				a.first[prod] = first
				changed = true
			}
		}
	}
	a.converged = true
}

func (a *grammarAnalyzer) firstOf(n node) *termSet {
	if !a.converged {
		return a.computeFirstOf(n)
	}
	if first, ok := a.memo[n]; ok {
		return first
	}
	first := a.computeFirstOf(n)
	a.memo[n] = first
	return first
}

func (a *grammarAnalyzer) computeFirstOf(n node) *termSet {
	switch n := n.(type) {
	case *strct, *union:
		if first := a.first[n]; first != nil {
			return first
		}
		return newTermSet()

	case *disjunction:
		out := newTermSet()
		for _, alt := range n.nodes {
			out.addAll(a.firstOf(alt), true)
		}
		return out

	case *sequence:
		out := newTermSet()
		for s := n; s != nil; s = s.next {
			first := a.firstOf(s.node)
			out.addAll(first, false)
			if !first.epsilon {
				return out
			}
		}
		out.epsilon = true
		return out

	case *group:
		out := a.firstOf(n.expr).clone()
		switch n.mode {
		case groupMatchZeroOrOne, groupMatchZeroOrMore:
			out.epsilon = true
		case groupMatchNonEmpty:
			out.epsilon = false
		case groupMatchOnce, groupMatchOneOrMore:
		}
		return out

	case *capture:
		return a.firstOf(n.node)

	case *literal:
		return newTermSet(literalTerminal(n))

	case *reference:
		return newTermSet(referenceTerminal(n))

	case *lookaheadGroup, *negation, *custom, *parseable:
		return newTermSet()
	}
	panic(fmt.Sprintf("unsupported node type %T", n))
}

func (a *grammarAnalyzer) computeFollow() {
	for _, prod := range a.productions {
		a.follow[prod] = newTermSet()
	}
	for changed := true; changed; {
		changed = false
		for _, prod := range a.productions {
			if a.propagateFollow(productionExpr(prod), a.follow[prod]) {
				changed = true
			}
		}
	}
}

// propagateFollow pushes the follow set of n down to its descendants, returning true if
// the follow set of any production grew.
func (a *grammarAnalyzer) propagateFollow(n node, follow *termSet) bool {
	changed := false
	switch n := n.(type) {
	case *strct, *union:
		changed = a.follow[n].addAll(follow, false)

	case *disjunction:
		for _, alt := range n.nodes {
			if a.propagateFollow(alt, follow) {
				changed = true
			}
		}

	case *sequence:
		elems := sequenceNodes(n)
		rest := follow
		for i := len(elems) - 1; i >= 0; i-- {
			if a.propagateFollow(elems[i], rest) {
				changed = true
			}
			first := a.firstOf(elems[i])
			next := newTermSet()
			next.addAll(first, false)
			if first.epsilon {
				next.addAll(rest, false)
			}
			rest = next
		}

	case *group:
		a.groupFollow[n] = follow.clone()
		inner := follow
		if n.mode == groupMatchZeroOrMore || n.mode == groupMatchOneOrMore {
			inner = follow.clone()
			inner.addAll(a.firstOf(n.expr), false)
		}
		changed = a.propagateFollow(n.expr, inner)

	case *capture:
		changed = a.propagateFollow(n.node, follow)
	}
	return changed
}

func (a *grammarAnalyzer) computeSamples() {
	for changed := true; changed; {
		changed = false
		for _, prod := range a.productions {
			sample, ok := a.sampleOf(productionExpr(prod))
			if old, exists := a.samples[prod]; ok && (!exists || len(sample) < len(old)) {
				a.samples[prod] = sample
				changed = true
			}
		}
	}
}

// sampleOf returns the shortest known token sequence matched by n.
func (a *grammarAnalyzer) sampleOf(n node) ([]string, bool) {
	switch n := n.(type) {
	case *strct, *union:
		sample, ok := a.samples[n]
		return sample, ok

	case *disjunction:
		var best []string
		found := false
		for _, alt := range n.nodes {
			if sample, ok := a.sampleOf(alt); ok && (!found || len(sample) < len(best)) {
				best, found = sample, true
			}
		}
		return best, found

	case *sequence:
		var out []string
		for s := n; s != nil; s = s.next {
			sample, ok := a.sampleOf(s.node)
			if !ok {
				return nil, false
			}
			out = append(out, sample...)
		}
		return out, true

	case *group:
		if n.mode == groupMatchZeroOrOne || n.mode == groupMatchZeroOrMore {
			return nil, true
		}
		return a.sampleOf(n.expr)

	case *capture:
		return a.sampleOf(n.node)

	case *literal:
		return []string{literalTerminal(n).sample()}, true

	case *reference:
		return []string{referenceTerminal(n).sample()}, true

	case *lookaheadGroup:
		return nil, true

	case *negation:
		return []string{"<any>"}, true

	case *custom:
		return []string{"<" + strings.ToLower(n.typ.Name()) + ">"}, true

	case *parseable:
		return []string{"<" + strings.ToLower(n.t.Name()) + ">"}, true
	}
	panic(fmt.Sprintf("unsupported node type %T", n))
}

// overlap returns the terminals of x that can match the same token as a terminal of y.
func (a *grammarAnalyzer) overlap(x, y *termSet) []terminal {
	var out []terminal
	if y == nil || len(x.terms) == 0 || len(y.terms) == 0 {
		return out
	}
	for _, tx := range x.sorted() {
		for _, ty := range y.terms {
			if a.terminalsOverlap(tx, ty) {
				out = append(out, tx)
				break
			}
		}
	}
	return out
}

// terminalsOverlap reports whether a single token can match both terminals.
//
// Literals and token types are treated as distinct, as a literal is usually a keyword
// or punctuation that is deliberately tried before a general token type.
func (a *grammarAnalyzer) terminalsOverlap(x, y terminal) bool {
	if x.literal != y.literal {
		return false
	}
	if !x.literal {
		return x.typ == y.typ
	}
	if x.typ != lexer.EOF && y.typ != lexer.EOF && x.typ != y.typ {
		return false
	}
	if x.value == "" || y.value == "" || x.value == y.value {
		return true
	}
	typ := x.typ
	if typ == lexer.EOF {
		typ = y.typ
	}
	caseInsensitive := a.caseInsensitive[typ] || (typ == lexer.EOF && len(a.caseInsensitive) > 0)
	return caseInsensitive && strings.EqualFold(x.value, y.value)
}

// snippet renders n as EBNF, referring to productions by name.
func (a *grammarAnalyzer) snippet(n node) string {
	out := &ebnfp{}
	var productions []*ebnfp
	buildEBNF(true, n, a.named, out, &productions)
	return out.out
}

type checkContext struct {
	typeName string
	// Field of the innermost capture enclosing the current node.
	field string
	// Field of the last capture preceding the current node.
	lastField string
	// Sample tokens leading up to the current node within the production.
	prefix []string
	// Nodes following the current node within the production.
	following []node
}

func (c checkContext) location(candidates ...node) ConflictLocation {
	field := c.field
	for _, n := range candidates {
		if field != "" {
			break
		}
		field = captureField(n, false)
	}
	if field == "" {
		field = c.lastField
	}
	return ConflictLocation{TypeName: c.typeName, FieldName: field}
}

func (c checkContext) example(tokens ...string) string {
	prefix := c.prefix
	if len(prefix) > maxExamplePrefix {
		prefix = append([]string{"..."}, prefix[len(prefix)-maxExamplePrefix:]...)
	}
	return strings.Join(append(append([]string{}, prefix...), tokens...), " ")
}

// check reports conflicts in n and its descendants within the current production.
//
// Nested productions are checked separately, while lookahead groups and negations are
// deliberately not checked.
func (a *grammarAnalyzer) check(n node, ctx checkContext) {
	switch n := n.(type) {
	case *disjunction:
		a.checkAlternatives(n.nodes, ctx)
		for _, alt := range n.nodes {
			a.check(alt, ctx)
		}

	case *sequence:
		elems := sequenceNodes(n)
		for i, elem := range elems {
			child := ctx
			child.following = append(append([]node{}, elems[i+1:]...), ctx.following...)
			a.check(elem, child)
			if sample, ok := a.sampleOf(elem); ok {
				ctx.prefix = append(append([]string{}, ctx.prefix...), sample...)
			}
			if field := captureField(elem, true); field != "" {
				ctx.lastField = field
			}
		}

	case *group:
		if n.mode == groupMatchZeroOrOne || n.mode == groupMatchZeroOrMore || n.mode == groupMatchOneOrMore {
			a.checkFollow(n, ctx)
		}
		a.check(n.expr, ctx)

	case *capture:
		ctx.field = n.field.Name
		a.check(n.node, ctx)
	}
}

func (a *grammarAnalyzer) checkAlternatives(alts []node, ctx checkContext) {
	firsts := make([]*termSet, len(alts))
	snippets := make([]string, len(alts))
	for i, alt := range alts {
		firsts[i] = a.firstOf(alt)
		snippets[i] = a.snippet(alt)
	}
	for j := 1; j < len(alts); j++ {
		reportedUnreachable := false
		for i := 0; i < j; i++ {
			shared := a.overlap(firsts[i], firsts[j])
			if len(shared) == 0 {
				continue
			}
			location := ctx.location(alts[j], alts[i])
			snippet := minSnippet(snippets[i] + " | " + snippets[j])
			tokens := describeTerminals(shared)
			a.conflicts = append(a.conflicts, Conflict{
				Type:     ConflictFirstFirst,
				Severity: SeverityWarning,
				Message: fmt.Sprintf("alternative %d (%s) and alternative %d (%s) can both start with %s; alternative %d is only tried if alternative %d fails",
					i+1, snippets[i], j+1, snippets[j], tokens, j+1, i+1),
				Location:       location,
				GrammarSnippet: snippet,
				Example:        ctx.example(shared[0].sample()),
				Suggestion: fmt.Sprintf("Make the alternatives start with distinct tokens, factor the common leading %s out of both alternatives, or disambiguate them with a lookahead group such as (?= ...)",
					tokens),
			})
			if reportedUnreachable || snippets[i] != snippets[j] || !firsts[i].equal(firsts[j]) {
				continue
			}
			reportedUnreachable = true
			sample, ok := a.sampleOf(alts[j])
			if !ok || len(sample) == 0 {
				sample = []string{shared[0].sample()}
			}
			a.conflicts = append(a.conflicts, Conflict{
				Type:     ConflictUnreachable,
				Severity: SeverityError,
				Message: fmt.Sprintf("alternative %d (%s) can never match because it is identical to alternative %d, which is always tried first",
					j+1, snippets[j], i+1),
				Location:       location,
				GrammarSnippet: snippet,
				Example:        ctx.example(sample...),
				Suggestion: fmt.Sprintf("Remove the duplicate alternative %s, or change it so that it matches input that alternative %d does not",
					snippets[j], i+1),
			})
		}
	}
}

func (a *grammarAnalyzer) checkFollow(g *group, ctx checkContext) {
	first := a.firstOf(g.expr)
	shared := a.overlap(first, a.groupFollow[g])
	if len(shared) == 0 {
		return
	}
	groupSnippet := a.snippet(g)
	snippet := groupSnippet
	if following := a.followingSnippet(first, ctx.following); following != "" {
		snippet += " " + following
	}
	kind, decision := "optional group", "enter or skip it"
	if g.mode != groupMatchZeroOrOne {
		kind, decision = "repetition", "repeat it or stop"
	}
	tokens := describeTerminals(shared)
	a.conflicts = append(a.conflicts, Conflict{
		Type:     ConflictFirstFollow,
		Severity: SeverityWarning,
		Message: fmt.Sprintf("%s %s can start with %s, which can also follow it, so the parser cannot tell whether to %s",
			kind, groupSnippet, tokens, decision),
		Location:       ctx.location(g),
		GrammarSnippet: minSnippet(snippet),
		Example:        ctx.example(shared[0].sample()),
		Suggestion: fmt.Sprintf("Ensure %s cannot both start and follow the %s, eg. by adding a delimiter or terminator, or guard it with a lookahead group such as (?! ...)",
			tokens, kind),
	})
}

// followingSnippet renders the nodes following a group, up to the first one that can
// start with a token in first, or returns "" if the conflicting token does not come
// from the immediately following nodes.
func (a *grammarAnalyzer) followingSnippet(first *termSet, following []node) string {
	for i, n := range following {
		nodeFirst := a.firstOf(n)
		if len(a.overlap(first, nodeFirst)) > 0 {
			parts := make([]string, 0, i+1)
			for _, f := range following[:i+1] {
				parts = append(parts, a.snippet(f))
			}
			return strings.Join(parts, " ")
		}
		if !nodeFirst.epsilon {
			break
		}
	}
	return ""
}

func sequenceNodes(s *sequence) []node {
	var out []node
	for ; s != nil; s = s.next {
		out = append(out, s.node)
	}
	return out
}

// captureField returns the field of the first (or last) capture within n.
func captureField(n node, last bool) string {
	field := ""
	_ = visit(n, func(n node, next func() error) error {
		if field != "" && !last {
			return nil
		}
		switch n := n.(type) {
		case *capture:
			field = n.field.Name
			return nil
		case *strct, *union, *lookaheadGroup, *negation:
			return nil
		}
		return next()
	})
	return field
}

func describeTerminals(terms []terminal) string {
	parts := make([]string, 0, maxDescribedTokens+1)
	for i, t := range terms {
		if i == maxDescribedTokens {
			parts = append(parts, fmt.Sprintf("%d more", len(terms)-i))
			break
		}
		parts = append(parts, t.String())
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " or " + parts[len(parts)-1]
}

func minSnippet(s string) string {
	for utf8.RuneCountInString(s) < minSnippetLength {
		s = "(" + s + ")"
	}
	return s
}
