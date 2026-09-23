//go:build analyze

package participle_test

import (
	"testing"

	require "github.com/alecthomas/assert/v2"
	"github.com/alecthomas/participle/v2"
)

type ffGrammar struct {
	A string `@Ident | @Ident`
}

type cleanGrammar struct {
	A string `@("if" | "while") | @Ident`
}

type followInner struct {
	X string `@Ident?`
}

type followGrammar struct {
	In  *followInner `@@`
	End string       `@Ident`
}

type laGrammar struct {
	A string `(?= Ident) @Ident | @Ident`
}

func TestAnalyze(t *testing.T) {
	r, err := participle.MustBuild[ffGrammar]().Analyze()
	require.NoError(t, err)
	require.True(t, r.HasType(participle.ConflictFirstFirst))
	require.True(t, r.HasType(participle.ConflictUnreachable))
	for _, c := range r.Conflicts {
		require.NotEqual(t, "", c.Example)
		require.True(t, len(c.GrammarSnippet) >= 4)
		require.Equal(t, "ffGrammar.A", c.Location.String())
	}
	t.Log(r)

	r, _ = participle.MustBuild[cleanGrammar]().Analyze()
	require.True(t, r.IsClean(), r.String())

	r, _ = participle.MustBuild[followGrammar]().Analyze()
	require.True(t, r.HasType(participle.ConflictFirstFollow), r.String())
	require.Equal(t, "followInner", r.FilterByType(participle.ConflictFirstFollow).Conflicts[0].Location.TypeName)
	require.Equal(t, "1 conflict(s): 0 first/first, 1 first/follow, 0 unreachable", r.Summary())

	r, _ = participle.MustBuild[followGrammar]().AnalyzeWithOptions(participle.SuppressConflictType(participle.ConflictFirstFollow))
	require.True(t, r.IsClean())

	_, err = participle.Build[followGrammar](participle.StrictMode())
	require.Error(t, err)
	require.Contains(t, err.Error(), "conflict")
	_, err = participle.Build[cleanGrammar](participle.StrictMode())
	require.NoError(t, err)
}
