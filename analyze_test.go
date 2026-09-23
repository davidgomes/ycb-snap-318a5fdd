//go:build analyze

package participle_test

import (
	"reflect"
	"strings"
	"testing"

	require "github.com/alecthomas/assert/v2"

	"github.com/alecthomas/participle/v2"
)

func analyze[G any](t *testing.T, options ...participle.Option) *participle.AnalysisReport {
	t.Helper()
	parser := participle.MustBuild[G](options...)
	report, err := parser.Analyze()
	require.NoError(t, err)
	for _, c := range report.Conflicts {
		v := reflect.ValueOf(c)
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).Kind() == reflect.String {
				require.NotEqual(t, "", v.Field(i).String(), "%s is empty in %s", v.Type().Field(i).Name, c)
			}
		}
		require.NotEqual(t, "", c.Location.TypeName)
		require.True(t, len(c.GrammarSnippet) >= 4, c.GrammarSnippet)
		require.True(t, strings.Contains(c.Suggestion, " "), c.Suggestion)
	}
	return report
}

func TestAnalyzeCleanGrammar(t *testing.T) {
	type Stmt struct {
		If    *string `  "if" @Ident`
		While *string `| "while" @Ident`
		Name  *string `| @Ident`
	}
	type Grammar struct {
		Stmts []*Stmt  `@@*`
		Args  []string `("(" @Ident ("," @Ident)* ")")?`
	}
	report := analyze[Grammar](t)
	require.True(t, report.IsClean(), report.String())
	require.Equal(t, "no conflicts detected", report.Summary())
	require.NotEqual(t, "", report.String())
}

func TestAnalyzeFirstFirst(t *testing.T) {
	type Grammar struct {
		A string `  @Ident`
		B string `| @Ident`
	}
	report := analyze[Grammar](t)
	require.Equal(t, 1, report.ConflictCount(participle.ConflictFirstFirst))
	c := report.FilterByType(participle.ConflictFirstFirst).Conflicts[0]
	require.Equal(t, participle.SeverityWarning, c.Severity)
	require.Equal(t, participle.ConflictLocation{TypeName: "Grammar", FieldName: "B"}, c.Location)
	require.Equal(t, "<ident> | <ident>", c.GrammarSnippet)
	require.Equal(t, "<ident>", c.Example)
}

func TestAnalyzeLiteralVsTokenIsNotAConflict(t *testing.T) {
	type Grammar struct {
		Keyword string `  @"keyword"`
		Name    string `| @Ident`
	}
	require.True(t, analyze[Grammar](t).IsClean())
}

func TestAnalyzeFirstFirstSharedPrefix(t *testing.T) {
	type Grammar struct {
		Layout string `  "target" "datalayout" "=" @String`
		Triple string `| "target" "triple" "=" @String`
	}
	report := analyze[Grammar](t)
	require.Equal(t, 1, report.ConflictCount(participle.ConflictFirstFirst))
	require.False(t, report.HasType(participle.ConflictUnreachable))
	require.Equal(t, `"target"`, report.Conflicts[0].Example)
}

func TestAnalyzeUnion(t *testing.T) {
	report := analyze[unionGrammar](t, participle.Union[unionMember](unionA{}, unionB{}))
	require.Equal(t, 1, report.ConflictCount(participle.ConflictFirstFirst), report.String())
	require.Equal(t, "unionMember", report.Conflicts[0].Location.TypeName)
}

type unionMember interface{ member() }
type unionA struct {
	Name string `@Ident`
}
type unionB struct {
	Name  string `@Ident`
	Value string `"=" @Ident`
}

func (unionA) member() {}
func (unionB) member() {}

type unionGrammar struct {
	Member unionMember `@@`
}

func TestAnalyzeFirstFollow(t *testing.T) {
	type Optional struct {
		Names []string `@Ident? @Ident`
	}
	type Many struct {
		Names []string `@Ident* @Ident`
	}
	type OneOrMore struct {
		Names []string `@Ident+ @Ident`
	}
	for _, report := range []*participle.AnalysisReport{analyze[Optional](t), analyze[Many](t), analyze[OneOrMore](t)} {
		require.Equal(t, 1, report.ConflictCount(participle.ConflictFirstFollow), report.String())
		c := report.Conflicts[0]
		require.Equal(t, "Names", c.Location.FieldName)
		require.Equal(t, "<ident> <ident>", c.Example)
	}
}

func TestAnalyzeFirstFollowThroughEmbedding(t *testing.T) {
	type Inner struct {
		Name string `@Ident?`
	}
	type Grammar struct {
		Inner *Inner `@@`
		Last  string `@Ident`
	}
	report := analyze[Grammar](t)
	require.Equal(t, 1, report.ConflictCount(participle.ConflictFirstFollow), report.String())
	require.Equal(t, participle.ConflictLocation{TypeName: "Inner", FieldName: "Name"}, report.Conflicts[0].Location)
}

func TestAnalyzeFirstFollowThroughNullableEmbedding(t *testing.T) {
	type Empty struct {
		Name string `@"x"?`
	}
	type Grammar struct {
		Names []string `@Ident*`
		Empty *Empty   `@@`
		Last  string   `@Ident`
	}
	report := analyze[Grammar](t)
	require.Equal(t, 1, report.ConflictCount(participle.ConflictFirstFollow), report.String())
	require.Equal(t, "Grammar", report.Conflicts[0].Location.TypeName)
	require.Equal(t, "Names", report.Conflicts[0].Location.FieldName)
}

func TestAnalyzeRepetitionFollowedByEOFIsClean(t *testing.T) {
	type Grammar struct {
		Names []string `@Ident ("," @Ident)*`
	}
	require.True(t, analyze[Grammar](t).IsClean())
}

func TestAnalyzeUnreachable(t *testing.T) {
	type Grammar struct {
		A string `  "a" @Ident`
		B string `| "a" @Ident`
	}
	report := analyze[Grammar](t)
	require.Equal(t, 1, report.ConflictCount(participle.ConflictUnreachable), report.String())
	require.Equal(t, 1, len(report.Errors()))
	require.Equal(t, participle.SeverityError, report.Errors()[0].Severity)
	require.Equal(t, 1, len(report.Warnings()))
}

func TestAnalyzeNotUnreachableWhenSnippetsDiffer(t *testing.T) {
	type Grammar struct {
		A string `  "a" @Ident`
		B string `| "a" @String`
	}
	report := analyze[Grammar](t)
	require.True(t, report.HasType(participle.ConflictFirstFirst))
	require.False(t, report.HasType(participle.ConflictUnreachable))
}

func TestAnalyzeLookaheadSuppresses(t *testing.T) {
	type Inside struct {
		A string `(?= @Ident | @Ident) @Ident`
	}
	require.True(t, analyze[Inside](t).IsClean())

	type Guarded struct {
		A string `  (?= @Ident "=") @Ident "=" @Ident`
		B string `| @Ident`
	}
	require.True(t, analyze[Guarded](t).IsClean(), analyze[Guarded](t).String())
}

func TestAnalyzeNegationIsNotAConflict(t *testing.T) {
	type Grammar struct {
		A string `  @!";"`
		B string `| @!";"`
		C string `(@!";")* ";"`
	}
	require.True(t, analyze[Grammar](t).IsClean(), analyze[Grammar](t).String())
}

func TestAnalyzeRecursiveGrammar(t *testing.T) {
	type Value struct {
		Number *float64 `  @Float | @Int`
		Ident  *string  `| @Ident`
		List   []*Value `| "[" (@@ ("," @@)*)? "]"`
	}
	require.True(t, analyze[Value](t).IsClean(), analyze[Value](t).String())
}

func TestAnalyzeSuppressConflictType(t *testing.T) {
	type Grammar struct {
		A string `  @Ident`
		B string `| @Ident`
	}
	parser := participle.MustBuild[Grammar]()
	full, err := parser.Analyze()
	require.NoError(t, err)
	require.True(t, full.HasType(participle.ConflictFirstFirst))
	require.True(t, full.HasType(participle.ConflictUnreachable))

	report, err := parser.AnalyzeWithOptions(participle.SuppressConflictType(participle.ConflictFirstFirst))
	require.NoError(t, err)
	require.False(t, report.HasType(participle.ConflictFirstFirst))
	require.True(t, report.HasType(participle.ConflictUnreachable))
}

func TestStrictMode(t *testing.T) {
	type Ambiguous struct {
		Names []string `@Ident* @Ident`
	}
	parser, err := participle.Build[Ambiguous](participle.StrictMode())
	require.Error(t, err)
	require.Zero(t, parser)
	require.Contains(t, err.Error(), "conflict")

	type Clean struct {
		Names []string `@Ident ("," @Ident)*`
	}
	_, err = participle.Build[Clean](participle.StrictMode())
	require.NoError(t, err)
}

func TestReportMethods(t *testing.T) {
	ff := participle.Conflict{
		Type: participle.ConflictFirstFirst, Severity: participle.SeverityWarning, Message: "m",
		Location: participle.ConflictLocation{TypeName: "T", FieldName: "F"}, GrammarSnippet: "<a> | <a>",
		Example: "<a>", Suggestion: "do something",
	}
	un := ff
	un.Type = participle.ConflictUnreachable
	un.Severity = participle.SeverityError
	un.Location = participle.ConflictLocation{TypeName: "T"}
	report := &participle.AnalysisReport{Conflicts: []participle.Conflict{ff, un, ff}}

	require.Equal(t, "[warning] first/first at T.F: m", ff.String())
	require.Equal(t, "[error] unreachable at T: m", un.String())
	require.Equal(t, "3 conflict(s): 2 first/first, 0 first/follow, 1 unreachable", report.Summary())
	require.Equal(t, 2, len(report.Dedup().Conflicts))
	require.Equal(t, 3, len(report.Conflicts))
	require.Equal(t, []participle.Conflict{ff, un}, report.Merge(&participle.AnalysisReport{Conflicts: []participle.Conflict{un}}).Conflicts)
	require.Equal(t, []participle.Conflict{ff, ff}, report.FilterByType(participle.ConflictFirstFirst).Conflicts)
	require.Equal(t, []participle.Conflict{un}, report.Errors())
	require.Equal(t, []participle.Conflict{ff, ff}, report.Warnings())
	require.False(t, report.HasType(participle.ConflictFirstFollow))
	require.False(t, report.IsClean())
	str := report.String()
	require.True(t, strings.Count(str, "\n") > 1)
	require.Contains(t, str, "unreachable at T")
	require.Contains(t, str, "first/first at T.F")

	require.Equal(t, "first/follow", participle.ConflictFirstFollow.String())
	require.Equal(t, "error", participle.SeverityError.String())
	require.Equal(t, "T", participle.ConflictLocation{TypeName: "T"}.String())
}
