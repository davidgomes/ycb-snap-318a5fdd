//go:build analyze

package participle_test

import (
	"strings"
	"testing"

	"github.com/alecthomas/assert/v2"

	"github.com/alecthomas/participle/v2"
)

func analyze[G any](t *testing.T, options ...participle.Option) *participle.AnalysisReport {
	t.Helper()
	parser, err := participle.Build[G](options...)
	assert.NoError(t, err)
	report, err := parser.Analyze()
	assert.NoError(t, err)
	checkConflictsComplete(t, report)
	return report
}

func checkConflictsComplete(t *testing.T, report *participle.AnalysisReport) {
	t.Helper()
	for _, c := range report.Conflicts {
		assert.NotEqual(t, "", c.Message, "%s", c)
		assert.NotEqual(t, "", c.Location.TypeName, "%s", c)
		assert.True(t, len(c.GrammarSnippet) >= 4, "%s", c)
		assert.NotEqual(t, "", c.Example, "%s", c)
		assert.True(t, strings.Contains(c.Suggestion, " "), "%s", c)
	}
}

func TestAnalyzeClean(t *testing.T) {
	type Grammar struct {
		Keyword string `  @("if" | "while")`
		Name    string `| @Ident`
	}
	report := analyze[Grammar](t)
	assert.True(t, report.IsClean(), "%s", report)
	assert.Equal(t, "no conflicts detected", report.Summary())
	assert.NotEqual(t, "", report.String())
}

func TestAnalyzeFirstFirst(t *testing.T) {
	type Grammar struct {
		A string `  @Ident`
		B string `| @Ident "x"`
	}
	report := analyze[Grammar](t)
	assert.Equal(t, 1, report.ConflictCount(participle.ConflictFirstFirst), "%s", report)
	assert.False(t, report.HasType(participle.ConflictUnreachable))
	c := report.Conflicts[0]
	assert.Equal(t, participle.SeverityWarning, c.Severity)
	assert.Equal(t, "Grammar", c.Location.TypeName)
	assert.Equal(t, "Grammar.A", c.Location.String())
	assert.Equal(t, "1 conflict(s): 1 first/first, 0 first/follow, 0 unreachable", report.Summary())
}

func TestAnalyzeUnreachable(t *testing.T) {
	type Grammar struct {
		A string `@Ident | @Ident`
	}
	report := analyze[Grammar](t)
	assert.True(t, report.HasType(participle.ConflictFirstFirst), "%s", report)
	assert.True(t, report.HasType(participle.ConflictUnreachable), "%s", report)
	assert.Equal(t, 1, len(report.Errors()))
	assert.Equal(t, 1, len(report.Warnings()))
}

func TestAnalyzeFirstFollow(t *testing.T) {
	type Optional struct {
		A *string `@Ident?`
		B string  `@Ident`
	}
	type Star struct {
		A []string `@Ident*`
		B string   `@Ident`
	}
	type Plus struct {
		A []string `@Ident+`
		B string   `@Ident`
	}
	type NoConflict struct {
		A []string `@Ident*`
		B string   `";"`
	}
	for _, report := range []*participle.AnalysisReport{analyze[Optional](t), analyze[Star](t), analyze[Plus](t)} {
		assert.Equal(t, 1, report.ConflictCount(participle.ConflictFirstFollow), "%s", report)
		assert.Equal(t, "A", report.Conflicts[0].Location.FieldName)
	}
	assert.True(t, analyze[NoConflict](t).IsClean())
}

func TestAnalyzeFirstFollowThroughEmbedding(t *testing.T) {
	type Inner struct {
		A *string `@Ident?`
	}
	type Outer struct {
		Inner *Inner `@@`
		B     string `@Ident`
	}
	report := analyze[Outer](t)
	assert.True(t, report.HasType(participle.ConflictFirstFollow), "%s", report)
	assert.Equal(t, "Inner", report.FilterByType(participle.ConflictFirstFollow).Conflicts[0].Location.TypeName)
}

func TestAnalyzeKeywordVsIdent(t *testing.T) {
	type Grammar struct {
		A string `  @"keyword"`
		B string `| @Ident`
	}
	assert.True(t, analyze[Grammar](t).IsClean())
}

func TestAnalyzeLookaheadAndNegation(t *testing.T) {
	type Grammar struct {
		A string   `(?= @Ident | @Ident)?`
		B []string `@(~";" | ~";")* ";"`
		C string   `@Ident`
	}
	report := analyze[Grammar](t)
	assert.True(t, report.IsClean(), "%s", report)
}

func TestAnalyzeSuppress(t *testing.T) {
	type Grammar struct {
		A *string `@Ident?`
		B string  `@Ident`
	}
	parser := participle.MustBuild[Grammar]()
	report, err := parser.AnalyzeWithOptions(participle.SuppressConflictType(participle.ConflictFirstFollow))
	assert.NoError(t, err)
	assert.True(t, report.IsClean(), "%s", report)
}

func TestStrictMode(t *testing.T) {
	type Bad struct {
		A *string `@Ident?`
		B string  `@Ident`
	}
	_, err := participle.Build[Bad](participle.StrictMode())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "conflict")

	type Good struct {
		A string `@Ident ";"`
	}
	_, err = participle.Build[Good](participle.StrictMode())
	assert.NoError(t, err)
}

func TestAnalysisReportMethods(t *testing.T) {
	a := participle.Conflict{Type: participle.ConflictFirstFirst, Location: participle.ConflictLocation{TypeName: "T"}, GrammarSnippet: "<a> | <a>"}
	b := participle.Conflict{Type: participle.ConflictUnreachable, Severity: participle.SeverityError, Location: participle.ConflictLocation{TypeName: "T", FieldName: "F"}, GrammarSnippet: "<a> | <a>"}
	r1 := &participle.AnalysisReport{Conflicts: []participle.Conflict{a, b, a}}
	r2 := &participle.AnalysisReport{Conflicts: []participle.Conflict{b}}

	assert.Equal(t, 2, len(r1.Dedup().Conflicts))
	assert.Equal(t, 3, len(r1.Conflicts))
	assert.Equal(t, []participle.Conflict{a, b}, r1.Merge(r2).Conflicts)
	assert.Equal(t, 2, r1.FilterByType(participle.ConflictFirstFirst).ConflictCount(participle.ConflictFirstFirst))
	assert.Equal(t, "3 conflict(s): 2 first/first, 0 first/follow, 1 unreachable", r1.Summary())
	assert.Equal(t, "[error] unreachable at T.F: ", b.String())
	assert.Contains(t, r1.String(), "T.F")
	assert.Contains(t, r1.String(), "first/first")
}
