//go:build analyze

package participle

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/alecthomas/participle/v2/lexer"
)

// ConflictType classifies a static grammar ambiguity.
type ConflictType int

const (
	// ConflictFirstFirst is reported when disjunction alternatives share first tokens.
	ConflictFirstFirst ConflictType = iota
	// ConflictFirstFollow is reported when a ?, * or + group overlaps the tokens that can follow it.
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
		return fmt.Sprintf("conflict(%d)", int(t))
	}
}

// Severity is the severity of a grammar conflict.
type Severity int

const (
	// SeverityWarning marks ambiguities that parsing may still resolve by ordered choice.
	SeverityWarning Severity = iota
	// SeverityError marks alternatives that can never be reached.
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
		return fmt.Sprintf("severity(%d)", int(s))
	}
}

// ConflictLocation identifies the Go type, and optional field, that contains a conflict.
type ConflictLocation struct {
	// TypeName is the struct (or union interface) where the conflict originates.
	// Nested grammars use the innermost struct.
	TypeName string
	// FieldName is the struct field associated with the conflict, when there is one.
	FieldName string
}

// String returns "TypeName" or "TypeName.FieldName".
func (l ConflictLocation) String() string {
	if l.FieldName == "" {
		return l.TypeName
	}
	return l.TypeName + "." + l.FieldName
}

// Conflict is one ambiguous or unreachable grammar fragment.
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

// AnalysisReport is the result of statically analyzing a grammar.
type AnalysisReport struct {
	Conflicts []Conflict
}

// Errors returns the conflicts whose severity is error. The report is not modified.
func (r *AnalysisReport) Errors() []Conflict {
	return r.collect(func(c Conflict) bool { return c.Severity == SeverityError })
}

// Warnings returns the conflicts whose severity is warning. The report is not modified.
func (r *AnalysisReport) Warnings() []Conflict {
	return r.collect(func(c Conflict) bool { return c.Severity == SeverityWarning })
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
	out.Conflicts = r.collect(pred)
	return out
}

// ConflictCount returns how many conflicts have the given type.
func (r *AnalysisReport) ConflictCount(t ConflictType) int {
	n := 0
	if r == nil {
		return 0
	}
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

// Summary describes the report in one line.
//
// A clean report is "no conflicts detected". Otherwise the line is
// "N conflict(s): A first/first, B first/follow, C unreachable", always with all three counts.
func (r *AnalysisReport) Summary() string {
	if r.IsClean() {
		return "no conflicts detected"
	}
	n := len(r.Conflicts)
	noun := "conflict"
	if n != 1 {
		noun = "conflicts"
	}
	return fmt.Sprintf("%d %s: %d first/first, %d first/follow, %d unreachable",
		n, noun,
		r.ConflictCount(ConflictFirstFirst),
		r.ConflictCount(ConflictFirstFollow),
		r.ConflictCount(ConflictUnreachable))
}

// String returns a multi-line description of the report, including each conflict type and location.
func (r *AnalysisReport) String() string {
	var b strings.Builder
	b.WriteString(r.Summary())
	b.WriteByte('\n')
	if r != nil {
		for _, c := range r.Conflicts {
			fmt.Fprintf(&b, "%s at %s\n", c.Type, c.Location)
		}
	}
	return b.String()
}

// Merge returns a new report containing conflicts from r and other.
// Duplicates are removed by (Type, Location.String(), GrammarSnippet), keeping the first occurrence.
func (r *AnalysisReport) Merge(other *AnalysisReport) *AnalysisReport {
	merged := &AnalysisReport{}
	if r != nil {
		merged.Conflicts = append(merged.Conflicts, r.Conflicts...)
	}
	if other != nil {
		merged.Conflicts = append(merged.Conflicts, other.Conflicts...)
	}
	return merged.Dedup()
}

// Dedup returns a new report with duplicate conflicts removed.
// Identity is (Type, Location.String(), GrammarSnippet). Order of first occurrences is preserved.
func (r *AnalysisReport) Dedup() *AnalysisReport {
	out := &AnalysisReport{}
	if r == nil {
		return out
	}
	seen := make(map[string]struct{}, len(r.Conflicts))
	for _, c := range r.Conflicts {
		key := fmt.Sprintf("%d\x00%s\x00%s", c.Type, c.Location.String(), c.GrammarSnippet)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out.Conflicts = append(out.Conflicts, c)
	}
	return out
}

func (r *AnalysisReport) collect(pred func(Conflict) bool) []Conflict {
	if r == nil {
		return nil
	}
	var out []Conflict
	for _, c := range r.Conflicts {
		if pred(c) {
			out = append(out, c)
		}
	}
	return out
}

// AnalysisOption adjusts AnalyzeWithOptions.
type AnalysisOption func(*analysisConfig)

type analysisConfig struct {
	suppress map[ConflictType]bool
}

// SuppressConflictType drops conflicts of type t from the analysis report.
// It has no effect on StrictMode.
func SuppressConflictType(t ConflictType) AnalysisOption {
	return func(cfg *analysisConfig) {
		if cfg.suppress == nil {
			cfg.suppress = map[ConflictType]bool{}
		}
		cfg.suppress[t] = true
	}
}

// Analyze statically checks the parser grammar for ambiguity.
func (p *Parser[G]) Analyze() (*AnalysisReport, error) {
	return p.AnalyzeWithOptions()
}

// AnalyzeWithOptions statically checks the parser grammar for ambiguity.
func (p *Parser[G]) AnalyzeWithOptions(opts ...AnalysisOption) (*AnalysisReport, error) {
	cfg := &analysisConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	root := p.grammarRoot()
	if root == nil {
		return nil, fmt.Errorf("analyze: parser has no grammar root")
	}
	report, err := analyzeRoot(root)
	if err != nil {
		return nil, err
	}
	if len(cfg.suppress) == 0 {
		return report, nil
	}
	return report.FilterWith(func(c Conflict) bool {
		return !cfg.suppress[c.Type]
	}), nil
}

func init() {
	runStrictModeCheck = func(root node) error {
		if root == nil {
			return fmt.Errorf("analyze: parser has no grammar root")
		}
		report, err := analyzeRoot(root)
		if err != nil {
			return err
		}
		if report.IsClean() {
			return nil
		}
		// Warnings fail the build too. SuppressConflictType does not apply here.
		return fmt.Errorf("grammar conflict: %s", report.Summary())
	}
}

func (p *Parser[G]) grammarRoot() node {
	if p == nil {
		return nil
	}
	if n := p.typeNodes[p.rootType]; n != nil {
		return n
	}
	if p.rootType != nil && p.rootType.Kind() == reflect.Ptr {
		return p.typeNodes[p.rootType.Elem()]
	}
	return nil
}

type atomKind int

const (
	atomLiteral atomKind = iota
	atomToken
	atomOpaque
)

// atom is one terminal that can appear in a FIRST or FOLLOW set.
// Literals and token-type references never overlap with each other.
type atom struct {
	kind    atomKind
	val     string
	typ     lexer.TokenType
	hasType bool
	id      string
}

func (a atom) overlaps(b atom) bool {
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case atomLiteral:
		if a.val != b.val {
			return false
		}
		if a.hasType && b.hasType && a.typ != b.typ {
			return false
		}
		return true
	case atomToken:
		return a.typ == b.typ
	case atomOpaque:
		return a.id == b.id
	default:
		return false
	}
}

func (a atom) example() string {
	switch a.kind {
	case atomLiteral:
		if a.val == "" {
			return `""`
		}
		return a.val
	case atomToken:
		if a.val != "" {
			return a.val
		}
		return "token"
	default:
		if a.id != "" {
			return a.id
		}
		return "token"
	}
}

type nodeFacts struct {
	nullable bool
	first    map[atom]struct{}
}

func analyzeRoot(root node) (*AnalysisReport, error) {
	live := map[node]struct{}{}
	collectLive(root, live, map[node]bool{})
	if len(live) == 0 {
		return &AnalysisReport{}, nil
	}
	facts, err := computeFacts(live)
	if err != nil {
		return nil, err
	}
	follow, err := computeFollow(live, facts)
	if err != nil {
		return nil, err
	}
	var conflicts []Conflict
	reportWalk(root, locCtx{}, facts, follow, map[node]bool{}, &conflicts)
	return &AnalysisReport{Conflicts: conflicts}, nil
}

func collectLive(n node, live map[node]struct{}, stack map[node]bool) {
	if n == nil || stack[n] {
		return
	}
	if _, ok := live[n]; ok {
		return
	}
	stack[n] = true
	live[n] = struct{}{}
	switch n.(type) {
	case *lookaheadGroup, *negation:
		// Detection is suppressed in these subtrees, and lookahead contributes no terminals.
	default:
		for _, c := range nodeChildren(n) {
			collectLive(c, live, stack)
		}
	}
	delete(stack, n)
}

func nodeChildren(n node) []node {
	switch n := n.(type) {
	case *disjunction:
		return append([]node(nil), n.nodes...)
	case *union:
		return []node{&n.disjunction}
	case *strct:
		if n.expr == nil {
			return nil
		}
		return []node{n.expr}
	case *sequence:
		var out []node
		if n.node != nil {
			out = append(out, n.node)
		}
		if n.next != nil {
			out = append(out, n.next)
		}
		return out
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
	default:
		return nil
	}
}

func computeFacts(live map[node]struct{}) (map[node]nodeFacts, error) {
	facts := make(map[node]nodeFacts, len(live))
	for n := range live {
		facts[n] = nodeFacts{}
	}
	// Finite lattice: nullable only flips false→true and FIRST sets only grow.
	for i := 0; i < len(live)*len(live)+2; i++ {
		changed := false
		for n := range live {
			next := evalFacts(n, facts)
			cur := facts[n]
			if next.nullable != cur.nullable || !atomsEqual(cur.first, next.first) {
				facts[n] = next
				changed = true
			}
		}
		if !changed {
			return facts, nil
		}
	}
	return nil, fmt.Errorf("analyze: FIRST/nullable computation did not converge")
}

func evalFacts(n node, cur map[node]nodeFacts) nodeFacts {
	switch n := n.(type) {
	case *literal:
		a := atom{kind: atomLiteral, val: n.s}
		// Unconstrained literals store lexer.EOF (-1) as their type.
		if n.t != lexer.EOF {
			a.hasType = true
			a.typ = n.t
		}
		return nodeFacts{first: atomSet(a)}
	case *reference:
		return nodeFacts{first: atomSet(atom{kind: atomToken, typ: n.typ, val: n.identifier})}
	case *negation:
		return nodeFacts{first: atomSet(atom{kind: atomOpaque, id: fmt.Sprintf("neg:%p", n)})}
	case *custom:
		return nodeFacts{first: atomSet(atom{kind: atomOpaque, id: "custom:" + n.typ.String()})}
	case *parseable:
		return nodeFacts{first: atomSet(atom{kind: atomOpaque, id: "parseable:" + n.t.String()})}
	case *lookaheadGroup:
		return nodeFacts{nullable: true}
	case *capture:
		return cur[n.node]
	case *strct:
		return cur[n.expr]
	case *union:
		return cur[&n.disjunction]
	case *group:
		child := cur[n.expr]
		f := cloneAtoms(child.first)
		nullable := child.nullable
		switch n.mode {
		case groupMatchZeroOrOne, groupMatchZeroOrMore:
			nullable = true
		case groupMatchOneOrMore:
		case groupMatchNonEmpty:
			nullable = false
		case groupMatchOnce:
		default:
		}
		return nodeFacts{nullable: nullable, first: f}
	case *sequence:
		f := map[atom]struct{}{}
		for s := n; s != nil; s = s.next {
			child := cur[s.node]
			addAtoms(f, child.first)
			if !child.nullable {
				return nodeFacts{nullable: false, first: f}
			}
		}
		return nodeFacts{nullable: true, first: f}
	case *disjunction:
		f := map[atom]struct{}{}
		nullable := false
		for _, alt := range n.nodes {
			child := cur[alt]
			addAtoms(f, child.first)
			if child.nullable {
				nullable = true
			}
		}
		return nodeFacts{nullable: nullable, first: f}
	default:
		return nodeFacts{}
	}
}

func computeFollow(live map[node]struct{}, facts map[node]nodeFacts) (map[node]map[atom]struct{}, error) {
	follow := make(map[node]map[atom]struct{}, len(live))
	for n := range live {
		follow[n] = map[atom]struct{}{}
	}
	for i := 0; i < len(live)*len(live)+2; i++ {
		changed := false
		for n := range live {
			if propagateFollow(n, facts, follow) {
				changed = true
			}
		}
		if !changed {
			return follow, nil
		}
	}
	return nil, fmt.Errorf("analyze: FOLLOW computation did not converge")
}

func propagateFollow(n node, facts map[node]nodeFacts, follow map[node]map[atom]struct{}) bool {
	switch n := n.(type) {
	case *lookaheadGroup, *negation:
		return false
	case *strct:
		return addFollow(follow, n.expr, follow[n])
	case *capture:
		return addFollow(follow, n.node, follow[n])
	case *union:
		return addFollow(follow, &n.disjunction, follow[n])
	case *disjunction:
		changed := false
		for _, alt := range n.nodes {
			if addFollow(follow, alt, follow[n]) {
				changed = true
			}
		}
		return changed
	case *group:
		childFollow := follow[n]
		if n.mode == groupMatchZeroOrMore || n.mode == groupMatchOneOrMore {
			childFollow = unionAtoms(childFollow, facts[n.expr].first)
		}
		return addFollow(follow, n.expr, childFollow)
	case *sequence:
		changed := false
		if n.next != nil && addFollow(follow, n.next, follow[n]) {
			changed = true
		}
		// FOLLOW flows from the right: a node is followed by the FIRST of what comes after it.
		elements := sequenceElements(n)
		acc := cloneAtoms(follow[n])
		for i := len(elements) - 1; i >= 0; i-- {
			if addFollow(follow, elements[i], acc) {
				changed = true
			}
			child := facts[elements[i]]
			if child.nullable {
				acc = unionAtoms(acc, child.first)
			} else {
				acc = cloneAtoms(child.first)
			}
		}
		return changed
	default:
		return false
	}
}

func addFollow(follow map[node]map[atom]struct{}, n node, extra map[atom]struct{}) bool {
	if n == nil || len(extra) == 0 {
		return false
	}
	dst, ok := follow[n]
	if !ok {
		return false
	}
	changed := false
	for a := range extra {
		if _, exists := dst[a]; exists {
			continue
		}
		dst[a] = struct{}{}
		changed = true
	}
	return changed
}

type locCtx struct {
	typeName  string
	fieldName string
}

func (c locCtx) location(field string) ConflictLocation {
	name := c.typeName
	if name == "" {
		name = "grammar"
	}
	if field == "" {
		field = c.fieldName
	}
	return ConflictLocation{TypeName: name, FieldName: field}
}

func reportWalk(n node, ctx locCtx, facts map[node]nodeFacts, follow map[node]map[atom]struct{}, seen map[node]bool, out *[]Conflict) {
	if n == nil || seen[n] {
		return
	}
	seen[n] = true
	switch n := n.(type) {
	case *lookaheadGroup, *negation:
		return
	case *strct:
		ctx.typeName = goTypeName(n.typ)
		ctx.fieldName = ""
		reportWalk(n.expr, ctx, facts, follow, seen, out)
	case *union:
		ctx.typeName = goTypeName(n.typ)
		ctx.fieldName = ""
		reportWalk(&n.disjunction, ctx, facts, follow, seen, out)
	case *capture:
		ctx.fieldName = n.field.Name
		reportWalk(n.node, ctx, facts, follow, seen, out)
	case *disjunction:
		reportChoice(n.nodes, ctx, facts, out)
		for _, alt := range n.nodes {
			reportWalk(alt, ctx, facts, follow, seen, out)
		}
	case *sequence:
		for s := n; s != nil; s = s.next {
			reportWalk(s.node, ctx, facts, follow, seen, out)
			if s.next != nil {
				seen[s.next] = true
			}
		}
	case *group:
		reportGroup(n, ctx, facts, follow, out)
		reportWalk(n.expr, ctx, facts, follow, seen, out)
	default:
		for _, c := range nodeChildren(n) {
			reportWalk(c, ctx, facts, follow, seen, out)
		}
	}
}

func reportChoice(alts []node, ctx locCtx, facts map[node]nodeFacts, out *[]Conflict) {
	reportedFF := map[int]bool{}
	reportedUR := map[int]bool{}
	for j := 1; j < len(alts); j++ {
		fj := facts[alts[j]].first
		snippetJ := ebnfFragment(alts[j])
		for i := 0; i < j; i++ {
			fi := facts[alts[i]].first
			if !reportedFF[j] && atomsOverlap(fi, fj) {
				reportedFF[j] = true
				overlap := overlapAtoms(fi, fj)
				example := exampleOf(overlap)
				snippet := choiceSnippet(alts)
				*out = append(*out, makeConflict(Conflict{
					Type:           ConflictFirstFirst,
					Severity:       SeverityWarning,
					Message:        fmt.Sprintf("alternatives share overlapping first tokens %s", example),
					Location:       ctx.location(leadingField(alts[j])),
					GrammarSnippet: snippet,
					Example:        example,
					Suggestion:     "Reorder the alternatives or give them distinct literal prefixes so the first tokens do not overlap",
				}))
			}
			if !reportedUR[j] && atomsEqual(fi, fj) && snippetJ == ebnfFragment(alts[i]) {
				reportedUR[j] = true
				example := exampleOf(atomsList(fj))
				*out = append(*out, makeConflict(Conflict{
					Type:           ConflictUnreachable,
					Severity:       SeverityError,
					Message:        fmt.Sprintf("alternative is shadowed by an earlier alternative with the same first tokens and the same form %s", snippetJ),
					Location:       ctx.location(leadingField(alts[j])),
					GrammarSnippet: choiceSnippet(alts),
					Example:        example,
					Suggestion:     "Remove the shadowed alternative or change its leading tokens so it can be reached",
				}))
			}
		}
	}
}

func reportGroup(n *group, ctx locCtx, facts map[node]nodeFacts, follow map[node]map[atom]struct{}, out *[]Conflict) {
	switch n.mode {
	case groupMatchZeroOrOne, groupMatchZeroOrMore, groupMatchOneOrMore:
	default:
		return
	}
	overlap := overlapAtoms(facts[n].first, follow[n])
	if len(overlap) == 0 {
		return
	}
	example := exampleOf(overlap)
	kind := "optional"
	switch n.mode {
	case groupMatchZeroOrMore:
		kind = "repeated"
	case groupMatchOneOrMore:
		kind = "repeated"
	case groupMatchZeroOrOne, groupMatchOnce, groupMatchNonEmpty:
	}
	*out = append(*out, makeConflict(Conflict{
		Type:           ConflictFirstFollow,
		Severity:       SeverityWarning,
		Message:        fmt.Sprintf("%s group overlaps its follow set on %s", kind, example),
		Location:       ctx.location(leadingField(n)),
		GrammarSnippet: displaySnippet(n),
		Example:        example,
		Suggestion:     "Anchor the group with a trailing delimiter outside its first set, or make the following construct use a different token",
	}))
}

func makeConflict(c Conflict) Conflict {
	if strings.TrimSpace(c.Message) == "" {
		c.Message = "grammar conflict detected"
	}
	c.GrammarSnippet = ensureSnippet(c.GrammarSnippet)
	if strings.TrimSpace(c.Example) == "" {
		c.Example = "token"
	}
	if len(strings.Fields(c.Suggestion)) < 2 {
		c.Suggestion = "Rewrite the grammar so the conflicting constructs are distinguishable"
	}
	if c.Location.TypeName == "" {
		c.Location.TypeName = "grammar"
	}
	return c
}

func ensureSnippet(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		s = "rule"
	}
	if len(s) < 4 {
		s = "(" + s + ")"
	}
	if len(s) < 4 {
		s += " ..."
	}
	return s
}

func choiceSnippet(alts []node) string {
	parts := make([]string, 0, len(alts))
	for _, alt := range alts {
		parts = append(parts, displaySnippet(alt))
	}
	return ensureSnippet(strings.Join(parts, " | "))
}

func displaySnippet(n node) string {
	if n == nil {
		return "rule"
	}
	if s, ok := n.(*strct); ok {
		return ensureSnippet(goTypeName(s.typ))
	}
	s := ebnfFragment(n)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return ensureSnippet(s)
}

func ebnfFragment(n node) string {
	if n == nil {
		return ""
	}
	if s, ok := n.(*strct); ok {
		return goTypeName(s.typ)
	}
	return ebnf(n)
}

func sequenceElements(s *sequence) []node {
	var out []node
	for n := s; n != nil; n = n.next {
		out = append(out, n.node)
	}
	return out
}

func leadingField(n node) string {
	if n == nil {
		return ""
	}
	switch n := n.(type) {
	case *capture:
		return n.field.Name
	case *group:
		if n == nil || n.expr == nil {
			return ""
		}
		return leadingField(n.expr)
	case *sequence:
		if n == nil {
			return ""
		}
		if name := leadingField(n.node); name != "" {
			return name
		}
		if n.next == nil {
			return ""
		}
		return leadingField(n.next)
	case *disjunction:
		if n == nil {
			return ""
		}
		for _, c := range n.nodes {
			if name := leadingField(c); name != "" {
				return name
			}
		}
		return ""
	case *negation:
		if n == nil || n.node == nil {
			return ""
		}
		return leadingField(n.node)
	default:
		return ""
	}
}

func goTypeName(t reflect.Type) string {
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

func atomSet(a atom) map[atom]struct{} {
	return map[atom]struct{}{a: {}}
}

func cloneAtoms(in map[atom]struct{}) map[atom]struct{} {
	out := make(map[atom]struct{}, len(in))
	for a := range in {
		out[a] = struct{}{}
	}
	return out
}

func addAtoms(dst, src map[atom]struct{}) {
	for a := range src {
		dst[a] = struct{}{}
	}
}

func unionAtoms(a, b map[atom]struct{}) map[atom]struct{} {
	out := cloneAtoms(a)
	addAtoms(out, b)
	return out
}

func atomsEqual(a, b map[atom]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for x := range a {
		if _, ok := b[x]; !ok {
			return false
		}
	}
	return true
}

func atomsOverlap(a, b map[atom]struct{}) bool {
	for x := range a {
		for y := range b {
			if x.overlaps(y) {
				return true
			}
		}
	}
	return false
}

func overlapAtoms(a, b map[atom]struct{}) []atom {
	var out []atom
	seen := map[atom]struct{}{}
	for x := range a {
		for y := range b {
			if !x.overlaps(y) {
				continue
			}
			if _, ok := seen[x]; ok {
				continue
			}
			seen[x] = struct{}{}
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].example() < out[j].example() })
	return out
}

func atomsList(set map[atom]struct{}) []atom {
	out := make([]atom, 0, len(set))
	for a := range set {
		if a.kind == atomOpaque {
			continue
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].example() < out[j].example() })
	return out
}

func exampleOf(atoms []atom) string {
	if len(atoms) == 0 {
		return "token"
	}
	parts := make([]string, 0, len(atoms))
	seen := map[string]struct{}{}
	for _, a := range atoms {
		ex := a.example()
		if ex == "" {
			continue
		}
		if _, ok := seen[ex]; ok {
			continue
		}
		seen[ex] = struct{}{}
		parts = append(parts, ex)
	}
	if len(parts) == 0 {
		return "token"
	}
	return strings.Join(parts, " ")
}
