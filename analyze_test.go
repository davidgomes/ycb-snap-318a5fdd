//go:build analyze

package participle_test

import (
	"strings"
	"testing"

	"github.com/alecthomas/assert/v2"

	"github.com/alecthomas/participle/v2"
)

func TestAnalyzeCleanLiterals(t *testing.T) {
	type grammar struct {
		A string `"if" | "while"`
	}
	p := participle.MustBuild[grammar]()
	rep, err := p.Analyze()
	assert.NoError(t, err)
	assert.True(t, rep.IsClean())
	assert.Equal(t, "no conflicts detected", rep.Summary())
	assert.True(t, strings.Contains(rep.String(), "no conflicts detected"))
	assert.True(t, len(rep.String()) > 0)
}

func TestAnalyzeLiteralVsIdent(t *testing.T) {
	type grammar struct {
		A string `"keyword" | @Ident`
	}
	p := participle.MustBuild[grammar]()
	rep, err := p.Analyze()
	assert.NoError(t, err)
	assert.True(t, rep.IsClean(), rep.String())
}

func TestAnalyzeFirstFirst(t *testing.T) {
	type grammar struct {
		A string `@Ident`
		B string `| @Ident`
	}
	p := participle.MustBuild[grammar]()
	rep, err := p.Analyze()
	assert.NoError(t, err)
	assert.True(t, rep.HasType(participle.ConflictFirstFirst))
	assert.True(t, rep.ConflictCount(participle.ConflictFirstFirst) >= 1)
	ff := rep.FilterByType(participle.ConflictFirstFirst)
	assert.Equal(t, len(ff.Conflicts), rep.ConflictCount(participle.ConflictFirstFirst))
	c := ff.Conflicts[0]
	assert.Equal(t, participle.SeverityWarning, c.Severity)
	assert.Equal(t, "warning", c.Severity.String())
	assert.Equal(t, "first/first", c.Type.String())
	assert.Equal(t, "grammar", c.Location.TypeName)
	assert.True(t, len(c.GrammarSnippet) >= 4)
	assert.True(t, c.Example != "")
	assert.True(t, len(strings.Fields(c.Suggestion)) >= 2)
	assert.True(t, c.Message != "")
	assert.True(t, strings.Contains(c.String(), "first/first"))
	assert.True(t, strings.Contains(c.String(), c.Location.String()))
	assert.True(t, strings.Contains(rep.String(), "first/first"))
	assert.True(t, strings.Contains(rep.Summary(), "first/first"))
	assert.True(t, strings.Contains(rep.Summary(), "first/follow"))
	assert.True(t, strings.Contains(rep.Summary(), "unreachable"))
}

func TestAnalyzeUnreachableDistinctSnippet(t *testing.T) {
	type grammar struct {
		A string `@Ident`
		B string `| @Ident @Ident`
	}
	p := participle.MustBuild[grammar]()
	rep, err := p.Analyze()
	assert.NoError(t, err)
	assert.True(t, rep.HasType(participle.ConflictFirstFirst))
	assert.False(t, rep.HasType(participle.ConflictUnreachable))
}

func TestAnalyzeUnreachable(t *testing.T) {
	type grammar struct {
		A string `"if"`
		B string `| "if"`
	}
	p := participle.MustBuild[grammar]()
	rep, err := p.Analyze()
	assert.NoError(t, err)
	assert.True(t, rep.HasType(participle.ConflictUnreachable))
	errs := rep.Errors()
	assert.True(t, len(errs) >= 1)
	assert.Equal(t, participle.SeverityError, errs[0].Severity)
	assert.Equal(t, "error", errs[0].Severity.String())
	assert.Equal(t, "unreachable", participle.ConflictUnreachable.String())
}

func TestAnalyzeFirstFollow(t *testing.T) {
	type grammar struct {
		A string `@"if"?`
		B string `@"if"`
	}
	p := participle.MustBuild[grammar]()
	rep, err := p.Analyze()
	assert.NoError(t, err)
	assert.True(t, rep.HasType(participle.ConflictFirstFollow), rep.String())
	w := rep.Warnings()
	assert.True(t, len(w) >= 1)
	assert.Equal(t, "grammar.A", rep.FilterByType(participle.ConflictFirstFollow).Conflicts[0].Location.String())
}

func TestAnalyzeFirstFollowPlusAndStar(t *testing.T) {
	type plus struct {
		A string `@"if"+`
		B string `@"if"`
	}
	type star struct {
		A string `@"if"*`
		B string `@"if"`
	}
	for _, p := range []*participle.Parser[plus]{participle.MustBuild[plus]()} {
		rep, err := p.Analyze()
		assert.NoError(t, err)
		assert.True(t, rep.HasType(participle.ConflictFirstFollow), rep.String())
	}
	sp := participle.MustBuild[star]()
	rep, err := sp.Analyze()
	assert.NoError(t, err)
	assert.True(t, rep.HasType(participle.ConflictFirstFollow), rep.String())
}

func TestAnalyzeFirstFollowThroughEmbedding(t *testing.T) {
	type nest struct {
		Opt string `@Ident?`
	}
	type top struct {
		N *nest  `@@`
		I string `@Ident`
	}
	p := participle.MustBuild[top]()
	rep, err := p.Analyze()
	assert.NoError(t, err)
	assert.True(t, rep.HasType(participle.ConflictFirstFollow), rep.String())
	c := rep.FilterByType(participle.ConflictFirstFollow).Conflicts[0]
	assert.Equal(t, "nest", c.Location.TypeName)
	assert.Equal(t, "Opt", c.Location.FieldName)
}

func TestAnalyzeLookaheadSuppresses(t *testing.T) {
	type grammar struct {
		A string `(?= @Ident`
		B string `| @Ident ) @Ident`
	}
	p := participle.MustBuild[grammar]()
	rep, err := p.Analyze()
	assert.NoError(t, err)
	assert.True(t, rep.IsClean(), rep.String())
}

func TestAnalyzeNegationProducesNoConflicts(t *testing.T) {
	type grammar struct {
		A string `!@Ident`
		B string `| !@Ident`
	}
	p := participle.MustBuild[grammar]()
	rep, err := p.Analyze()
	assert.NoError(t, err)
	assert.True(t, rep.IsClean(), rep.String())
}

func TestAnalyzeSuppress(t *testing.T) {
	type grammar struct {
		A string `@Ident | @Ident`
	}
	p := participle.MustBuild[grammar]()
	rep, err := p.AnalyzeWithOptions(participle.SuppressConflictType(participle.ConflictFirstFirst))
	assert.NoError(t, err)
	assert.False(t, rep.HasType(participle.ConflictFirstFirst))
}

func TestStrictMode(t *testing.T) {
	type bad struct {
		A string `"if" | "if"`
	}
	_, err := participle.Build[bad](participle.StrictMode())
	assert.Error(t, err)
	assert.True(t, strings.Contains(strings.ToLower(err.Error()), "conflict"))

	type good struct {
		A string `"if" | "while"`
	}
	_, err = participle.Build[good](participle.StrictMode())
	assert.NoError(t, err)
}

func TestReportMethodsDoNotMutate(t *testing.T) {
	base := &participle.AnalysisReport{Conflicts: []participle.Conflict{
		{
			Type: participle.ConflictFirstFirst, Severity: participle.SeverityWarning,
			Message: "overlap", Location: participle.ConflictLocation{TypeName: "T", FieldName: "A"},
			GrammarSnippet: "<ident>", Example: "foo", Suggestion: "Reorder alternatives now",
		},
		{
			Type: participle.ConflictUnreachable, Severity: participle.SeverityError,
			Message: "shadow", Location: participle.ConflictLocation{TypeName: "T", FieldName: "A"},
			GrammarSnippet: "<ident>", Example: "foo", Suggestion: "Remove the duplicate alternative",
		},
		{
			Type: participle.ConflictFirstFirst, Severity: participle.SeverityWarning,
			Message: "overlap again", Location: participle.ConflictLocation{TypeName: "T", FieldName: "B"},
			GrammarSnippet: "<int>", Example: "1", Suggestion: "Reorder alternatives now",
		},
	}}
	dup := &participle.AnalysisReport{Conflicts: []participle.Conflict{base.Conflicts[0]}}
	merged := base.Merge(dup)
	assert.Equal(t, 3, len(base.Conflicts))
	assert.Equal(t, 3, len(merged.Conflicts))
	filtered := base.FilterByType(participle.ConflictFirstFirst)
	assert.Equal(t, 2, len(filtered.Conflicts))
	assert.Equal(t, "T.A", filtered.Conflicts[0].Location.String())
	assert.Equal(t, "T.B", filtered.Conflicts[1].Location.String())
	onlyB := base.FilterWith(func(c participle.Conflict) bool { return c.Location.FieldName == "B" })
	assert.Equal(t, 1, len(onlyB.Conflicts))
	assert.Equal(t, 3, len(base.Conflicts))
	deduped := base.Merge(base)
	assert.Equal(t, 3, len(deduped.Conflicts))
	assert.False(t, deduped.IsClean())
	assert.Equal(t, 1, deduped.ConflictCount(participle.ConflictUnreachable))
	errs := deduped.Errors()
	errs[0].Message = "mutated"
	assert.Equal(t, "shadow", deduped.Conflicts[1].Message)
}
