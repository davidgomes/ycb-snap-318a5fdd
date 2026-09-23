//go:build analyze

package participle_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/alecthomas/assert/v2"
	"github.com/alecthomas/participle/v2"
	"github.com/alecthomas/participle/v2/lexer"
)

func analyze[G any](t *testing.T, options ...participle.Option) *participle.AnalysisReport {
	t.Helper()
	parser := mustTestParser[G](t, options...)
	report, err := parser.Analyze()
	assert.NoError(t, err)
	for _, c := range report.Conflicts {
		assert.NotEqual(t, "", c.Message, "%s", c)
		assert.NotEqual(t, "", c.Location.TypeName, "%s", c)
		assert.True(t, utf8.RuneCountInString(c.GrammarSnippet) >= 4, "%s", c)
		assert.NotEqual(t, "", c.Example, "%s", c)
		assert.True(t, strings.Contains(strings.TrimSpace(c.Suggestion), " "), "%s", c)
	}
	return report
}

func onlyConflict(t *testing.T, report *participle.AnalysisReport, typ participle.ConflictType) participle.Conflict {
	t.Helper()
	conflicts := report.FilterByType(typ).Conflicts
	assert.Equal(t, 1, len(conflicts), "%s", report)
	return conflicts[0]
}

func TestAnalyzeFirstFirst(t *testing.T) {
	type grammar struct {
		Name  string `  "let" @Ident`
		Value string `| @Ident "=" @Int`
		Other string `| @Ident`
	}
	report := analyze[grammar](t)
	assert.False(t, report.HasType(participle.ConflictFirstFollow), "%s", report)
	assert.False(t, report.HasType(participle.ConflictUnreachable), "%s", report)
	conflict := onlyConflict(t, report, participle.ConflictFirstFirst)
	assert.Equal(t, participle.SeverityWarning, conflict.Severity)
	assert.Equal(t, participle.ConflictLocation{TypeName: "grammar", FieldName: "Other"}, conflict.Location)
	assert.Equal(t, `<ident> "=" <int> | <ident>`, conflict.GrammarSnippet)
	assert.Equal(t, "foo", conflict.Example)
	assert.Contains(t, conflict.Message, "<ident>")
}

func TestAnalyzeDuplicateAlternatives(t *testing.T) {
	type grammar struct {
		Value string `@Ident | @Ident`
	}
	report := analyze[grammar](t)
	firstFirst := onlyConflict(t, report, participle.ConflictFirstFirst)
	assert.Equal(t, "grammar.Value", firstFirst.Location.String())
	assert.Equal(t, "<ident> | <ident>", firstFirst.GrammarSnippet)

	unreachable := onlyConflict(t, report, participle.ConflictUnreachable)
	assert.Equal(t, participle.SeverityError, unreachable.Severity)
	assert.Equal(t, "grammar.Value", unreachable.Location.String())
	assert.Equal(t, []participle.Conflict{unreachable}, report.Errors())
	assert.Equal(t, []participle.Conflict{firstFirst}, report.Warnings())
}

func TestAnalyzeDistinctLiteralsAreClean(t *testing.T) {
	type grammar struct {
		If    string `  "if" @Ident`
		While string `| "while" @Ident`
	}
	report := analyze[grammar](t)
	assert.True(t, report.IsClean(), "%s", report)
	assert.Equal(t, "no conflicts detected", report.Summary())
}

func TestAnalyzeKeywordAndTokenTypeAreDistinct(t *testing.T) {
	type grammar struct {
		Keyword string `  @"keyword"`
		Ident   string `| @Ident`
	}
	report := analyze[grammar](t)
	assert.True(t, report.IsClean(), "%s", report)
}

func TestAnalyzeTypedLiterals(t *testing.T) {
	type grammar struct {
		A string `  @"x":Ident "a"`
		B string `| @"x":String "b"`
		C string `| @"x" "c"`
	}
	report := analyze[grammar](t)
	assert.Equal(t, 2, report.ConflictCount(participle.ConflictFirstFirst), "%s", report)
	for _, c := range report.Conflicts {
		assert.Equal(t, "grammar.C", c.Location.String())
	}
}

func TestAnalyzeCaseInsensitiveLiterals(t *testing.T) {
	type grammar struct {
		Upper string `  @"SELECT" @Ident`
		Lower string `| @"select" @Int`
	}
	report := analyze[grammar](t)
	assert.True(t, report.IsClean(), "%s", report)

	report = analyze[grammar](t, participle.CaseInsensitive("Ident"))
	conflict := onlyConflict(t, report, participle.ConflictFirstFirst)
	assert.Equal(t, "grammar.Lower", conflict.Location.String())
}

func TestAnalyzeFirstFollow(t *testing.T) {
	type optional struct {
		Optional string `"let" @Ident?`
		Required string `@Ident`
	}
	type zeroOrMore struct {
		Many []string `@Ident*`
		Last string   `@Ident`
	}
	type oneOrMore struct {
		Many []string `@Ident+`
		Last string   `@Ident`
	}
	for _, test := range []struct {
		name     string
		analyze  func(t *testing.T, options ...participle.Option) *participle.AnalysisReport
		location string
		snippet  string
		example  string
	}{
		{"Optional", analyze[optional], "optional.Optional", `<ident>? <ident>`, "let foo"},
		{"ZeroOrMore", analyze[zeroOrMore], "zeroOrMore.Many", `<ident>* <ident>`, "foo"},
		{"OneOrMore", analyze[oneOrMore], "oneOrMore.Many", `<ident>+ <ident>`, "foo"},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := test.analyze(t)
			assert.Equal(t, 1, len(report.Conflicts), "%s", report)
			conflict := onlyConflict(t, report, participle.ConflictFirstFollow)
			assert.Equal(t, participle.SeverityWarning, conflict.Severity)
			assert.Equal(t, test.location, conflict.Location.String())
			assert.Equal(t, test.snippet, conflict.GrammarSnippet)
			assert.Equal(t, test.example, conflict.Example)
		})
	}
}

func TestAnalyzeFirstFollowCleanWhenDelimited(t *testing.T) {
	type list struct {
		Items []string `"(" (@Ident ("," @Ident)*)? ")"`
	}
	type grammar struct {
		Lists []*list `@@*`
		Name  string  `@Ident?`
	}
	report := analyze[grammar](t)
	assert.True(t, report.IsClean(), "%s", report)
}

func TestAnalyzeFirstFollowAcrossEmbeddedStruct(t *testing.T) {
	type inner struct {
		Name string `@Ident?`
	}
	type grammar struct {
		Inner *inner `@@`
		Next  string `@Ident`
	}
	report := analyze[grammar](t)
	conflict := onlyConflict(t, report, participle.ConflictFirstFollow)
	assert.Equal(t, participle.ConflictLocation{TypeName: "inner", FieldName: "Name"}, conflict.Location)
	assert.Equal(t, 1, len(report.Conflicts), "%s", report)
}

func TestAnalyzeFirstFollowThroughNullableEmbeddedStruct(t *testing.T) {
	type empty struct {
		Flag bool `@"flag"?`
	}
	type grammar struct {
		Name  string `@Ident?`
		Empty *empty `@@`
		Next  string `@Ident`
	}
	report := analyze[grammar](t)
	conflict := onlyConflict(t, report, participle.ConflictFirstFollow)
	assert.Equal(t, "grammar.Name", conflict.Location.String())
	assert.Equal(t, "<ident>? Empty <ident>", conflict.GrammarSnippet)
}

func TestAnalyzeNestedLocation(t *testing.T) {
	type value struct {
		Str string `  @Ident`
		Num string `| @Ident`
	}
	type entry struct {
		Key   string `@Ident "="`
		Value *value `@@`
	}
	type grammar struct {
		Entries []*entry `@@*`
	}
	report := analyze[grammar](t)
	assert.Equal(t, 2, len(report.Conflicts), "%s", report)
	for _, c := range report.Conflicts {
		assert.Equal(t, "value", c.Location.TypeName)
		assert.Equal(t, "Num", c.Location.FieldName)
	}
}

func TestAnalyzeRecursiveGrammarIsClean(t *testing.T) {
	report := analyze[analyzeExpr](t)
	assert.True(t, report.IsClean(), "%s", report)
}

type analyzeExpr struct {
	Left  *analyzeTerm `@@`
	Op    string       `( @("+" | "-")`
	Right *analyzeExpr `  @@ )?`
}

type analyzeTerm struct {
	Number *float64       `(  @(Int | Float)`
	Ident  string         ` | @Ident`
	Sub    *analyzeExpr   ` | "(" @@ ")" )`
	Calls  []*analyzeCall `@@*`
}

type analyzeCall struct {
	Args []*analyzeExpr `"." "(" (@@ ("," @@)*)? ")"`
}

func TestAnalyzeUnion(t *testing.T) {
	report := analyze[analyzeUnionGrammar](t, participle.Union[analyzeUnionValue](analyzeUnionIdent{}, analyzeUnionAssign{}))
	conflict := onlyConflict(t, report, participle.ConflictFirstFirst)
	assert.Equal(t, "analyzeUnionValue", conflict.Location.String())
	assert.Equal(t, "AnalyzeUnionIdent | AnalyzeUnionAssign", conflict.GrammarSnippet)
}

type analyzeUnionValue interface{ value() }

type analyzeUnionIdent struct {
	Name string `@Ident`
}

func (analyzeUnionIdent) value() {}

type analyzeUnionAssign struct {
	Name  string `@Ident "="`
	Value int    `@Int`
}

func (analyzeUnionAssign) value() {}

type analyzeUnionGrammar struct {
	Values []analyzeUnionValue `@@*`
}

func TestAnalyzeLookaheadSuppressesConflicts(t *testing.T) {
	type insideLookahead struct {
		Value string `(?= ("a" | "a") | @Ident? @Ident) @Ident`
	}
	type guardedAlternative struct {
		Assign string `  (?= Ident "=") @Ident "=" Int`
		Ident  string `| @Ident`
	}
	type guardedOptional struct {
		Name string `((?! Ident "=") @Ident)?`
		Rest string `@Ident "=" @Int`
	}
	assert.True(t, analyze[insideLookahead](t).IsClean())
	assert.True(t, analyze[guardedAlternative](t).IsClean())
	assert.True(t, analyze[guardedOptional](t).IsClean())
}

func TestAnalyzeNegationProducesNoConflicts(t *testing.T) {
	type grammar struct {
		Tokens []string `(@~";")* ";"`
		Either string   `(@~"x" | @~"x")`
		Last   string   `@Ident`
	}
	report := analyze[grammar](t)
	assert.True(t, report.IsClean(), "%s", report)
}

func TestAnalyzeCustomAndParseableAreOpaque(t *testing.T) {
	type grammar struct {
		Custom []customAnalyzeType `@@*`
		Name   string              `@Ident?`
	}
	parser := mustTestParser[grammar](t, participle.ParseTypeWith(func(lex *lexer.PeekingLexer) (customAnalyzeType, error) {
		return nil, participle.NextMatch
	}))
	report, err := parser.Analyze()
	assert.NoError(t, err)
	assert.True(t, report.IsClean(), "%s", report)
}

type customAnalyzeType interface{ custom() }

func TestAnalyzeWithSuppressConflictType(t *testing.T) {
	type grammar struct {
		Value string   `(@Ident | @Ident)`
		Many  []string `@Int* @Int`
	}
	parser := mustTestParser[grammar](t)
	report, err := parser.Analyze()
	assert.NoError(t, err)
	assert.Equal(t, "3 conflict(s): 1 first/first, 1 first/follow, 1 unreachable", report.Summary())

	report, err = parser.AnalyzeWithOptions(
		participle.SuppressConflictType(participle.ConflictFirstFirst),
		participle.SuppressConflictType(participle.ConflictUnreachable),
	)
	assert.NoError(t, err)
	assert.Equal(t, "1 conflict(s): 0 first/first, 1 first/follow, 0 unreachable", report.Summary())
}

func TestStrictMode(t *testing.T) {
	type clean struct {
		Key   string `@Ident "="`
		Value int    `@Int`
	}
	type warningOnly struct {
		Name string `@Ident? @Ident`
	}
	type unreachable struct {
		Value string `@Ident | @Ident`
	}

	p, err := participle.Build[clean](participle.StrictMode())
	assert.NoError(t, err)
	actual, err := p.ParseString("", `a = 1`)
	assert.NoError(t, err)
	assert.Equal(t, &clean{Key: "a", Value: 1}, actual)

	warn, err := participle.Build[warningOnly](participle.StrictMode())
	assert.Zero(t, warn)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "conflict")
	assert.Contains(t, err.Error(), "first/follow")

	unreach, err := participle.Build[unreachable](participle.StrictMode())
	assert.Zero(t, unreach)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unreachable")

	_, err = participle.Build[unreachable]()
	assert.NoError(t, err)
}

func TestConflictStrings(t *testing.T) {
	assert.Equal(t, "first/first", participle.ConflictFirstFirst.String())
	assert.Equal(t, "first/follow", participle.ConflictFirstFollow.String())
	assert.Equal(t, "unreachable", participle.ConflictUnreachable.String())
	assert.Equal(t, "warning", participle.SeverityWarning.String())
	assert.Equal(t, "error", participle.SeverityError.String())
	assert.Equal(t, "Grammar", participle.ConflictLocation{TypeName: "Grammar"}.String())
	assert.Equal(t, "Grammar.Field", participle.ConflictLocation{TypeName: "Grammar", FieldName: "Field"}.String())

	conflict := participle.Conflict{
		Type:     participle.ConflictFirstFollow,
		Severity: participle.SeverityWarning,
		Message:  "something is ambiguous",
		Location: participle.ConflictLocation{TypeName: "Grammar", FieldName: "Field"},
	}
	assert.Equal(t, "[warning] first/follow at Grammar.Field: something is ambiguous", conflict.String())
}

func testConflict(typ participle.ConflictType, severity participle.Severity, field, snippet string) participle.Conflict {
	return participle.Conflict{
		Type:           typ,
		Severity:       severity,
		Message:        "message for " + snippet,
		Location:       participle.ConflictLocation{TypeName: "Grammar", FieldName: field},
		GrammarSnippet: snippet,
		Example:        "foo",
		Suggestion:     "fix the grammar",
	}
}

func TestAnalysisReportMethods(t *testing.T) {
	a := testConflict(participle.ConflictFirstFirst, participle.SeverityWarning, "A", `"a" | "a"`)
	b := testConflict(participle.ConflictUnreachable, participle.SeverityError, "B", `"b" | "b"`)
	c := testConflict(participle.ConflictFirstFirst, participle.SeverityWarning, "C", `"c" | "c"`)
	report := &participle.AnalysisReport{Conflicts: []participle.Conflict{a, b, c}}

	assert.Equal(t, []participle.Conflict{b}, report.Errors())
	assert.Equal(t, []participle.Conflict{a, c}, report.Warnings())
	assert.Equal(t, []participle.Conflict{a, c}, report.FilterByType(participle.ConflictFirstFirst).Conflicts)
	assert.Equal(t, 0, len(report.FilterByType(participle.ConflictFirstFollow).Conflicts))
	assert.Equal(t, []participle.Conflict{c}, report.FilterWith(func(c participle.Conflict) bool {
		return c.Location.FieldName == "C"
	}).Conflicts)
	assert.Equal(t, 2, report.ConflictCount(participle.ConflictFirstFirst))
	assert.Equal(t, 0, report.ConflictCount(participle.ConflictFirstFollow))
	assert.True(t, report.HasType(participle.ConflictUnreachable))
	assert.False(t, report.HasType(participle.ConflictFirstFollow))
	assert.False(t, report.IsClean())
	assert.Equal(t, "3 conflict(s): 2 first/first, 0 first/follow, 1 unreachable", report.Summary())

	str := report.String()
	assert.True(t, strings.Count(str, "\n") > 3, "%s", str)
	for _, conflict := range report.Conflicts {
		assert.Contains(t, str, conflict.String())
		assert.Contains(t, str, conflict.GrammarSnippet)
	}

	// The receiver is never modified.
	assert.Equal(t, []participle.Conflict{a, b, c}, report.Conflicts)
}

func TestAnalysisReportClean(t *testing.T) {
	for _, report := range []*participle.AnalysisReport{{}, nil} {
		assert.True(t, report.IsClean())
		assert.Equal(t, "no conflicts detected", report.Summary())
		assert.Contains(t, report.String(), "no conflicts detected")
		assert.Equal(t, 0, len(report.Errors()))
		assert.Equal(t, 0, len(report.Warnings()))
		assert.True(t, report.Merge(report).IsClean())
	}
}

func TestAnalysisReportMergeAndDedup(t *testing.T) {
	a := testConflict(participle.ConflictFirstFirst, participle.SeverityWarning, "A", `"a" | "a"`)
	b := testConflict(participle.ConflictUnreachable, participle.SeverityError, "A", `"a" | "a"`)
	c := testConflict(participle.ConflictFirstFollow, participle.SeverityWarning, "C", `"c"? "c"`)
	aDuplicate := a
	aDuplicate.Message = "same type, location and snippet"

	left := &participle.AnalysisReport{Conflicts: []participle.Conflict{a, b}}
	right := &participle.AnalysisReport{Conflicts: []participle.Conflict{aDuplicate, c, c}}
	merged := left.Merge(right)
	assert.Equal(t, []participle.Conflict{a, b, c}, merged.Conflicts)
	assert.Equal(t, []participle.Conflict{a, b}, left.Conflicts)
	assert.Equal(t, []participle.Conflict{aDuplicate, c, c}, right.Conflicts)
	assert.Equal(t, []participle.Conflict{a, b}, left.Merge(nil).Conflicts)

	assert.Equal(t, []participle.Conflict{aDuplicate, c}, right.Dedup().Conflicts)
	assert.Equal(t, []participle.Conflict{aDuplicate, c, c}, right.Conflicts)
}
