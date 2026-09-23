//go:build analyze

package participle

import (
	"strings"
	"testing"

	require "github.com/alecthomas/assert/v2"
)

func TestAnalysisReportAPI(t *testing.T) {
	ff := Conflict{
		Type:           ConflictFirstFirst,
		Severity:       SeverityWarning,
		Message:        "alternatives overlap",
		Location:       ConflictLocation{TypeName: "Expr", FieldName: "Op"},
		GrammarSnippet: `<ident> | <ident>`,
		Example:        "Ident",
		Suggestion:     "Reorder the alternatives so they do not overlap",
	}
	fl := Conflict{
		Type:           ConflictFirstFollow,
		Severity:       SeverityWarning,
		Message:        "group overlaps follow",
		Location:       ConflictLocation{TypeName: "Expr", FieldName: "Rest"},
		GrammarSnippet: "<ident>*",
		Example:        "Ident",
		Suggestion:     "Anchor the repetition with a distinct delimiter",
	}
	ur := Conflict{
		Type:           ConflictUnreachable,
		Severity:       SeverityError,
		Message:        "alternative is shadowed",
		Location:       ConflictLocation{TypeName: "Expr"},
		GrammarSnippet: `"if" | "if"`,
		Example:        "if",
		Suggestion:     "Remove the shadowed alternative",
	}

	require.Equal(t, "first/first", ConflictFirstFirst.String())
	require.Equal(t, "first/follow", ConflictFirstFollow.String())
	require.Equal(t, "unreachable", ConflictUnreachable.String())
	require.Equal(t, "warning", SeverityWarning.String())
	require.Equal(t, "error", SeverityError.String())
	require.Equal(t, "Expr", ConflictLocation{TypeName: "Expr"}.String())
	require.Equal(t, "Expr.Op", ff.Location.String())
	require.Equal(t, "[warning] first/first at Expr.Op: alternatives overlap", ff.String())

	empty := &AnalysisReport{}
	require.True(t, empty.IsClean())
	require.Equal(t, "no conflicts detected", empty.Summary())
	require.NotEqual(t, "", empty.String())
	require.True(t, strings.Contains(empty.String(), "\n"))
	require.Equal(t, "no conflicts detected", strings.TrimSpace(empty.String()))
	require.Equal(t, 0, empty.ConflictCount(ConflictFirstFirst))
	require.False(t, empty.HasType(ConflictUnreachable))

	report := &AnalysisReport{Conflicts: []Conflict{ff, fl, ur, ff}}
	require.False(t, report.IsClean())
	require.Equal(t, []Conflict{ur}, report.Errors())
	require.Equal(t, []Conflict{ff, fl, ff}, report.Warnings())
	require.Equal(t, 2, report.ConflictCount(ConflictFirstFirst))
	require.Equal(t, 1, report.ConflictCount(ConflictFirstFollow))
	require.Equal(t, 1, report.ConflictCount(ConflictUnreachable))
	require.True(t, report.HasType(ConflictUnreachable))
	require.Equal(t, "4 conflicts: 2 first/first, 1 first/follow, 1 unreachable", report.Summary())

	onlyFF := report.FilterByType(ConflictFirstFirst)
	require.Equal(t, []Conflict{ff, ff}, onlyFF.Conflicts)
	require.Equal(t, 4, len(report.Conflicts))

	filtered := report.FilterWith(func(c Conflict) bool {
		return c.Location.FieldName != ""
	})
	require.Equal(t, []Conflict{ff, fl, ff}, filtered.Conflicts)

	deduped := report.Dedup()
	require.Equal(t, []Conflict{ff, fl, ur}, deduped.Conflicts)
	require.Equal(t, 4, len(report.Conflicts))

	other := &AnalysisReport{Conflicts: []Conflict{ur, fl}}
	merged := report.Merge(other)
	require.Equal(t, []Conflict{ff, fl, ur}, merged.Conflicts)
	require.Equal(t, 4, len(report.Conflicts))
	require.Equal(t, 2, len(other.Conflicts))

	text := report.String()
	require.True(t, strings.Contains(text, "first/first"))
	require.True(t, strings.Contains(text, "Expr.Op"))
	require.True(t, strings.Contains(text, "unreachable"))
	require.True(t, strings.Contains(text, "Expr"))
	require.True(t, strings.Count(text, "\n") >= 2)

	one := &AnalysisReport{Conflicts: []Conflict{ff}}
	require.Equal(t, "1 conflict: 1 first/first, 0 first/follow, 0 unreachable", one.Summary())
}

func TestAnalyzeCleanAndDistinctTokens(t *testing.T) {
	type value struct {
		String *string  `  @String`
		Float  *float64 `| @Float`
		Int    *int     `| @Int`
	}
	type property struct {
		Key   string `@Ident "="`
		Value *value `@@`
	}
	type section struct {
		Identifier string      `"[" @Ident "]"`
		Properties []*property `@@*`
	}
	type ini struct {
		Properties []*property `@@*`
		Sections   []*section  `@@*`
	}

	p := MustBuild[ini]()
	report, err := p.Analyze()
	require.NoError(t, err)
	require.True(t, report.IsClean(), report.String())

	type keywordOrIdent struct {
		Keyword string `'keyword' |`
		Ident   string `@Ident`
	}
	p2 := MustBuild[keywordOrIdent]()
	report, err = p2.Analyze()
	require.NoError(t, err)
	require.True(t, report.IsClean(), report.String())

	type keywords struct {
		If    string `'if' |`
		While string `'while'`
	}
	p3 := MustBuild[keywords]()
	report, err = p3.Analyze()
	require.NoError(t, err)
	require.True(t, report.IsClean(), report.String())
}

func TestAnalyzeFirstFirstAndUnreachable(t *testing.T) {
	type sameIdent struct {
		A string `@Ident |`
		B string `@Ident`
	}
	p := MustBuild[sameIdent]()
	report, err := p.Analyze()
	require.NoError(t, err)
	require.True(t, report.HasType(ConflictFirstFirst), report.String())
	require.True(t, report.HasType(ConflictUnreachable), report.String())
	require.Equal(t, SeverityWarning, report.FilterByType(ConflictFirstFirst).Conflicts[0].Severity)
	require.Equal(t, SeverityError, report.FilterByType(ConflictUnreachable).Conflicts[0].Severity)
	for _, c := range report.Conflicts {
		require.True(t, len(c.GrammarSnippet) >= 4, c.GrammarSnippet)
		require.NotEqual(t, "", c.Example)
		require.True(t, len(strings.Fields(c.Suggestion)) >= 2, c.Suggestion)
		require.NotEqual(t, "", c.Message)
		require.Equal(t, "sameIdent", c.Location.TypeName)
		require.Equal(t, "B", c.Location.FieldName, c.String())
		require.True(t, strings.Contains(c.Example, "Ident"), c.Example)
	}

	type sameField struct {
		A string `@Ident | @Ident`
	}
	report, err = MustBuild[sameField]().Analyze()
	require.NoError(t, err)
	require.True(t, report.HasType(ConflictFirstFirst))
	require.Equal(t, "A", report.Conflicts[0].Location.FieldName)

	type prefix struct {
		A string `'if' Ident |`
		B string `'if' String`
	}
	report, err = MustBuild[prefix]().Analyze()
	require.NoError(t, err)
	require.True(t, report.HasType(ConflictFirstFirst), report.String())
	require.False(t, report.HasType(ConflictUnreachable), report.String())
	require.Equal(t, "prefix", report.FilterByType(ConflictFirstFirst).Conflicts[0].Location.TypeName)
}

func TestAnalyzeFirstFollow(t *testing.T) {
	type starred struct {
		Xs []string `@Ident*`
		Y  string   `@Ident`
	}
	report, err := MustBuild[starred]().Analyze()
	require.NoError(t, err)
	require.True(t, report.HasType(ConflictFirstFollow), report.String())
	require.False(t, report.HasType(ConflictFirstFirst), report.String())
	c := report.FilterByType(ConflictFirstFollow).Conflicts[0]
	require.Equal(t, SeverityWarning, c.Severity)
	require.Equal(t, "starred", c.Location.TypeName)
	require.Equal(t, "Xs", c.Location.FieldName)
	require.True(t, len(c.GrammarSnippet) >= 4)
	require.NotEqual(t, "", c.Example)

	type optional struct {
		X string `@Ident?`
		Y string `@Ident`
	}
	report, err = MustBuild[optional]().Analyze()
	require.NoError(t, err)
	require.True(t, report.HasType(ConflictFirstFollow), report.String())
	require.Equal(t, "X", report.Conflicts[0].Location.FieldName)

	type plus struct {
		Xs []string `@Ident+`
		Y  string   `@Ident`
	}
	report, err = MustBuild[plus]().Analyze()
	require.NoError(t, err)
	require.True(t, report.HasType(ConflictFirstFollow), report.String())

	type separated struct {
		Xs []string `@Ident*`
		Y  string   `@';'`
	}
	report, err = MustBuild[separated]().Analyze()
	require.NoError(t, err)
	require.True(t, report.IsClean(), report.String())

	type brackets struct {
		Xs []string `[ @Ident ]`
		Y  string   `@Ident`
	}
	report, err = MustBuild[brackets]().Analyze()
	require.NoError(t, err)
	require.True(t, report.HasType(ConflictFirstFollow), report.String())

	type braces struct {
		Xs []string `{ @Ident }`
		Y  string   `@Ident`
	}
	report, err = MustBuild[braces]().Analyze()
	require.NoError(t, err)
	require.True(t, report.HasType(ConflictFirstFollow), report.String())
}

func TestAnalyzeEpsilonThroughEmbedding(t *testing.T) {
	type maybeComma struct {
		C string `@','?`
	}
	type inner struct {
		A string `@Ident?`
	}
	type outer struct {
		I inner      `@@`
		M maybeComma `@@`
		T string     `@Ident`
	}
	report, err := MustBuild[outer]().Analyze()
	require.NoError(t, err)
	require.True(t, report.HasType(ConflictFirstFollow), report.String())
	foundInner := false
	for _, c := range report.FilterByType(ConflictFirstFollow).Conflicts {
		if c.Location.TypeName == "inner" && c.Location.FieldName == "A" {
			foundInner = true
		}
	}
	require.True(t, foundInner, report.String())

	// A nullable embedded struct followed by a different token is not a conflict.
	type onlySemi struct {
		I inner  `@@`
		T string `@';'`
	}
	report, err = MustBuild[onlySemi]().Analyze()
	require.NoError(t, err)
	require.True(t, report.IsClean(), report.String())

	type trailing struct {
		A string `@Ident?`
	}
	report, err = MustBuild[trailing]().Analyze()
	require.NoError(t, err)
	require.True(t, report.IsClean(), report.String())

	type plusSep struct {
		Xs []string `@Ident+`
		Y  string   `@';'`
	}
	report, err = MustBuild[plusSep]().Analyze()
	require.NoError(t, err)
	require.True(t, report.IsClean(), report.String())
}

func TestAnalyzeLookaheadAndNegationSuppress(t *testing.T) {
	type looked struct {
		A string `(?= @Ident | @Ident) @Ident`
	}
	report, err := MustBuild[looked]().Analyze()
	require.NoError(t, err)
	require.True(t, report.IsClean(), report.String())

	type negated struct {
		A string `@~Ident |`
		B string `@~Ident`
	}
	report, err = MustBuild[negated]().Analyze()
	require.NoError(t, err)
	require.True(t, report.IsClean(), report.String())

	type outside struct {
		A string `(?= 'if') @Ident |`
		B string `@Ident`
	}
	report, err = MustBuild[outside]().Analyze()
	require.NoError(t, err)
	require.True(t, report.HasType(ConflictFirstFirst), report.String())
}

func TestAnalyzeSuppressAndStrictMode(t *testing.T) {
	type starred struct {
		Xs []string `@Ident*`
		Y  string   `@Ident`
	}
	p := MustBuild[starred]()
	report, err := p.AnalyzeWithOptions(SuppressConflictType(ConflictFirstFollow))
	require.NoError(t, err)
	require.True(t, report.IsClean(), report.String())

	full, err := p.Analyze()
	require.NoError(t, err)
	require.False(t, full.IsClean())

	_, err = Build[starred](StrictMode())
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "conflict"), err.Error())

	type clean struct {
		A string `'if' |`
		B string `'while'`
	}
	built, err := Build[clean](StrictMode())
	require.NoError(t, err)
	require.True(t, built != nil)

	type ambiguous struct {
		A string `@Ident |`
		B string `@Ident`
	}
	parser, err := Build[ambiguous](StrictMode())
	require.Error(t, err)
	require.True(t, parser == nil)
	require.True(t, strings.Contains(err.Error(), "conflict"), err.Error())

	// StrictMode does not consult SuppressConflictType; warnings alone fail the build.
	type warnOnly struct {
		A string `'if' Ident |`
		B string `'if' String`
	}
	_, err = Build[warnOnly](StrictMode())
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "conflict"), err.Error())
}

type analyzeExpr interface{ analyzeExpr() }
type analyzeAdd struct {
	V string `'+' @Ident`
}
type analyzeSub struct {
	V string `'+' @Ident`
}

func (analyzeAdd) analyzeExpr() {}
func (analyzeSub) analyzeExpr() {}

func TestAnalyzeUnionRecursionAndNonEmpty(t *testing.T) {
	type program struct {
		E analyzeExpr `@@`
	}
	p := MustBuild[program](Union[analyzeExpr](analyzeAdd{}, analyzeSub{}))
	report, err := p.Analyze()
	require.NoError(t, err)
	require.True(t, report.HasType(ConflictFirstFirst), report.String())
	require.False(t, report.HasType(ConflictUnreachable), report.String())
	require.Equal(t, "analyzeExpr", report.FilterByType(ConflictFirstFirst).Conflicts[0].Location.TypeName)

	type rec struct {
		Ident string `@Ident`
		Op    *rec   `('+' @@)?`
	}
	report, err = MustBuild[rec]().Analyze()
	require.NoError(t, err)
	require.True(t, report.IsClean(), report.String())

	type bang struct {
		A string `@Ident!`
		B string `@Ident`
	}
	report, err = MustBuild[bang]().Analyze()
	require.NoError(t, err)
	require.True(t, report.IsClean(), report.String())

	type three struct {
		A string `@Ident | @Ident | @Ident`
	}
	report, err = MustBuild[three]().Analyze()
	require.NoError(t, err)
	require.Equal(t, 2, report.ConflictCount(ConflictFirstFirst))
	require.Equal(t, 2, report.ConflictCount(ConflictUnreachable))
}

type calcValue struct {
	Number   *float64  `  @(Float|Int)`
	Variable *string   `| @Ident`
	Sub      *calcExpr `| "(" @@ ")"`
}
type calcFactor struct {
	Base *calcValue `@@`
	Exp  *calcValue `("^" @@)?`
}
type calcOpFactor struct {
	Op     string      `@("*" | "/")`
	Factor *calcFactor `@@`
}
type calcTerm struct {
	Left  *calcFactor     `@@`
	Right []*calcOpFactor `@@*`
}
type calcOpTerm struct {
	Op   string    `@("+" | "-")`
	Term *calcTerm `@@`
}
type calcExpr struct {
	Left  *calcTerm     `@@`
	Right []*calcOpTerm `@@*`
}

func TestAnalyzeExpressionGrammarIsClean(t *testing.T) {
	report, err := MustBuild[calcExpr]().Analyze()
	require.NoError(t, err)
	require.True(t, report.IsClean(), report.String())
}

func TestAnalyzeNestedTypeName(t *testing.T) {
	type inner struct {
		A string `@Ident |`
		B string `@Ident`
	}
	type outer struct {
		I inner `@@`
	}
	report, err := MustBuild[outer]().Analyze()
	require.NoError(t, err)
	require.True(t, report.HasType(ConflictFirstFirst), report.String())
	for _, c := range report.Conflicts {
		require.Equal(t, "inner", c.Location.TypeName, c.String())
	}
}
