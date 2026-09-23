//go:build analyze

package participle

import (
	"errors"
	"strings"
)

// An AnalysisOption modifies the behaviour of Parser.AnalyzeWithOptions.
type AnalysisOption func(o *analysisOptions)

type analysisOptions struct {
	suppressed map[ConflictType]bool
}

// SuppressConflictType excludes conflicts of type t from the analysis report.
//
// It does not affect StrictMode, which always considers every conflict.
func SuppressConflictType(t ConflictType) AnalysisOption {
	return func(o *analysisOptions) {
		o.suppressed[t] = true
	}
}

// Analyze statically analyses the grammar for ambiguities.
//
// The following conflicts are detected:
//
//   - first/first (warning): two alternatives of a disjunction can start with the same token.
//   - first/follow (warning): an optional or repeated group can start with a token that can also follow it.
//   - unreachable (error): an alternative is identical to an earlier alternative and can never match.
//
// Literals and token types are considered distinct, so "keyword" | @Ident is not a conflict.
// Lookahead groups and negations are not analysed.
func (p *Parser[G]) Analyze() (*AnalysisReport, error) {
	return p.AnalyzeWithOptions()
}

// AnalyzeWithOptions is like Analyze but accepts options to control the analysis.
func (p *Parser[G]) AnalyzeWithOptions(options ...AnalysisOption) (*AnalysisReport, error) {
	if p == nil {
		return nil, errors.New("participle: cannot analyze a nil parser")
	}
	opts := analysisOptions{suppressed: map[ConflictType]bool{}}
	for _, option := range options {
		if option != nil {
			option(&opts)
		}
	}
	report, err := analyzeGrammar(&p.parserOptions)
	if err != nil {
		return nil, err
	}
	return report.FilterWith(func(c Conflict) bool { return !opts.suppressed[c.Type] }), nil
}

func (p *Parser[G]) checkStrictMode() error {
	report, err := analyzeGrammar(&p.parserOptions)
	if err != nil {
		return err
	}
	if report.IsClean() {
		return nil
	}
	lines := make([]string, 0, len(report.Conflicts)+1)
	lines = append(lines, "strict mode: grammar has "+report.Summary())
	for _, c := range report.Conflicts {
		lines = append(lines, "  "+c.String())
	}
	return errors.New(strings.Join(lines, "\n"))
}
