package participle_test

import (
	"testing"

	require "github.com/alecthomas/assert/v2"
	"github.com/alecthomas/participle/v2"
)

// StrictMode is available without the analyze tag. An unambiguous grammar builds
// either way: with the tag, analysis runs and finds nothing; without it, the hook is absent.
func TestStrictModeCleanGrammar(t *testing.T) {
	type grammar struct {
		Name string `@Ident`
	}
	p, err := participle.Build[grammar](participle.StrictMode())
	require.NoError(t, err)
	got, err := p.ParseString("", "foo")
	require.NoError(t, err)
	require.Equal(t, "foo", got.Name)
}

// Ambiguous grammars still build when StrictMode is not requested.
func TestAmbiguousGrammarBuildsWithoutStrictMode(t *testing.T) {
	type grammar struct {
		A string `  @Ident`
		B string `| @Ident`
	}
	p, err := participle.Build[grammar]()
	require.NoError(t, err)
	got, err := p.ParseString("", "foo")
	require.NoError(t, err)
	require.Equal(t, "foo", got.A)
}
