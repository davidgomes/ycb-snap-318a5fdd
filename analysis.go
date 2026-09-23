//go:build analyze

package participle

import (
	"fmt"
	"strings"

	"github.com/alecthomas/participle/v2/lexer"
)

func init() {
	strictModeCheck = func(p *parserOptions) error {
		report, err := p.analyze()
		if err != nil {
			return err
		}
		if !report.IsClean() {
			return fmt.Errorf("strict mode: grammar conflict(s) detected: %s\n%s", report.Summary(), report)
		}
		return nil
	}
}

// An AnalysisOption configures grammar analysis.
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
func (p *Parser[G]) Analyze() (*AnalysisReport, error) {
	return p.AnalyzeWithOptions()
}

// AnalyzeWithOptions statically analyses the grammar for ambiguities.
func (p *Parser[G]) AnalyzeWithOptions(opts ...AnalysisOption) (*AnalysisReport, error) {
	options := &analysisOptions{suppressed: map[ConflictType]bool{}}
	for _, opt := range opts {
		opt(options)
	}
	report, err := p.parserOptions.analyze()
	if err != nil {
		return nil, err
	}
	return report.FilterWith(func(c Conflict) bool { return !options.suppressed[c.Type] }), nil
}

func (p *parserOptions) analyze() (report *AnalysisReport, err error) {
	root := p.typeNodes[p.rootType]
	if root == nil {
		return nil, fmt.Errorf("no grammar to analyze")
	}
	defer func() {
		if r := recover(); r != nil {
			report = nil
			err = fmt.Errorf("grammar analysis failed: %v", r)
		}
	}()
	a := &analyzer{
		first:  map[node]*firstSet{},
		follow: map[node]*firstSet{},
		seen:   map[node]bool{},
	}
	a.collect(root)
	a.computeFirst()
	a.computeFollow(root)
	for _, prod := range a.productions {
		a.checkProduction(prod)
	}
	return (&AnalysisReport{Conflicts: a.conflicts}).Dedup(), nil
}

type tokenKind int

const (
	tokLiteral tokenKind = iota
	tokType
	tokOpaque
)

// A terminal that can appear in a first or follow set.
type firstToken struct {
	kind  tokenKind
	value string          // literal value
	typ   lexer.TokenType // literal type constraint or reference type
	name  string          // symbolic name for display (and identity for opaque tokens)
}

func (t firstToken) overlaps(o firstToken) bool {
	if t.kind != o.kind {
		return false
	}
	switch t.kind {
	case tokLiteral:
		valueMatch := t.value == o.value || t.value == "" || o.value == ""
		typeMatch := t.typ == lexer.EOF || o.typ == lexer.EOF || t.typ == o.typ
		return valueMatch && typeMatch
	case tokType:
		return t.typ == o.typ
	default:
		return t.name == o.name
	}
}

func (t firstToken) String() string {
	switch t.kind {
	case tokLiteral:
		return fmt.Sprintf("%q", t.value)
	case tokType:
		return "<" + strings.ToLower(t.name) + ">"
	default:
		return t.name
	}
}

// example returns a plausible concrete source token for t.
func (t firstToken) example() string {
	switch t.kind {
	case tokLiteral:
		if t.value == "" {
			return "x"
		}
		return t.value
	case tokType:
		switch t.name {
		case "Ident":
			return "foo"
		case "Int":
			return "42"
		case "Float":
			return "3.14"
		case "String", "RawString":
			return `"text"`
		case "Char":
			return "'c'"
		}
		return t.String()
	default:
		return "<" + t.name + ">"
	}
}

// The set of terminals a node can start with (or, for follow sets, be followed by).
//
// epsilon indicates the node can match nothing (or, for follow sets, that end of input may follow).
type firstSet struct {
	tokens  []firstToken
	epsilon bool
}

func (f *firstSet) add(t firstToken) bool {
	for _, existing := range f.tokens {
		if existing == t {
			return false
		}
	}
	f.tokens = append(f.tokens, t)
	return true
}

func (f *firstSet) merge(o *firstSet, withEpsilon bool) bool {
	changed := false
	for _, t := range o.tokens {
		if f.add(t) {
			changed = true
		}
	}
	if withEpsilon && o.epsilon && !f.epsilon {
		f.epsilon = true
		changed = true
	}
	return changed
}

func (f *firstSet) contains(t firstToken) bool {
	for _, existing := range f.tokens {
		if existing == t {
			return true
		}
	}
	return false
}

func (f *firstSet) equal(o *firstSet) bool {
	if f.epsilon != o.epsilon || len(f.tokens) != len(o.tokens) {
		return false
	}
	for _, t := range f.tokens {
		if !o.contains(t) {
			return false
		}
	}
	return true
}

// overlap returns the tokens of f that overlap any token of o.
func (f *firstSet) overlap(o *firstSet) []firstToken {
	var out []firstToken
	for _, a := range f.tokens {
		for _, b := range o.tokens {
			if a.overlaps(b) {
				out = append(out, a)
				break
			}
		}
	}
	return out
}

type analyzer struct {
	// Productions (*strct and *union) in depth-first order from the root.
	productions []node
	seen        map[node]bool
	first       map[node]*firstSet
	follow      map[node]*firstSet
	conflicts   []Conflict
}

func (a *analyzer) collect(n node) {
	switch n := n.(type) {
	case *strct:
		if a.seen[n] {
			return
		}
		a.seen[n] = true
		a.productions = append(a.productions, n)
		a.collect(n.expr)
	case *union:
		if a.seen[n] {
			return
		}
		a.seen[n] = true
		a.productions = append(a.productions, n)
		for _, member := range n.disjunction.nodes {
			a.collect(member)
		}
	case *disjunction:
		for _, alt := range n.nodes {
			a.collect(alt)
		}
	case *sequence:
		for s := n; s != nil; s = s.next {
			a.collect(s.node)
		}
	case *capture:
		a.collect(n.node)
	case *group:
		a.collect(n.expr)
	}
}

func productionExpr(n node) node {
	switch n := n.(type) {
	case *strct:
		return n.expr
	case *union:
		return &n.disjunction
	}
	panic(fmt.Sprintf("%T is not a production", n))
}

func (a *analyzer) computeFirst() {
	for _, prod := range a.productions {
		a.first[prod] = &firstSet{}
	}
	for changed := true; changed; {
		changed = false
		for _, prod := range a.productions {
			f := a.firstOf(productionExpr(prod))
			if a.first[prod].merge(&f, true) {
				changed = true
			}
		}
	}
}

func (a *analyzer) firstOf(n node) firstSet {
	switch n := n.(type) {
	case *strct, *union:
		if f, ok := a.first[n]; ok {
			return firstSet{tokens: append([]firstToken(nil), f.tokens...), epsilon: f.epsilon}
		}
		return firstSet{}
	case *disjunction:
		out := firstSet{}
		for _, alt := range n.nodes {
			f := a.firstOf(alt)
			out.merge(&f, true)
		}
		return out
	case *sequence:
		out := firstSet{}
		for s := n; s != nil; s = s.next {
			f := a.firstOf(s.node)
			out.merge(&f, false)
			if !f.epsilon {
				return out
			}
		}
		out.epsilon = true
		return out
	case *capture:
		return a.firstOf(n.node)
	case *group:
		f := a.firstOf(n.expr)
		if n.mode == groupMatchZeroOrOne || n.mode == groupMatchZeroOrMore {
			f.epsilon = true
		}
		return f
	case *lookaheadGroup:
		return firstSet{epsilon: true}
	case *reference:
		return firstSet{tokens: []firstToken{{kind: tokType, typ: n.typ, name: n.identifier}}}
	case *literal:
		return firstSet{tokens: []firstToken{{kind: tokLiteral, value: n.s, typ: n.t, name: n.tt}}}
	case *negation:
		return firstSet{tokens: []firstToken{{kind: tokOpaque, name: fmt.Sprintf("~%p", n)}}}
	case *custom:
		return firstSet{tokens: []firstToken{{kind: tokOpaque, name: n.typ.String()}}}
	case *parseable:
		return firstSet{tokens: []firstToken{{kind: tokOpaque, name: n.t.String()}}}
	}
	panic(fmt.Sprintf("unsupported node type %T", n))
}

// followOfRest returns the set of tokens that can follow the node preceding s in a sequence.
func (a *analyzer) followOfRest(s *sequence, follow *firstSet) *firstSet {
	if s == nil {
		return follow
	}
	f := a.firstOf(s)
	out := &firstSet{tokens: f.tokens}
	if f.epsilon {
		out.merge(follow, true)
	}
	return out
}

func (a *analyzer) repeatFollow(g *group, follow *firstSet) *firstSet {
	if g.mode != groupMatchZeroOrMore && g.mode != groupMatchOneOrMore {
		return follow
	}
	f := a.firstOf(g.expr)
	out := &firstSet{tokens: f.tokens}
	out.merge(follow, true)
	return out
}

func (a *analyzer) computeFollow(root node) {
	for _, prod := range a.productions {
		a.follow[prod] = &firstSet{}
	}
	if f, ok := a.follow[root]; ok {
		f.epsilon = true
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

func (a *analyzer) propagateFollow(n node, follow *firstSet) bool {
	switch n := n.(type) {
	case *strct, *union:
		return a.follow[n].merge(follow, true)
	case *disjunction:
		changed := false
		for _, alt := range n.nodes {
			if a.propagateFollow(alt, follow) {
				changed = true
			}
		}
		return changed
	case *sequence:
		changed := false
		for s := n; s != nil; s = s.next {
			if a.propagateFollow(s.node, a.followOfRest(s.next, follow)) {
				changed = true
			}
		}
		return changed
	case *capture:
		return a.propagateFollow(n.node, follow)
	case *group:
		return a.propagateFollow(n.expr, a.repeatFollow(n, follow))
	}
	return false
}

type checkContext struct {
	typeName string
	field    string
}

func (a *analyzer) checkProduction(prod node) {
	switch prod := prod.(type) {
	case *strct:
		a.check(prod.expr, a.follow[prod], checkContext{typeName: typeName(prod)})
	case *union:
		a.checkDisjunction(prod.disjunction.nodes, &prod.disjunction, checkContext{typeName: typeName(prod)})
	}
}

func (a *analyzer) check(n node, follow *firstSet, ctx checkContext) {
	switch n := n.(type) {
	case *disjunction:
		a.checkDisjunction(n.nodes, n, ctx)
		for _, alt := range n.nodes {
			a.check(alt, follow, ctx)
		}
	case *sequence:
		for s := n; s != nil; s = s.next {
			a.check(s.node, a.followOfRest(s.next, follow), ctx)
		}
	case *capture:
		ctx.field = n.field.Name
		a.check(n.node, follow, ctx)
	case *group:
		if n.mode == groupMatchZeroOrOne || n.mode == groupMatchZeroOrMore || n.mode == groupMatchOneOrMore {
			a.checkFirstFollow(n, follow, ctx)
		}
		a.check(n.expr, a.repeatFollow(n, follow), ctx)
	}
	// Lookahead groups and negations are deliberately not descended into.
}

func (a *analyzer) checkDisjunction(alts []node, n node, ctx checkContext) {
	firsts := make([]firstSet, len(alts))
	snippets := make([]string, len(alts))
	for i, alt := range alts {
		firsts[i] = a.firstOf(alt)
		snippets[i] = snippet(alt)
	}
	shadowed := make([]bool, len(alts))
	for i := range alts {
		for j := i + 1; j < len(alts); j++ {
			loc := a.location(n, ctx)
			pair := padSnippet(snippets[i] + " | " + snippets[j])
			if overlap := firsts[i].overlap(&firsts[j]); len(overlap) > 0 {
				a.conflicts = append(a.conflicts, Conflict{
					Type:     ConflictFirstFirst,
					Severity: SeverityWarning,
					Message: fmt.Sprintf("alternatives %d (%s) and %d (%s) can both start with %s",
						i+1, snippets[i], j+1, snippets[j], describeTokens(overlap)),
					Location:       loc,
					GrammarSnippet: pair,
					Example:        overlap[0].example(),
					Suggestion: "Reorder the alternatives or factor out their common prefix so each begins " +
						"with a distinct token, or increase lookahead with participle.UseLookahead().",
				})
			}
			if !shadowed[j] && snippets[i] == snippets[j] && firsts[i].equal(&firsts[j]) {
				shadowed[j] = true
				example := "<empty input>"
				if len(firsts[j].tokens) > 0 {
					example = firsts[j].tokens[0].example()
				}
				a.conflicts = append(a.conflicts, Conflict{
					Type:     ConflictUnreachable,
					Severity: SeverityError,
					Message: fmt.Sprintf("alternative %d (%s) is unreachable because it is identical to alternative %d",
						j+1, snippets[j], i+1),
					Location:       loc,
					GrammarSnippet: pair,
					Example:        example,
					Suggestion: fmt.Sprintf("Remove the duplicate alternative %d, or change it so that it "+
						"matches input the earlier alternative %d does not.", j+1, i+1),
				})
			}
		}
	}
}

func (a *analyzer) checkFirstFollow(g *group, follow *firstSet, ctx checkContext) {
	f := a.firstOf(g.expr)
	overlap := f.overlap(follow)
	if len(overlap) == 0 {
		return
	}
	var kind string
	switch g.mode { // nolint: exhaustive
	case groupMatchZeroOrOne:
		kind = "optional"
	default:
		kind = "repeated"
	}
	s := snippet(g)
	a.conflicts = append(a.conflicts, Conflict{
		Type:     ConflictFirstFollow,
		Severity: SeverityWarning,
		Message: fmt.Sprintf("%s group %s can start with %s, which can also follow it; the group will greedily consume it",
			kind, s, describeTokens(overlap)),
		Location:       a.location(g, ctx),
		GrammarSnippet: padSnippet(s),
		Example:        overlap[0].example(),
		Suggestion: "Make the tokens that can start the " + kind + " group distinct from those that can follow it, " +
			"add a distinguishing delimiter, or increase lookahead with participle.UseLookahead().",
	})
}

func (a *analyzer) location(n node, ctx checkContext) ConflictLocation {
	field := ctx.field
	if field == "" {
		field = firstCaptureField(n)
	}
	return ConflictLocation{TypeName: ctx.typeName, FieldName: field}
}

// firstCaptureField returns the name of the first field captured within n, without crossing productions.
func firstCaptureField(n node) string {
	switch n := n.(type) {
	case *capture:
		return n.field.Name
	case *disjunction:
		for _, alt := range n.nodes {
			if f := firstCaptureField(alt); f != "" {
				return f
			}
		}
	case *sequence:
		for s := n; s != nil; s = s.next {
			if f := firstCaptureField(s.node); f != "" {
				return f
			}
		}
	case *group:
		return firstCaptureField(n.expr)
	}
	return ""
}

func typeName(n node) string {
	var name string
	switch n := n.(type) {
	case *strct:
		name = n.typ.Name()
		if name == "" {
			name = n.typ.String()
		}
	case *union:
		name = n.typ.Name()
		if name == "" {
			name = n.typ.String()
		}
	}
	return name
}

func snippet(n node) string {
	switch n := n.(type) {
	case *strct, *union:
		name := typeName(n)
		return strings.ToUpper(name[:1]) + name[1:]
	case *custom:
		return strings.ToUpper(n.typ.Name()[:1]) + n.typ.Name()[1:]
	}
	return ebnf(n)
}

func padSnippet(s string) string {
	if len(s) < 4 {
		return "(" + s + ")"
	}
	return s
}

func describeTokens(tokens []firstToken) string {
	parts := make([]string, len(tokens))
	for i, t := range tokens {
		parts[i] = t.String()
	}
	return strings.Join(parts, ", ")
}
