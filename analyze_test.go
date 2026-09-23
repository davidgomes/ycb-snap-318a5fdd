//go:build analyze

package participle_test

import (
	"strings"
	"testing"

	require "github.com/alecthomas/assert/v2"
	"github.com/alecthomas/participle/v2"
)

func TestConflictFormatting(t *testing.T) {
	require.Equal(t, "first/first", participle.ConflictFirstFirst.String())
	require.Equal(t, "first/follow", participle.ConflictFirstFollow.String())
	require.Equal(t, "unreachable", participle.ConflictUnreachable.String())
	require.Equal(t, "warning", participle.SeverityWarning.String())
	require.Equal(t, "error", participle.SeverityError.String())

	loc := participle.ConflictLocation{TypeName: "Expr"}
	require.Equal(t, "Expr", loc.String())
	loc.FieldName = "Op"
	require.Equal(t, "Expr.Op", loc.String())

	c := participle.Conflict{
		Type:           participle.ConflictFirstFirst,
		Severity:       participle.SeverityWarning,
		Message:        "alternatives overlap",
		Location:       loc,
		GrammarSnippet: `<ident> | <ident>`,
		Example:        "foo",
		Suggestion:     "rename one alternative",
	}
	require.Equal(t, "[warning] first/first at Expr.Op: alternatives overlap", c.String())
}

func TestAnalysisReportMethods(t *testing.T) {
	mk := func(typ participle.ConflictType, sev participle.Severity, loc, snippet, msg string) participle.Conflict {
		field := ""
		typeName := loc
		if i := strings.IndexByte(loc, '.'); i >= 0 {
			typeName, field = loc[:i], loc[i+1:]
		}
		return participle.Conflict{
			Type:           typ,
			Severity:       sev,
			Message:        msg,
			Location:       participle.ConflictLocation{TypeName: typeName, FieldName: field},
			GrammarSnippet: snippet,
			Example:        "foo",
			Suggestion:     "change the grammar",
		}
	}
	ff1 := mk(participle.ConflictFirstFirst, participle.SeverityWarning, "A.X", "<ident> | <ident>", "first")
	fl := mk(participle.ConflictFirstFollow, participle.SeverityWarning, "B.Y", "<ident>?", "follow")
	ff2 := mk(participle.ConflictFirstFirst, participle.SeverityWarning, "C", `"if" | "if"`, "second")
	ur := mk(participle.ConflictUnreachable, participle.SeverityError, "A.X", "<ident>", "shadow")
	dup := mk(participle.ConflictFirstFirst, participle.SeverityWarning, "A.X", "<ident> | <ident>", "duplicate message")

	report := &participle.AnalysisReport{Conflicts: []participle.Conflict{ff1, fl, ff2, ur}}

	require.Equal(t, "4 conflict(s): 2 first/first, 1 first/follow, 1 unreachable", report.Summary())
	require.False(t, report.IsClean())
	require.Equal(t, 2, report.ConflictCount(participle.ConflictFirstFirst))
	require.Equal(t, 1, report.ConflictCount(participle.ConflictFirstFollow))
	require.Equal(t, 1, report.ConflictCount(participle.ConflictUnreachable))
	require.True(t, report.HasType(participle.ConflictUnreachable))
	require.Equal(t, 1, len(report.Errors()))
	require.Equal(t, ur.Message, report.Errors()[0].Message)
	require.Equal(t, 3, len(report.Warnings()))

	filtered := report.FilterByType(participle.ConflictFirstFirst)
	require.Equal(t, []string{ff1.Message, ff2.Message}, messages(filtered))
	require.Equal(t, 4, len(report.Conflicts))

	onlyFollow := report.FilterWith(func(c participle.Conflict) bool {
		return c.Type == participle.ConflictFirstFollow
	})
	require.Equal(t, []string{fl.Message}, messages(onlyFollow))
	require.Equal(t, 4, len(report.Conflicts))

	clean := report.FilterByType(participle.ConflictType(99))
	require.True(t, clean.IsClean())
	require.Equal(t, "no conflicts detected", clean.Summary())
	require.True(t, strings.Contains(clean.String(), "\n"))
	require.True(t, strings.Contains(clean.String(), "no conflicts detected"))

	text := report.String()
	require.True(t, strings.Contains(text, "\n"))
	for _, c := range report.Conflicts {
		require.True(t, strings.Contains(text, c.Type.String()), text)
		require.True(t, strings.Contains(text, c.Location.String()), text)
	}

	merged := report.Merge(&participle.AnalysisReport{Conflicts: []participle.Conflict{dup, ur}})
	require.Equal(t, 4, len(report.Conflicts))
	require.Equal(t, []string{ff1.Message, fl.Message, ff2.Message, ur.Message}, messages(merged))
	require.Equal(t, "first", merged.Conflicts[0].Message)

	withDup := &participle.AnalysisReport{Conflicts: []participle.Conflict{ff1, dup, fl}}
	deduped := withDup.Dedup()
	require.Equal(t, 3, len(withDup.Conflicts))
	require.Equal(t, []string{ff1.Message, fl.Message}, messages(deduped))

	var empty *participle.AnalysisReport
	require.True(t, empty.IsClean())
	require.Equal(t, "no conflicts detected", empty.Summary())
	require.Equal(t, 0, empty.ConflictCount(participle.ConflictFirstFirst))
	require.False(t, empty.HasType(participle.ConflictFirstFollow))
	require.Equal(t, 0, len(empty.Errors()))
	require.Equal(t, 0, len(empty.Warnings()))
	require.True(t, empty.Merge(report).ConflictCount(participle.ConflictUnreachable) == 1)
	require.True(t, report.Merge(nil).ConflictCount(participle.ConflictFirstFirst) == 2)
}

func messages(r *participle.AnalysisReport) []string {
	out := make([]string, len(r.Conflicts))
	for i, c := range r.Conflicts {
		out[i] = c.Message
	}
	return out
}

func assertConflictShape(t *testing.T, c participle.Conflict) {
	t.Helper()
	require.True(t, c.Message != "")
	require.True(t, len(c.GrammarSnippet) >= 4, c.GrammarSnippet)
	require.True(t, c.Example != "")
	require.True(t, strings.Contains(c.Suggestion, " "), c.Suggestion)
	require.True(t, c.Location.TypeName != "")
	require.True(t, strings.HasPrefix(c.String(), "["+c.Severity.String()+"] "+c.Type.String()+" at "+c.Location.String()+": "))
}

func mustAnalyze[G any](t *testing.T, opts ...participle.AnalysisOption) *participle.AnalysisReport {
	t.Helper()
	p, err := participle.Build[G]()
	require.NoError(t, err)
	r, err := p.AnalyzeWithOptions(opts...)
	require.NoError(t, err)
	require.True(t, r != nil)
	for _, c := range r.Conflicts {
		assertConflictShape(t, c)
	}
	return r
}

func TestFirstFirstIdent(t *testing.T) {
	type choice struct {
		A string `  @Ident`
		B string `| @Ident`
	}
	r := mustAnalyze[choice](t)
	require.True(t, r.HasType(participle.ConflictFirstFirst), r.String())
	require.True(t, r.HasType(participle.ConflictUnreachable), r.String())
	require.Equal(t, participle.SeverityWarning, r.FilterByType(participle.ConflictFirstFirst).Conflicts[0].Severity)
	require.Equal(t, participle.SeverityError, r.FilterByType(participle.ConflictUnreachable).Conflicts[0].Severity)
	for _, c := range r.Conflicts {
		require.Equal(t, "choice", c.Location.TypeName)
	}
	require.Equal(t, "foo", r.FilterByType(participle.ConflictFirstFirst).Conflicts[0].Example)
}

func TestDistinctLiteralsAreClean(t *testing.T) {
	type choice struct {
		A string `"if" | "while"`
	}
	r := mustAnalyze[choice](t)
	require.True(t, r.IsClean(), r.String())
	require.Equal(t, "no conflicts detected", r.Summary())
}

func TestLiteralDoesNotOverlapTokenType(t *testing.T) {
	type choice struct {
		A string `  "keyword"`
		B string `| @Ident`
	}
	r := mustAnalyze[choice](t)
	require.False(t, r.HasType(participle.ConflictFirstFirst), r.String())
	require.True(t, r.IsClean(), r.String())
}

func TestTypedLiteralDoesNotOverlapTokenType(t *testing.T) {
	type choice struct {
		A string `  "keyword":Ident`
		B string `| @Ident`
	}
	r := mustAnalyze[choice](t)
	require.True(t, r.IsClean(), r.String())
}

func TestFirstFollowOptionalStarAndPlus(t *testing.T) {
	type optional struct {
		A string `@Ident?`
		B string `@Ident`
	}
	type star struct {
		A string `@Ident*`
		B string `@Ident`
	}
	type plus struct {
		A string `@Ident+`
		B string `@Ident`
	}
	for _, r := range []*participle.AnalysisReport{
		mustAnalyze[optional](t),
		mustAnalyze[star](t),
		mustAnalyze[plus](t),
	} {
		require.True(t, r.HasType(participle.ConflictFirstFollow), r.String())
		require.Equal(t, 1, r.ConflictCount(participle.ConflictFirstFollow), r.String())
		c := r.Conflicts[0]
		require.Equal(t, participle.SeverityWarning, c.Severity)
		require.True(t, c.Location.TypeName == "optional" || c.Location.TypeName == "star" || c.Location.TypeName == "plus", c.Location.String())
		require.Equal(t, "A", c.Location.FieldName, c.String())
	}
}

func TestPlusWithoutOverlapIsClean(t *testing.T) {
	type grammar struct {
		A string `@Ident+`
		B string `@String`
	}
	r := mustAnalyze[grammar](t)
	require.True(t, r.IsClean(), r.String())
}

func TestEpsilonPropagatesThroughEmbedding(t *testing.T) {
	type n1 struct {
		X string `@String?`
	}
	type n2 struct {
		N *n1 `@@`
	}
	type outer struct {
		A string `@Ident?`
		N *n2    `@@`
		B string `@Ident`
	}
	r := mustAnalyze[outer](t)
	require.True(t, r.HasType(participle.ConflictFirstFollow), r.String())
	found := false
	for _, c := range r.FilterByType(participle.ConflictFirstFollow).Conflicts {
		if c.Location.TypeName == "outer" && c.Location.FieldName == "A" {
			found = true
		}
	}
	require.True(t, found, r.String())
}

func TestNestedStructLocation(t *testing.T) {
	type inner struct {
		X string `@Ident | @Ident`
	}
	type outer struct {
		Inner inner `@@`
	}
	r := mustAnalyze[outer](t)
	require.True(t, r.HasType(participle.ConflictFirstFirst), r.String())
	for _, c := range r.Conflicts {
		require.Equal(t, "inner", c.Location.TypeName, c.String())
	}
}

func TestLookaheadSuppressesSubtree(t *testing.T) {
	type grammar struct {
		A string `(?= @Ident | @Ident) @String`
	}
	r := mustAnalyze[grammar](t)
	require.True(t, r.IsClean(), r.String())
}

func TestNegationProducesNoConflicts(t *testing.T) {
	type dup struct {
		A string `~@Ident | ~@Ident`
	}
	type nested struct {
		A string `~(@Ident? @Ident)`
	}
	require.True(t, mustAnalyze[dup](t).IsClean())
	require.True(t, mustAnalyze[nested](t).IsClean())
}

func TestUnreachableRequiresIdenticalSnippet(t *testing.T) {
	type grammar struct {
		A string `  "if"`
		B string `| "if" @Ident`
	}
	r := mustAnalyze[grammar](t)
	require.True(t, r.HasType(participle.ConflictFirstFirst), r.String())
	require.False(t, r.HasType(participle.ConflictUnreachable), r.String())
}

func TestSuppressConflictType(t *testing.T) {
	type choice struct {
		A string `  @Ident`
		B string `| @Ident`
	}
	all := mustAnalyze[choice](t)
	require.True(t, all.HasType(participle.ConflictFirstFirst))
	require.True(t, all.HasType(participle.ConflictUnreachable))

	r := mustAnalyze[choice](t, participle.SuppressConflictType(participle.ConflictFirstFirst))
	require.False(t, r.HasType(participle.ConflictFirstFirst), r.String())
	require.True(t, r.HasType(participle.ConflictUnreachable), r.String())

	both := mustAnalyze[choice](t,
		participle.SuppressConflictType(participle.ConflictFirstFirst),
		participle.SuppressConflictType(participle.ConflictUnreachable),
	)
	require.True(t, both.IsClean(), both.String())
}

func TestStrictModeRejectsWarningsAndErrors(t *testing.T) {
	type firstFirst struct {
		A string `@Ident | @Ident`
	}
	_, err := participle.Build[firstFirst](participle.StrictMode())
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "conflict"), err.Error())

	type firstFollow struct {
		A string `@Ident?`
		B string `@Ident`
	}
	parser, err := participle.Build[firstFollow](participle.StrictMode())
	require.Error(t, err)
	require.True(t, parser == nil)
	require.True(t, strings.Contains(err.Error(), "conflict"), err.Error())

	type clean struct {
		A string `@"if" | @"while"`
	}
	cleanParser, err := participle.Build[clean](participle.StrictMode())
	require.NoError(t, err)
	got, err := cleanParser.ParseString("", "while")
	require.NoError(t, err)
	require.Equal(t, "while", got.A)

	// SuppressConflictType does not apply to StrictMode.
	_, err = participle.Build[firstFollow](participle.StrictMode())
	require.Error(t, err)
}

func TestAnalyzeLeavesParserUsable(t *testing.T) {
	type choice struct {
		A string `  @Ident`
		B string `| @String`
	}
	p, err := participle.Build[choice]()
	require.NoError(t, err)
	r, err := p.Analyze()
	require.NoError(t, err)
	require.True(t, r.IsClean(), r.String())
	got, err := p.ParseString("", `"bar"`)
	require.NoError(t, err)
	require.Equal(t, "\"bar\"", got.B)
}

func TestAnalyzeComplexGrammarDoesNotPanic(t *testing.T) {
	p, err := participle.Build[EBNF]()
	require.NoError(t, err)
	r, err := p.Analyze()
	require.NoError(t, err)
	require.True(t, r != nil)
	for _, c := range r.Conflicts {
		assertConflictShape(t, c)
	}
	// Lookahead and negation subtrees must not be the only reason a report exists;
	// the call itself must succeed and the report string must stay well-formed.
	require.True(t, strings.Contains(r.String(), "\n"), r.String())
	if r.IsClean() {
		require.Equal(t, "no conflicts detected", r.Summary())
	} else {
		require.True(t, strings.Contains(r.Summary(), "first/first"), r.Summary())
		require.True(t, strings.Contains(r.Summary(), "first/follow"), r.Summary())
		require.True(t, strings.Contains(r.Summary(), "unreachable"), r.Summary())
	}
}

func TestQuantifierSugarAndLonelyGroups(t *testing.T) {
	type bracket struct {
		A string `[ @Ident ]`
		B string `@Ident`
	}
	type braces struct {
		A string `{ @Ident }`
		B string `@Ident`
	}
	type starAlone struct {
		A string `@Ident*`
	}
	type plusAlone struct {
		A string `@Ident+`
	}
	type optionalAlone struct {
		A string `@Ident?`
	}
	type optionalOther struct {
		A string `@Ident?`
		B string `@String`
	}
	br := mustAnalyze[bracket](t)
	require.Equal(t, 1, br.ConflictCount(participle.ConflictFirstFollow), br.String())
	require.Equal(t, "A", br.Conflicts[0].Location.FieldName)

	bz := mustAnalyze[braces](t)
	require.Equal(t, 1, bz.ConflictCount(participle.ConflictFirstFollow), bz.String())

	require.True(t, mustAnalyze[starAlone](t).IsClean(), mustAnalyze[starAlone](t).String())
	require.True(t, mustAnalyze[plusAlone](t).IsClean())
	require.True(t, mustAnalyze[optionalAlone](t).IsClean())
	require.True(t, mustAnalyze[optionalOther](t).IsClean())
}

func TestFollowReachesEmbeddedGroup(t *testing.T) {
	type rep struct {
		A string `@Ident+`
	}
	type wrap struct {
		R rep    `@@`
		B string `@Ident`
	}
	r := mustAnalyze[wrap](t)
	require.Equal(t, 1, r.ConflictCount(participle.ConflictFirstFollow), r.String())
	c := r.Conflicts[0]
	require.Equal(t, "rep", c.Location.TypeName, c.String())
	require.Equal(t, "A", c.Location.FieldName, c.String())
}

type analysisUnion interface{ isAnalysisUnion() }

type analysisUnionA struct {
	V string `@Ident`
}

type analysisUnionB struct {
	V string `@Ident`
}

func (analysisUnionA) isAnalysisUnion() {}
func (analysisUnionB) isAnalysisUnion() {}

func TestUnionMembersFirstFirst(t *testing.T) {
	type grammar struct {
		T analysisUnion `@@`
	}
	p, err := participle.Build[grammar](participle.Union[analysisUnion](analysisUnionA{}, analysisUnionB{}))
	require.NoError(t, err)
	r, err := p.Analyze()
	require.NoError(t, err)
	require.True(t, r.HasType(participle.ConflictFirstFirst), r.String())
	require.False(t, r.HasType(participle.ConflictUnreachable), r.String())
	require.Equal(t, "analysisUnion", r.FilterByType(participle.ConflictFirstFirst).Conflicts[0].Location.TypeName)

	_, err = participle.Build[grammar](participle.StrictMode(), participle.Union[analysisUnion](analysisUnionA{}, analysisUnionB{}))
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "conflict"), err.Error())
}

func TestPartialOverlapIsNotUnreachable(t *testing.T) {
	type grammar struct {
		A string `  @Ident`
		B string `| @Ident @String`
	}
	r := mustAnalyze[grammar](t)
	require.True(t, r.HasType(participle.ConflictFirstFirst), r.String())
	require.False(t, r.HasType(participle.ConflictUnreachable), r.String())
}

func TestSingleFieldDisjunctionUsesFieldName(t *testing.T) {
	type inner struct {
		X string `@Ident | @Ident`
	}
	r := mustAnalyze[inner](t)
	for _, c := range r.Conflicts {
		require.Equal(t, "inner.X", c.Location.String(), c.String())
	}
}

func TestSummaryListsZeroCounts(t *testing.T) {
	type grammar struct {
		A string `@Ident?`
		B string `@Ident`
	}
	r := mustAnalyze[grammar](t)
	require.Equal(t, "1 conflict(s): 0 first/first, 1 first/follow, 0 unreachable", r.Summary())
}
