//go:build analyze

package participle

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ConflictType classifies a static grammar ambiguity.
type ConflictType int

const (
	// ConflictFirstFirst is reported when disjunction alternatives share a first token.
	ConflictFirstFirst ConflictType = iota
	// ConflictFirstFollow is reported when a ?, * or + group overlaps the tokens that follow it.
	ConflictFirstFollow
	// ConflictUnreachable is reported when an alternative is shadowed by an earlier identical one.
	ConflictUnreachable
)

// String returns "first/first", "first/follow" or "unreachable".
func (t ConflictType) String() string {
	switch t {
	case ConflictFirstFirst:
		return "first/first"
	case ConflictFirstFollow:
		return "first/follow"
	case ConflictUnreachable:
		return "unreachable"
	default:
		return fmt.Sprintf("conflict-type-%d", int(t))
	}
}

// Severity is the severity of a grammar conflict.
type Severity int

const (
	// SeverityWarning marks first/first and first/follow conflicts.
	SeverityWarning Severity = iota
	// SeverityError marks unreachable alternatives.
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
		return fmt.Sprintf("severity-%d", int(s))
	}
}

// ConflictLocation identifies the struct (and optional field) that owns a conflict.
// TypeName is the innermost Go struct type where the conflict originates.
type ConflictLocation struct {
	TypeName  string
	FieldName string
}

// String returns "TypeName" or "TypeName.FieldName".
func (l ConflictLocation) String() string {
	if l.FieldName == "" {
		return l.TypeName
	}
	return l.TypeName + "." + l.FieldName
}

// Conflict is one ambiguity found while analysing a grammar.
// Every string field is non-empty.
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
// Methods return new values and do not mutate the receiver.
type AnalysisReport struct {
	Conflicts []Conflict
}

// Errors returns the error-severity conflicts, in original order.
func (r *AnalysisReport) Errors() []Conflict {
	return r.collect(func(c Conflict) bool { return c.Severity == SeverityError })
}

// Warnings returns the warning-severity conflicts, in original order.
func (r *AnalysisReport) Warnings() []Conflict {
	return r.collect(func(c Conflict) bool { return c.Severity == SeverityWarning })
}

func (r *AnalysisReport) collect(pred func(Conflict) bool) []Conflict {
	if r == nil {
		return nil
	}
	out := make([]Conflict, 0)
	for _, c := range r.Conflicts {
		if pred(c) {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// FilterByType returns a new report containing only conflicts of type t, in original order.
func (r *AnalysisReport) FilterByType(t ConflictType) *AnalysisReport {
	return r.FilterWith(func(c Conflict) bool { return c.Type == t })
}

// FilterWith returns a new report containing conflicts for which pred is true, in original order.
func (r *AnalysisReport) FilterWith(pred func(Conflict) bool) *AnalysisReport {
	out := &AnalysisReport{}
	if r == nil || pred == nil {
		return out
	}
	for _, c := range r.Conflicts {
		if pred(c) {
			out.Conflicts = append(out.Conflicts, c)
		}
	}
	return out
}

// ConflictCount returns how many conflicts of type t the report contains.
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

// Summary returns "no conflicts detected" or
// "N conflict(s): A first/first, B first/follow, C unreachable".
// The three counts are always present, including zeroes.
func (r *AnalysisReport) Summary() string {
	if r.IsClean() {
		return "no conflicts detected"
	}
	ff, fl, ur := 0, 0, 0
	for _, c := range r.Conflicts {
		switch c.Type {
		case ConflictFirstFirst:
			ff++
		case ConflictFirstFollow:
			fl++
		case ConflictUnreachable:
			ur++
		}
	}
	return fmt.Sprintf("%d conflict(s): %d first/first, %d first/follow, %d unreachable", len(r.Conflicts), ff, fl, ur)
}

// String is a multi-line description of the report.
// It is non-empty even when the report is clean, and lists each conflict's type and location.
func (r *AnalysisReport) String() string {
	var b strings.Builder
	if r.IsClean() {
		b.WriteString("no conflicts detected\n")
		b.WriteString("conflicts: none\n")
		return b.String()
	}
	b.WriteString(r.Summary())
	b.WriteByte('\n')
	for _, c := range r.Conflicts {
		fmt.Fprintf(&b, "%s at %s\n", c.Type, c.Location)
		b.WriteString(c.String())
		b.WriteByte('\n')
	}
	return b.String()
}

// Merge returns a new report containing conflicts from r and other.
// Duplicates are removed by (Type, Location.String(), GrammarSnippet), keeping the first.
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

// Dedup returns a new report with duplicates removed by
// (Type, Location.String(), GrammarSnippet), preserving the first occurrence.
func (r *AnalysisReport) Dedup() *AnalysisReport {
	out := &AnalysisReport{}
	if r == nil {
		return out
	}
	seen := make(map[string]struct{}, len(r.Conflicts))
	for _, c := range r.Conflicts {
		key := c.Type.String() + "\x00" + c.Location.String() + "\x00" + c.GrammarSnippet
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out.Conflicts = append(out.Conflicts, c)
	}
	return out
}

// AnalysisOption adjusts AnalyzeWithOptions.
type AnalysisOption func(*analysisConfig)

type analysisConfig struct {
	suppress map[ConflictType]bool
}

// SuppressConflictType drops conflicts of type t from an analysis report.
// It has no effect on StrictMode.
func SuppressConflictType(t ConflictType) AnalysisOption {
	return func(c *analysisConfig) {
		if c.suppress == nil {
			c.suppress = map[ConflictType]bool{}
		}
		c.suppress[t] = true
	}
}

// Analyze reports grammar ambiguities in p.
func (p *Parser[G]) Analyze() (*AnalysisReport, error) {
	return p.AnalyzeWithOptions()
}

// AnalyzeWithOptions reports grammar ambiguities in p.
// SuppressConflictType options remove conflicts of the given types.
func (p *Parser[G]) AnalyzeWithOptions(opts ...AnalysisOption) (*AnalysisReport, error) {
	if p == nil {
		return nil, fmt.Errorf("analyze: nil parser")
	}
	root := p.typeNodes[p.rootType]
	if root == nil {
		return nil, fmt.Errorf("analyze: parser has no root grammar")
	}
	cfg := &analysisConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	report, err := analyzeRoot(root)
	if err != nil {
		return nil, err
	}
	if len(cfg.suppress) == 0 {
		return report, nil
	}
	return report.FilterWith(func(c Conflict) bool { return !cfg.suppress[c.Type] }), nil
}

func init() {
	runStrictAnalysis = func(root node) error {
		report, err := analyzeRoot(root)
		if err != nil {
			return err
		}
		if report != nil && !report.IsClean() {
			return fmt.Errorf("strict mode rejected grammar with conflict: %s", report.Summary())
		}
		return nil
	}
}

// term is one concrete lookahead symbol.
// Literals and named token types live in different namespaces, so "keyword" does not
// overlap the Ident token type.
type termKind uint8

const (
	termToken termKind = iota + 1
	termLiteral
)

type term struct {
	kind termKind
	text string
}

func (t term) String() string {
	if t.kind == termLiteral {
		return strconv.Quote(t.text)
	}
	return "<" + strings.ToLower(t.text) + ">"
}

// tokenSet is a FIRST set. epsilon is tracked for every node kind so FOLLOW can
// pass through @@ embeddings, not only through ? and * groups.
type tokenSet struct {
	terms   map[term]struct{}
	epsilon bool
}

type analyzer struct {
	memo      map[node]tokenSet
	visiting  map[node]bool
	follows   map[node]tokenSet
	inProp    map[node]bool
	conflicts []Conflict
}

func analyzeRoot(root node) (*AnalysisReport, error) {
	if root == nil {
		return nil, fmt.Errorf("analyze: nil grammar")
	}
	a := &analyzer{
		memo:     map[node]tokenSet{},
		visiting: map[node]bool{},
		follows:  map[node]tokenSet{},
		inProp:   map[node]bool{},
	}
	// Follow sets only grow, so a walk per dependency step converges.
	for i := 0; i < 128; i++ {
		changed := false
		a.prop(root, tokenSet{}, &changed)
		if !changed {
			break
		}
	}
	w := walkCtx{}
	if s, ok := root.(*strct); ok {
		w.typeName = s.typ.Name()
	} else if u, ok := root.(*union); ok && u.typ != nil {
		w.typeName = u.typ.Name()
	}
	a.collect(root, w, map[node]bool{})
	return &AnalysisReport{Conflicts: a.conflicts}, nil
}

func (a *analyzer) first(n node) tokenSet {
	if n == nil {
		return tokenSet{epsilon: true}
	}
	if fs, ok := a.memo[n]; ok {
		return fs
	}
	if a.visiting[n] {
		// Break cycles without treating the back-edge as nullable.
		return tokenSet{}
	}
	a.visiting[n] = true
	fs := a.computeFirst(n)
	a.visiting[n] = false
	a.memo[n] = fs
	return fs
}

func (a *analyzer) computeFirst(n node) tokenSet {
	switch n := n.(type) {
	case *disjunction:
		return a.firstOfAlts(n.nodes)
	case *union:
		return a.firstOfAlts(n.disjunction.nodes)
	case *strct:
		return a.first(n.expr)
	case *sequence:
		return a.firstSequence(n)
	case *capture:
		return a.first(n.node)
	case *group:
		return a.firstGroup(n)
	case *lookaheadGroup:
		// Lookahead consumes nothing, so it contributes only epsilon.
		return tokenSet{epsilon: true}
	case *negation:
		// Negations produce no conflicts and no concrete first tokens.
		return tokenSet{}
	case *reference:
		return tokenSet{terms: map[term]struct{}{{kind: termToken, text: n.identifier}: {}}}
	case *literal:
		return tokenSet{terms: map[term]struct{}{{kind: termLiteral, text: n.s}: {}}}
	case *custom, *parseable:
		return tokenSet{}
	default:
		panic(fmt.Sprintf("analyze: unsupported node %T", n))
	}
}

func (a *analyzer) firstOfAlts(alts []node) tokenSet {
	out := tokenSet{terms: map[term]struct{}{}}
	for _, alt := range alts {
		fs := a.first(alt)
		for t := range fs.terms {
			out.terms[t] = struct{}{}
		}
		if fs.epsilon {
			out.epsilon = true
		}
	}
	return out
}

func (a *analyzer) firstSequence(n *sequence) tokenSet {
	out := tokenSet{epsilon: true, terms: map[term]struct{}{}}
	for s := n; s != nil; s = s.next {
		fs := a.first(s.node)
		for t := range fs.terms {
			out.terms[t] = struct{}{}
		}
		if !fs.epsilon {
			out.epsilon = false
			break
		}
	}
	return out
}

func (a *analyzer) firstGroup(n *group) tokenSet {
	inner := a.first(n.expr)
	out := tokenSet{epsilon: inner.epsilon, terms: map[term]struct{}{}}
	for t := range inner.terms {
		out.terms[t] = struct{}{}
	}
	switch n.mode {
	case groupMatchZeroOrOne, groupMatchZeroOrMore:
		out.epsilon = true
	case groupMatchNonEmpty:
		out.epsilon = false
	case groupMatchOneOrMore, groupMatchOnce:
		// epsilon, when present, comes from the inner expression
	}
	return out
}

func (a *analyzer) prop(n node, follow tokenSet, changed *bool) {
	if n == nil {
		return
	}
	if !subsetTerms(follow, a.follows[n]) {
		a.follows[n] = unionTerms(a.follows[n], follow)
		*changed = true
	}
	if a.inProp[n] {
		return
	}
	a.inProp[n] = true
	defer func() { a.inProp[n] = false }()
	a.propChildren(n, a.follows[n], changed)
}

func (a *analyzer) propChildren(n node, follow tokenSet, changed *bool) {
	switch n := n.(type) {
	case *disjunction:
		for _, alt := range n.nodes {
			a.prop(alt, follow, changed)
		}
	case *union:
		for _, alt := range n.disjunction.nodes {
			a.prop(alt, follow, changed)
		}
	case *strct:
		a.prop(n.expr, follow, changed)
	case *sequence:
		elts := sequenceNodes(n)
		acc := follow
		elemFollow := make([]tokenSet, len(elts))
		for i := len(elts) - 1; i >= 0; i-- {
			elemFollow[i] = acc
			fs := a.first(elts[i])
			if fs.epsilon {
				acc = unionTerms(acc, withoutEpsilon(fs))
			} else {
				acc = withoutEpsilon(fs)
			}
		}
		for i, el := range elts {
			a.prop(el, elemFollow[i], changed)
		}
	case *capture:
		a.prop(n.node, follow, changed)
	case *group:
		innerFollow := follow
		if n.mode == groupMatchZeroOrMore || n.mode == groupMatchOneOrMore {
			// The repeated expression can be followed by another iteration.
			innerFollow = unionTerms(innerFollow, withoutEpsilon(a.first(n.expr)))
		}
		a.prop(n.expr, innerFollow, changed)
	case *lookaheadGroup, *negation:
		// Detection is suppressed for the whole subtree.
		return
	case *reference, *literal, *custom, *parseable:
		return
	default:
		panic(fmt.Sprintf("analyze: unsupported node %T", n))
	}
}

func sequenceNodes(n *sequence) []node {
	var out []node
	for s := n; s != nil; s = s.next {
		out = append(out, s.node)
	}
	return out
}

type walkCtx struct {
	typeName  string
	fieldName string
}

func (w walkCtx) location(n node) ConflictLocation {
	field := w.fieldName
	if field == "" && n != nil {
		field = dominantField(n)
	}
	typeName := w.typeName
	if typeName == "" {
		typeName = "grammar"
	}
	return ConflictLocation{TypeName: typeName, FieldName: field}
}

func (a *analyzer) collect(n node, w walkCtx, seen map[node]bool) {
	if n == nil || seen[n] {
		return
	}
	seen[n] = true
	switch n := n.(type) {
	case *lookaheadGroup, *negation:
		return
	case *strct:
		w.typeName = n.typ.Name()
		w.fieldName = ""
		a.collect(n.expr, w, seen)
	case *union:
		if n.typ != nil && n.typ.Name() != "" {
			w.typeName = n.typ.Name()
		}
		w.fieldName = ""
		a.detectDisjunction(n.disjunction.nodes, &n.disjunction, w)
		for _, alt := range n.disjunction.nodes {
			a.collect(alt, w, seen)
		}
	case *disjunction:
		a.detectDisjunction(n.nodes, n, w)
		for _, alt := range n.nodes {
			a.collect(alt, w, seen)
		}
	case *sequence:
		for s := n; s != nil; s = s.next {
			a.collect(s.node, w, seen)
		}
	case *capture:
		w.fieldName = n.field.Name
		a.collect(n.node, w, seen)
	case *group:
		a.detectGroup(n, w)
		a.collect(n.expr, w, seen)
	case *reference, *literal, *custom, *parseable:
		return
	default:
		panic(fmt.Sprintf("analyze: unsupported node %T", n))
	}
}

func (a *analyzer) detectDisjunction(alts []node, snippetNode node, w walkCtx) {
	if len(alts) < 2 {
		return
	}
	sets := make([]tokenSet, len(alts))
	snippets := make([]string, len(alts))
	for i, alt := range alts {
		sets[i] = a.first(alt)
		snippets[i] = strings.TrimSpace(ebnf(alt))
	}
	var overlap []term
	seenTerm := map[term]struct{}{}
	for i := 0; i < len(alts); i++ {
		if negationRooted(alts[i]) {
			continue
		}
		for j := i + 1; j < len(alts); j++ {
			if negationRooted(alts[j]) {
				continue
			}
			for _, t := range intersectTerms(sets[i], sets[j]) {
				if _, ok := seenTerm[t]; ok {
					continue
				}
				seenTerm[t] = struct{}{}
				overlap = append(overlap, t)
			}
		}
	}
	if len(overlap) > 0 {
		sortTerms(overlap)
		loc := w.location(snippetNode)
		a.add(Conflict{
			Type:           ConflictFirstFirst,
			Severity:       SeverityWarning,
			Message:        "alternatives share overlapping first tokens " + formatTerms(overlap),
			Location:       loc,
			GrammarSnippet: nodeSnippet(snippetNode),
			Example:        concreteTerm(overlap[0]),
			Suggestion:     "Give each alternative a distinct leading token so their first sets do not overlap",
		})
	}
	for j := 1; j < len(alts); j++ {
		if negationRooted(alts[j]) {
			continue
		}
		for i := 0; i < j; i++ {
			if negationRooted(alts[i]) {
				continue
			}
			if sameFirst(sets[i], sets[j]) && snippets[i] == snippets[j] {
				loc := w.location(alts[j])
				example := "epsilon"
				if terms := sortedTerms(sets[j]); len(terms) > 0 {
					example = concreteTerm(terms[0])
				}
				a.add(Conflict{
					Type:           ConflictUnreachable,
					Severity:       SeverityError,
					Message:        "alternative is shadowed by an earlier alternative with the same first set and the same EBNF",
					Location:       loc,
					GrammarSnippet: ensureSnippet(snippets[j]),
					Example:        example,
					Suggestion:     "Remove the duplicated alternative or change it so it is not fully shadowed",
				})
				break
			}
		}
	}
}

func (a *analyzer) detectGroup(g *group, w walkCtx) {
	switch g.mode {
	case groupMatchZeroOrOne, groupMatchZeroOrMore, groupMatchOneOrMore:
	default:
		return
	}
	if negationRooted(g.expr) {
		return
	}
	overlap := intersectTerms(a.first(g), a.follows[g])
	if len(overlap) == 0 {
		return
	}
	sortTerms(overlap)
	example := concreteTerm(overlap[0])
	if g.mode == groupMatchZeroOrMore || g.mode == groupMatchOneOrMore {
		example = example + " " + concreteTerm(overlap[0])
	}
	word := "optional"
	switch g.mode {
	case groupMatchZeroOrMore:
		word = "repeated"
	case groupMatchOneOrMore:
		word = "one-or-more"
	}
	a.add(Conflict{
		Type:           ConflictFirstFollow,
		Severity:       SeverityWarning,
		Message:        word + " group first tokens overlap the follow set on " + formatTerms(overlap),
		Location:       w.location(g),
		GrammarSnippet: nodeSnippet(g),
		Example:        example,
		Suggestion:     "Separate the group from the following token so the group's first set does not overlap what follows",
	})
}

func (a *analyzer) add(c Conflict) {
	if strings.TrimSpace(c.Message) == "" {
		c.Message = "grammar conflict detected"
	}
	c.GrammarSnippet = ensureSnippet(c.GrammarSnippet)
	if strings.TrimSpace(c.Example) == "" {
		c.Example = "tok"
	}
	if !strings.Contains(strings.TrimSpace(c.Suggestion), " ") {
		c.Suggestion = "Revise the grammar to remove the conflict"
	}
	if c.Location.TypeName == "" {
		c.Location.TypeName = "grammar"
	}
	a.conflicts = append(a.conflicts, c)
}

func negationRooted(n node) bool {
	switch n := n.(type) {
	case *negation:
		return true
	case *capture:
		return negationRooted(n.node)
	case *group:
		if n.mode == groupMatchOnce || n.mode == groupMatchNonEmpty {
			return negationRooted(n.expr)
		}
		return false
	default:
		return false
	}
}

func dominantField(n node) string {
	fields := map[string]struct{}{}
	collectFields(n, fields)
	if len(fields) != 1 {
		return ""
	}
	for f := range fields {
		return f
	}
	return ""
}

func collectFields(n node, fields map[string]struct{}) {
	if n == nil {
		return
	}
	switch n := n.(type) {
	case *capture:
		if n.field.Name != "" {
			fields[n.field.Name] = struct{}{}
		}
		switch n.node.(type) {
		case *strct, *union:
			return
		}
		collectFields(n.node, fields)
	case *group:
		collectFields(n.expr, fields)
	case *disjunction:
		for _, alt := range n.nodes {
			collectFields(alt, fields)
		}
	case *sequence:
		for s := n; s != nil; s = s.next {
			collectFields(s.node, fields)
		}
	case *strct, *union, *lookaheadGroup, *negation, *literal, *reference, *custom, *parseable:
		return
	default:
		return
	}
}

func nodeSnippet(n node) string {
	if n == nil {
		return ensureSnippet("")
	}
	return ensureSnippet(strings.TrimSpace(ebnf(n)))
}

func ensureSnippet(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		s = "expr"
	}
	for len(s) < 4 {
		s += " ..."
	}
	return s
}

func concreteTerm(t term) string {
	if t.kind == termLiteral {
		if t.text == "" {
			return "\"\""
		}
		return t.text
	}
	switch strings.ToLower(t.text) {
	case "ident":
		return "foo"
	case "int":
		return "123"
	case "float":
		return "1.5"
	case "string":
		return "\"bar\""
	case "rawstring":
		return "`bar`"
	case "char":
		return "'c'"
	case "comment":
		return "//c"
	default:
		if t.text == "" {
			return "tok"
		}
		return strings.ToLower(t.text)
	}
}

func formatTerms(terms []term) string {
	parts := make([]string, len(terms))
	for i, t := range terms {
		parts[i] = t.String()
	}
	return strings.Join(parts, ", ")
}

func intersectTerms(a, b tokenSet) []term {
	var out []term
	for t := range a.terms {
		if _, ok := b.terms[t]; ok {
			out = append(out, t)
		}
	}
	sortTerms(out)
	return out
}

func sortedTerms(s tokenSet) []term {
	out := make([]term, 0, len(s.terms))
	for t := range s.terms {
		out = append(out, t)
	}
	sortTerms(out)
	return out
}

func sortTerms(terms []term) {
	sort.Slice(terms, func(i, j int) bool {
		if terms[i].kind != terms[j].kind {
			return terms[i].kind < terms[j].kind
		}
		return terms[i].text < terms[j].text
	})
}

func sameFirst(a, b tokenSet) bool {
	if a.epsilon != b.epsilon || len(a.terms) != len(b.terms) {
		return false
	}
	for t := range a.terms {
		if _, ok := b.terms[t]; !ok {
			return false
		}
	}
	return true
}

func subsetTerms(small, big tokenSet) bool {
	for t := range small.terms {
		if _, ok := big.terms[t]; !ok {
			return false
		}
	}
	return true
}

func unionTerms(a, b tokenSet) tokenSet {
	out := tokenSet{
		epsilon: a.epsilon || b.epsilon,
		terms:   make(map[term]struct{}, len(a.terms)+len(b.terms)),
	}
	for t := range a.terms {
		out.terms[t] = struct{}{}
	}
	for t := range b.terms {
		out.terms[t] = struct{}{}
	}
	return out
}

func withoutEpsilon(s tokenSet) tokenSet {
	out := tokenSet{terms: make(map[term]struct{}, len(s.terms))}
	for t := range s.terms {
		out.terms[t] = struct{}{}
	}
	return out
}
