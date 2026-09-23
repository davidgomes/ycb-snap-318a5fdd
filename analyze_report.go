//go:build analyze

package participle

import (
	"fmt"
	"strings"
)

// ConflictType classifies an ambiguity detected by grammar analysis.
type ConflictType int

const (
	// ConflictFirstFirst indicates that two alternatives of a disjunction can start with the same token.
	ConflictFirstFirst ConflictType = iota
	// ConflictFirstFollow indicates that an optional or repeated group can start with a token that can also
	// follow it.
	ConflictFirstFollow
	// ConflictUnreachable indicates an alternative that can never match because an identical earlier
	// alternative always takes precedence.
	ConflictUnreachable
)

var conflictTypes = []ConflictType{ConflictFirstFirst, ConflictFirstFollow, ConflictUnreachable}

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
	// SeverityWarning marks an ambiguity that the parser resolves by ordered choice, possibly not as intended.
	SeverityWarning Severity = iota
	// SeverityError marks grammar that can never match.
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

// ConflictLocation identifies where in the grammar a Conflict originates.
type ConflictLocation struct {
	// TypeName is the name of the innermost Go type (struct or union interface) containing the conflict.
	TypeName string
	// FieldName is the struct field the conflict is attributed to, if one could be determined.
	FieldName string
}

func (l ConflictLocation) String() string {
	if l.FieldName == "" {
		return l.TypeName
	}
	return l.TypeName + "." + l.FieldName
}

// Conflict describes a single ambiguity detected in a grammar.
type Conflict struct {
	Type     ConflictType
	Severity Severity
	// Message is a human readable description of the conflict.
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

// AnalysisReport is the result of analysing a grammar.
//
// Methods on AnalysisReport never modify the receiver; they return new values.
type AnalysisReport struct {
	Conflicts []Conflict
}

func (r *AnalysisReport) conflicts() []Conflict {
	if r == nil {
		return nil
	}
	return r.Conflicts
}

// Errors returns conflicts with SeverityError.
func (r *AnalysisReport) Errors() []Conflict {
	return r.FilterWith(func(c Conflict) bool { return c.Severity == SeverityError }).Conflicts
}

// Warnings returns conflicts with SeverityWarning.
func (r *AnalysisReport) Warnings() []Conflict {
	return r.FilterWith(func(c Conflict) bool { return c.Severity == SeverityWarning }).Conflicts
}

// FilterByType returns a report containing only conflicts of type t.
func (r *AnalysisReport) FilterByType(t ConflictType) *AnalysisReport {
	return r.FilterWith(func(c Conflict) bool { return c.Type == t })
}

// FilterWith returns a report containing only the conflicts for which keep returns true, in their original order.
func (r *AnalysisReport) FilterWith(keep func(Conflict) bool) *AnalysisReport {
	out := make([]Conflict, 0, len(r.conflicts()))
	for _, c := range r.conflicts() {
		if keep == nil || keep(c) {
			out = append(out, c)
		}
	}
	return &AnalysisReport{Conflicts: out}
}

// ConflictCount returns the number of conflicts of type t.
func (r *AnalysisReport) ConflictCount(t ConflictType) int {
	count := 0
	for _, c := range r.conflicts() {
		if c.Type == t {
			count++
		}
	}
	return count
}

// HasType returns true if the report contains at least one conflict of type t.
func (r *AnalysisReport) HasType(t ConflictType) bool {
	return r.ConflictCount(t) > 0
}

// IsClean returns true if no conflicts were detected.
func (r *AnalysisReport) IsClean() bool {
	return len(r.conflicts()) == 0
}

// Summary returns a one line summary of the report.
func (r *AnalysisReport) Summary() string {
	if r.IsClean() {
		return "no conflicts detected"
	}
	counts := make([]string, 0, len(conflictTypes))
	for _, t := range conflictTypes {
		counts = append(counts, fmt.Sprintf("%d %s", r.ConflictCount(t), t))
	}
	return fmt.Sprintf("%d conflict(s): %s", len(r.conflicts()), strings.Join(counts, ", "))
}

func (r *AnalysisReport) String() string {
	w := &strings.Builder{}
	fmt.Fprintf(w, "grammar analysis report\n  %s\n", r.Summary())
	for i, c := range r.conflicts() {
		fmt.Fprintf(w, "\n  %d. %s\n", i+1, c)
		fmt.Fprintf(w, "     grammar:    %s\n", c.GrammarSnippet)
		fmt.Fprintf(w, "     example:    %s\n", c.Example)
		fmt.Fprintf(w, "     suggestion: %s\n", c.Suggestion)
	}
	return w.String()
}

// Merge returns a report containing the conflicts of both reports, without duplicates.
//
// Conflicts are considered duplicates if they have the same type, location and grammar snippet.
func (r *AnalysisReport) Merge(other *AnalysisReport) *AnalysisReport {
	combined := make([]Conflict, 0, len(r.conflicts())+len(other.conflicts()))
	combined = append(combined, r.conflicts()...)
	combined = append(combined, other.conflicts()...)
	return (&AnalysisReport{Conflicts: combined}).Dedup()
}

// Dedup returns a report with duplicate conflicts removed, keeping the first occurrence of each.
//
// Conflicts are considered duplicates if they have the same type, location and grammar snippet.
func (r *AnalysisReport) Dedup() *AnalysisReport {
	type key struct {
		typ      ConflictType
		location string
		snippet  string
	}
	seen := make(map[key]bool, len(r.conflicts()))
	return r.FilterWith(func(c Conflict) bool {
		k := key{c.Type, c.Location.String(), c.GrammarSnippet}
		if seen[k] {
			return false
		}
		seen[k] = true
		return true
	})
}
