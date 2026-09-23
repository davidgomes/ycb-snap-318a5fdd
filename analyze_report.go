//go:build analyze

package participle

import (
	"fmt"
	"strings"
)

// ConflictType classifies a grammar ambiguity detected by Analyze.
type ConflictType int

const (
	// ConflictFirstFirst indicates that two alternatives of a disjunction can
	// start with the same token.
	ConflictFirstFirst ConflictType = iota
	// ConflictFirstFollow indicates that an optional or repeated group can
	// start with a token that may also follow it.
	ConflictFirstFollow
	// ConflictUnreachable indicates an alternative that can never match
	// because an identical earlier alternative always wins.
	ConflictUnreachable
)

var allConflictTypes = []ConflictType{ConflictFirstFirst, ConflictFirstFollow, ConflictUnreachable}

func (c ConflictType) String() string {
	switch c {
	case ConflictFirstFirst:
		return "first/first"
	case ConflictFirstFollow:
		return "first/follow"
	case ConflictUnreachable:
		return "unreachable"
	}
	return fmt.Sprintf("ConflictType(%d)", int(c))
}

// Severity of a Conflict.
type Severity int

const (
	SeverityWarning Severity = iota
	SeverityError
)

func (s Severity) String() string {
	switch s {
	case SeverityWarning:
		return "warning"
	case SeverityError:
		return "error"
	}
	return fmt.Sprintf("Severity(%d)", int(s))
}

// ConflictLocation identifies where in the Go grammar a conflict originates.
type ConflictLocation struct {
	// TypeName is the innermost Go type containing the conflict.
	TypeName string
	// FieldName is the struct field containing the conflict, if known.
	FieldName string
}

func (l ConflictLocation) String() string {
	if l.FieldName == "" {
		return l.TypeName
	}
	return l.TypeName + "." + l.FieldName
}

// Conflict describes a single grammar ambiguity.
type Conflict struct {
	Type     ConflictType
	Severity Severity
	Message  string
	Location ConflictLocation
	// GrammarSnippet is the EBNF of the conflicting grammar fragment.
	GrammarSnippet string
	// Example is a token sequence that triggers the ambiguity.
	Example string
	// Suggestion is a recommendation for resolving the conflict.
	Suggestion string
}

func (c Conflict) String() string {
	return fmt.Sprintf("[%s] %s at %s: %s", c.Severity, c.Type, c.Location, c.Message)
}

func (c Conflict) dedupKey() [3]string {
	return [3]string{c.Type.String(), c.Location.String(), c.GrammarSnippet}
}

// AnalysisReport is the result of analysing a grammar.
//
// All methods return new values and never mutate the receiver.
type AnalysisReport struct {
	Conflicts []Conflict
}

func (r *AnalysisReport) conflicts() []Conflict {
	if r == nil {
		return nil
	}
	return r.Conflicts
}

// FilterWith returns a new report containing the conflicts for which keep
// returns true, in their original order.
func (r *AnalysisReport) FilterWith(keep func(Conflict) bool) *AnalysisReport {
	out := &AnalysisReport{Conflicts: []Conflict{}}
	for _, c := range r.conflicts() {
		if keep(c) {
			out.Conflicts = append(out.Conflicts, c)
		}
	}
	return out
}

// FilterByType returns a new report containing only conflicts of type t.
func (r *AnalysisReport) FilterByType(t ConflictType) *AnalysisReport {
	return r.FilterWith(func(c Conflict) bool { return c.Type == t })
}

// Errors returns all conflicts with SeverityError.
func (r *AnalysisReport) Errors() []Conflict {
	return r.FilterWith(func(c Conflict) bool { return c.Severity == SeverityError }).Conflicts
}

// Warnings returns all conflicts with SeverityWarning.
func (r *AnalysisReport) Warnings() []Conflict {
	return r.FilterWith(func(c Conflict) bool { return c.Severity == SeverityWarning }).Conflicts
}

// ConflictCount returns the number of conflicts of type t.
func (r *AnalysisReport) ConflictCount(t ConflictType) int {
	n := 0
	for _, c := range r.conflicts() {
		if c.Type == t {
			n++
		}
	}
	return n
}

// HasType returns true if the report contains at least one conflict of type t.
func (r *AnalysisReport) HasType(t ConflictType) bool {
	return r.ConflictCount(t) > 0
}

// IsClean returns true if no conflicts were detected.
func (r *AnalysisReport) IsClean() bool {
	return len(r.conflicts()) == 0
}

// Summary returns a single line summarising the conflict counts by type.
func (r *AnalysisReport) Summary() string {
	if r.IsClean() {
		return "no conflicts detected"
	}
	counts := make([]string, 0, len(allConflictTypes))
	for _, t := range allConflictTypes {
		counts = append(counts, fmt.Sprintf("%d %s", r.ConflictCount(t), t))
	}
	return fmt.Sprintf("%d conflict(s): %s", len(r.conflicts()), strings.Join(counts, ", "))
}

// String returns a human readable, multi-line description of the report.
func (r *AnalysisReport) String() string {
	w := &strings.Builder{}
	fmt.Fprintf(w, "grammar analysis: %s\n", r.Summary())
	for _, c := range r.conflicts() {
		fmt.Fprintf(w, "  %s\n", c)
		fmt.Fprintf(w, "    grammar:    %s\n", c.GrammarSnippet)
		fmt.Fprintf(w, "    example:    %s\n", c.Example)
		fmt.Fprintf(w, "    suggestion: %s\n", c.Suggestion)
	}
	return strings.TrimSuffix(w.String(), "\n")
}

// Merge returns a new report containing the conflicts of both reports, with
// duplicates removed.
func (r *AnalysisReport) Merge(other *AnalysisReport) *AnalysisReport {
	combined := make([]Conflict, 0, len(r.conflicts())+len(other.conflicts()))
	combined = append(combined, r.conflicts()...)
	combined = append(combined, other.conflicts()...)
	return (&AnalysisReport{Conflicts: combined}).Dedup()
}

// Dedup returns a new report with duplicate conflicts removed. Conflicts are
// considered duplicates if they have the same type, location and grammar
// snippet. The first occurrence is kept.
func (r *AnalysisReport) Dedup() *AnalysisReport {
	seen := map[[3]string]bool{}
	return r.FilterWith(func(c Conflict) bool {
		key := c.dedupKey()
		if seen[key] {
			return false
		}
		seen[key] = true
		return true
	})
}
