//go:build analyze

package participle

import (
	"strings"
	"testing"
)

func TestConflictAndReportAPI(t *testing.T) {
	if ConflictFirstFirst.String() != "first/first" || ConflictFirstFollow.String() != "first/follow" || ConflictUnreachable.String() != "unreachable" {
		t.Fatalf("conflict type strings: %s %s %s", ConflictFirstFirst, ConflictFirstFollow, ConflictUnreachable)
	}
	if SeverityWarning.String() != "warning" || SeverityError.String() != "error" {
		t.Fatalf("severity strings: %s %s", SeverityWarning, SeverityError)
	}
	loc := ConflictLocation{TypeName: "Expr", FieldName: "Op"}
	if loc.String() != "Expr.Op" {
		t.Fatalf("location: %s", loc)
	}
	if (ConflictLocation{TypeName: "Expr"}).String() != "Expr" {
		t.Fatal("type-only location")
	}

	c := Conflict{
		Type:           ConflictFirstFirst,
		Severity:       SeverityWarning,
		Message:        "alternatives overlap",
		Location:       loc,
		GrammarSnippet: `<ident> | <ident>`,
		Example:        "Ident",
		Suggestion:     "Reorder the alternatives",
	}
	got := c.String()
	want := "[warning] first/first at Expr.Op: alternatives overlap"
	if got != want {
		t.Fatalf("Conflict.String() = %q", got)
	}

	other := c
	other.Type = ConflictUnreachable
	other.Severity = SeverityError
	other.GrammarSnippet = "<ident>"
	other.Message = "shadowed alternative"

	follow := Conflict{
		Type:           ConflictFirstFollow,
		Severity:       SeverityWarning,
		Message:        "quantifier overlaps follow",
		Location:       ConflictLocation{TypeName: "Expr"},
		GrammarSnippet: "<ident>*",
		Example:        "Ident",
		Suggestion:     "Add a delimiter between the group and what follows",
	}

	report := &AnalysisReport{Conflicts: []Conflict{c, other, follow, c}}
	if report.IsClean() {
		t.Fatal("expected conflicts")
	}
	if len(report.Errors()) != 1 || len(report.Warnings()) != 3 {
		t.Fatalf("errors=%d warnings=%d", len(report.Errors()), len(report.Warnings()))
	}
	if report.ConflictCount(ConflictFirstFirst) != 2 || !report.HasType(ConflictFirstFollow) {
		t.Fatalf("counts ff=%d has follow=%v", report.ConflictCount(ConflictFirstFirst), report.HasType(ConflictFirstFollow))
	}
	summary := report.Summary()
	if summary != "4 conflict(s): 2 first/first, 1 first/follow, 1 unreachable" {
		t.Fatalf("summary: %s", summary)
	}
	text := report.String()
	if !strings.Contains(text, "\n") || !strings.Contains(text, "first/first") || !strings.Contains(text, "Expr.Op") {
		t.Fatalf("report string:\n%s", text)
	}

	deduped := report.Dedup()
	if len(deduped.Conflicts) != 3 {
		t.Fatalf("dedup len=%d", len(deduped.Conflicts))
	}
	if len(report.Conflicts) != 4 {
		t.Fatal("Dedup mutated the receiver")
	}
	if deduped.Conflicts[0].Message != c.Message || deduped.Conflicts[1].Type != ConflictUnreachable {
		t.Fatal("Dedup did not preserve order")
	}

	filtered := report.FilterByType(ConflictFirstFollow)
	if len(filtered.Conflicts) != 1 || filtered.Conflicts[0].Type != ConflictFirstFollow {
		t.Fatalf("filter: %+v", filtered.Conflicts)
	}
	if len(report.Conflicts) != 4 {
		t.Fatal("FilterByType mutated the receiver")
	}

	merged := filtered.Merge(deduped)
	if merged.ConflictCount(ConflictFirstFollow) != 1 || merged.ConflictCount(ConflictFirstFirst) != 1 {
		t.Fatalf("merge: %s", merged.Summary())
	}
	if len(filtered.Conflicts) != 1 {
		t.Fatal("Merge mutated the receiver")
	}

	clean := &AnalysisReport{}
	if clean.Summary() != "no conflicts detected" || clean.String() == "" || !strings.Contains(clean.String(), "\n") {
		t.Fatalf("clean summary=%q string=%q", clean.Summary(), clean.String())
	}
	if !clean.IsClean() || clean.HasType(ConflictUnreachable) || clean.ConflictCount(ConflictFirstFirst) != 0 {
		t.Fatal("clean report predicates")
	}
	if (&AnalysisReport{}).Merge(nil).Summary() != "no conflicts detected" {
		t.Fatal("merge nil")
	}
}

func TestAnalyzeConflicts(t *testing.T) {
	t.Run("ident disjunction", func(t *testing.T) {
		type choice struct {
			A string `@Ident |`
			B string `@Ident`
		}
		p := MustBuild[choice]()
		report, err := p.Analyze()
		if err != nil {
			t.Fatal(err)
		}
		if !report.HasType(ConflictFirstFirst) || !report.HasType(ConflictUnreachable) {
			t.Fatalf("missing conflicts: %s\n%s", report.Summary(), report)
		}
		for _, c := range report.Conflicts {
			assertConflictShape(t, c)
			if c.Location.TypeName != "choice" {
				t.Fatalf("location type %s", c.Location)
			}
			if c.Location.FieldName != "" {
				t.Fatalf("struct-level disjunction should not pin a field, got %s", c.Location)
			}
		}
		ff := report.FilterByType(ConflictFirstFirst)
		if ff.Conflicts[0].Severity != SeverityWarning {
			t.Fatal("first/first severity")
		}
		un := report.FilterByType(ConflictUnreachable)
		if un.Conflicts[0].Severity != SeverityError {
			t.Fatal("unreachable severity")
		}
	})

	t.Run("same field", func(t *testing.T) {
		type choice struct {
			A string `@Ident | @Ident`
		}
		report := mustAnalyze[choice](t)
		ff := report.FilterByType(ConflictFirstFirst)
		if len(ff.Conflicts) != 1 || ff.Conflicts[0].Location.String() != "choice.A" {
			t.Fatalf("location: %+v", ff.Conflicts)
		}
	})

	t.Run("distinct literals", func(t *testing.T) {
		type choice struct {
			A string `@"if" | @"while"`
		}
		report := mustAnalyze[choice](t)
		if !report.IsClean() {
			t.Fatalf("expected clean, got %s", report)
		}
		type bare struct {
			A string `"if" | "while"`
		}
		if report := mustAnalyze[bare](t); !report.IsClean() {
			t.Fatalf("expected clean bare literals, got %s", report)
		}
	})

	t.Run("bare token reference", func(t *testing.T) {
		type choice struct {
			A string `Ident | Ident`
		}
		report := mustAnalyze[choice](t)
		if !report.HasType(ConflictFirstFirst) || !report.HasType(ConflictUnreachable) {
			t.Fatalf("got %s", report)
		}
	})

	t.Run("bracket optional", func(t *testing.T) {
		type gram struct {
			A string `[ @Ident ]`
			B string `@Ident`
		}
		report := mustAnalyze[gram](t)
		if !report.HasType(ConflictFirstFollow) {
			t.Fatalf("got %s", report)
		}
	})

	t.Run("literal versus token", func(t *testing.T) {
		type choice struct {
			A string `@"keyword" | @Ident`
		}
		report := mustAnalyze[choice](t)
		if !report.IsClean() {
			t.Fatalf("literal and token type must not conflict, got %s", report)
		}
	})

	t.Run("duplicate literal", func(t *testing.T) {
		type choice struct {
			A string `@"if" | @"if"`
		}
		report := mustAnalyze[choice](t)
		if !report.HasType(ConflictFirstFirst) || !report.HasType(ConflictUnreachable) {
			t.Fatalf("got %s", report)
		}
		if !strings.Contains(report.FilterByType(ConflictFirstFirst).Conflicts[0].Example, "if") {
			t.Fatalf("example: %+v", report.Conflicts)
		}
	})

	t.Run("longer alternative is not unreachable", func(t *testing.T) {
		type choice struct {
			A string `@Ident | @Ident @Ident`
		}
		report := mustAnalyze[choice](t)
		if !report.HasType(ConflictFirstFirst) || report.HasType(ConflictUnreachable) {
			t.Fatalf("got %s", report)
		}
	})

	for _, tc := range []struct {
		name  string
		build func(t *testing.T) *AnalysisReport
	}{
		{"star", func(t *testing.T) *AnalysisReport {
			type gram struct {
				A string `@Ident*`
				B string `@Ident`
			}
			return mustAnalyze[gram](t)
		}},
		{"plus", func(t *testing.T) *AnalysisReport {
			type gram struct {
				A string `@Ident+`
				B string `@Ident`
			}
			return mustAnalyze[gram](t)
		}},
		{"optional", func(t *testing.T) *AnalysisReport {
			type gram struct {
				A string `@Ident?`
				B string `@Ident`
			}
			return mustAnalyze[gram](t)
		}},
	} {
		t.Run("first/follow "+tc.name, func(t *testing.T) {
			report := tc.build(t)
			if !report.HasType(ConflictFirstFollow) {
				t.Fatalf("expected first/follow, got %s", report)
			}
			c := report.FilterByType(ConflictFirstFollow).Conflicts[0]
			assertConflictShape(t, c)
			if c.Severity != SeverityWarning {
				t.Fatal("severity")
			}
			if c.Location.FieldName != "A" {
				t.Fatalf("field location %s", c.Location)
			}
		})
	}

	t.Run("optional different follow", func(t *testing.T) {
		type gram struct {
			A string `@"if"?`
			B string `@"then"`
		}
		if report := mustAnalyze[gram](t); !report.IsClean() {
			t.Fatalf("got %s", report)
		}
	})

	t.Run("epsilon through embed", func(t *testing.T) {
		type optInner struct {
			A string `@Ident?`
		}
		type optOuter struct {
			I optInner `@@`
			B string   `@Ident`
		}
		report := mustAnalyze[optOuter](t)
		if !report.HasType(ConflictFirstFollow) {
			t.Fatalf("expected propagated first/follow, got %s", report)
		}
		c := report.FilterByType(ConflictFirstFollow).Conflicts[0]
		if c.Location.String() != "optInner.A" {
			t.Fatalf("innermost location %s", c.Location)
		}
	})

	t.Run("lookahead suppresses subtree", func(t *testing.T) {
		type gram struct {
			A string `(?= @Ident | @Ident) @String`
		}
		if report := mustAnalyze[gram](t); !report.IsClean() {
			t.Fatalf("lookahead subtree should be silent, got %s", report)
		}
	})

	t.Run("negation suppresses subtree", func(t *testing.T) {
		type gram struct {
			A string `!(@Ident | @Ident) @String`
		}
		if report := mustAnalyze[gram](t); !report.IsClean() {
			t.Fatalf("negation subtree should be silent, got %s", report)
		}
	})

	t.Run("negation alternatives", func(t *testing.T) {
		type gram struct {
			A string `!@Ident | !@Ident`
		}
		if report := mustAnalyze[gram](t); !report.IsClean() {
			t.Fatalf("negation alternatives should be silent, got %s", report)
		}
	})

	t.Run("brace repetition", func(t *testing.T) {
		type gram struct {
			A string `{ @Ident }`
			B string `@Ident`
		}
		report := mustAnalyze[gram](t)
		if !report.HasType(ConflictFirstFollow) {
			t.Fatalf("got %s", report)
		}
	})

	t.Run("deep embed epsilon", func(t *testing.T) {
		type aWrap struct {
			X string `@Ident?`
		}
		type bWrap struct {
			A aWrap `@@`
		}
		type cWrap struct {
			B bWrap  `@@`
			Y string `@Ident`
		}
		report := mustAnalyze[cWrap](t)
		c := report.FilterByType(ConflictFirstFollow)
		if len(c.Conflicts) != 1 || c.Conflicts[0].Location.String() != "aWrap.X" {
			t.Fatalf("got %s", report)
		}
	})

	t.Run("union overlap", func(t *testing.T) {
		p, err := Build[analyzeProgram](Union[analyzeStmt](analyzeIf{}, analyzeWhile{}))
		if err != nil {
			t.Fatal(err)
		}
		report, err := p.Analyze()
		if err != nil {
			t.Fatal(err)
		}
		if !report.HasType(ConflictFirstFirst) {
			t.Fatalf("union members with the same first token should conflict, got %s", report)
		}
		if report.FilterByType(ConflictFirstFirst).Conflicts[0].Location.TypeName != "analyzeStmt" {
			t.Fatalf("location %s", report.Conflicts[0].Location)
		}
	})

	t.Run("suppress option", func(t *testing.T) {
		type choice struct {
			A string `@Ident | @Ident`
		}
		p := MustBuild[choice]()
		report, err := p.AnalyzeWithOptions(SuppressConflictType(ConflictFirstFirst))
		if err != nil {
			t.Fatal(err)
		}
		if report.HasType(ConflictFirstFirst) || !report.HasType(ConflictUnreachable) {
			t.Fatalf("got %s", report)
		}
	})
}

func TestStrictMode(t *testing.T) {
	type choice struct {
		A string `@Ident | @Ident`
	}
	if _, err := Build[choice](StrictMode()); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("strict error: %v", err)
	}
	type clean struct {
		A string `@"if"`
		B string `@"while"`
	}
	if _, err := Build[clean](StrictMode()); err != nil {
		t.Fatal(err)
	}
	type quantified struct {
		A string `@Ident*`
		B string `@Ident`
	}
	if _, err := Build[quantified](StrictMode()); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("strict mode should reject first/follow warnings: %v", err)
	}
	// Analysis is independent of parsing when StrictMode is off.
	if _, err := Build[choice](); err != nil {
		t.Fatal(err)
	}
}

type analyzeStmt interface{ analyzeStmt() }

type analyzeIf struct {
	A string `@Ident`
}

func (analyzeIf) analyzeStmt() {}

type analyzeWhile struct {
	A string `@Ident`
}

func (analyzeWhile) analyzeStmt() {}

type analyzeProgram struct {
	S analyzeStmt `@@`
}

func mustAnalyze[G any](t *testing.T) *AnalysisReport {
	t.Helper()
	p, err := Build[G]()
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func assertConflictShape(t *testing.T, c Conflict) {
	t.Helper()
	if c.Message == "" || c.GrammarSnippet == "" || c.Example == "" || c.Suggestion == "" {
		t.Fatalf("empty field: %+v", c)
	}
	if len(c.GrammarSnippet) < 4 {
		t.Fatalf("snippet %q", c.GrammarSnippet)
	}
	if !strings.Contains(c.Suggestion, " ") {
		t.Fatalf("suggestion should be multi-word: %q", c.Suggestion)
	}
	if c.Location.String() == "" || c.String() == "" {
		t.Fatalf("location/string: %+v", c)
	}
}
