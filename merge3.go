package git

import (
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/go-git/go-git/v6/utils/diff"
)

// edit replaces base[start:end] with lines. A pure insertion has start == end.
type edit struct {
	start int
	end   int
	lines []string
}

// mergeText merges base, ours and theirs line-wise.
// label is written on the "theirs" side of conflict markers.
// Non-overlapping edits are combined. Identical edits are taken once.
// Overlapping edits, insertions at the same point, and edits with no
// unchanged line between them are conflicts.
func mergeText(base, ours, theirs, label string) (string, bool) {
	lines, conflict := mergeLines(splitLines(base), splitLines(ours), splitLines(theirs), label)
	return joinLines(lines), conflict
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := make([]string, 0, strings.Count(s, "\n")+1)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func joinLines(lines []string) string {
	return strings.Join(lines, "")
}

func mergeLines(base, ours, theirs []string, label string) ([]string, bool) {
	aEdits := lineEdits(base, ours)
	bEdits := lineEdits(base, theirs)

	out := make([]string, 0, len(base)+len(ours)+len(theirs))
	conflict := false
	ia, ib := 0, 0
	pos := 0

	for ia < len(aEdits) || ib < len(bEdits) {
		hasA := ia < len(aEdits)
		hasB := ib < len(bEdits)

		if hasA && (!hasB || (aEdits[ia].start <= bEdits[ib].start && !editsConflict(aEdits[ia], bEdits[ib]))) {
			e := aEdits[ia]
			out = append(out, base[pos:e.start]...)
			out = append(out, e.lines...)
			pos = e.end
			ia++
			continue
		}
		if hasB && (!hasA || (bEdits[ib].start <= aEdits[ia].start && !editsConflict(bEdits[ib], aEdits[ia]))) {
			e := bEdits[ib]
			out = append(out, base[pos:e.start]...)
			out = append(out, e.lines...)
			pos = e.end
			ib++
			continue
		}

		start := min(aEdits[ia].start, bEdits[ib].start)
		end := max(aEdits[ia].end, bEdits[ib].end)

		tmpA, tmpB := ia, ib
		for {
			grew := false
			if tmpA < len(aEdits) && editOverlapsSpan(aEdits[tmpA], start, end) {
				if aEdits[tmpA].end > end {
					end = aEdits[tmpA].end
				}
				tmpA++
				grew = true
			}
			if tmpB < len(bEdits) && editOverlapsSpan(bEdits[tmpB], start, end) {
				if bEdits[tmpB].end > end {
					end = bEdits[tmpB].end
				}
				tmpB++
				grew = true
			}
			if !grew {
				break
			}
		}
		if tmpA == ia {
			tmpA++
		}
		if tmpB == ib {
			tmpB++
		}

		aText := renderSide(base, aEdits[ia:tmpA], start, end)
		bText := renderSide(base, bEdits[ib:tmpB], start, end)
		out = append(out, base[pos:start]...)
		if linesEqual(aText, bText) {
			out = append(out, aText...)
		} else {
			conflict = true
			out = append(out, conflictLines(aText, bText, label)...)
		}
		pos = end
		ia, ib = tmpA, tmpB
	}

	out = append(out, base[pos:]...)
	return out, conflict
}

func lineEdits(base, other []string) []edit {
	if linesEqual(base, other) {
		return nil
	}
	diffs := diff.Do(joinLines(base), joinLines(other))
	return editsFromDiffs(diffs)
}

func editsFromDiffs(diffs []diffmatchpatch.Diff) []edit {
	edits := make([]edit, 0)
	bLine := 0
	var cur *edit
	flush := func() {
		if cur == nil {
			return
		}
		edits = append(edits, *cur)
		cur = nil
	}

	for _, d := range diffs {
		if d.Text == "" {
			continue
		}
		lines := splitLines(d.Text)
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			flush()
			bLine += len(lines)
		case diffmatchpatch.DiffDelete:
			if cur == nil {
				cur = &edit{start: bLine, end: bLine}
			}
			cur.end += len(lines)
			bLine += len(lines)
		case diffmatchpatch.DiffInsert:
			if cur == nil {
				cur = &edit{start: bLine, end: bLine}
			}
			cur.lines = append(cur.lines, lines...)
		}
	}
	flush()
	return edits
}

func editsOverlap(a, b edit) bool {
	if a.start < b.end && b.start < a.end {
		return true
	}
	if a.start == a.end && b.start == b.end && a.start == b.start {
		return true
	}
	if a.start == a.end && b.start <= a.start && a.start < b.end {
		return true
	}
	if b.start == b.end && a.start <= b.start && b.start < a.end {
		return true
	}
	return false
}

// editsConflict reports whether two edits cannot be applied independently.
// Overlapping edits conflict, as do edits with no unchanged base line between
// them. A pure insertion sits on a boundary: it conflicts with a change that
// ends or starts on that same line.
func editsConflict(a, b edit) bool {
	if editsOverlap(a, b) {
		return true
	}
	return a.end == b.start || b.end == a.start
}

func editOverlapsSpan(e edit, start, end int) bool {
	if e.start == e.end {
		if start == end {
			return e.start == start
		}
		// An insertion at the end boundary belongs to the conflict region.
		return start <= e.start && e.start <= end
	}
	if start == end {
		return e.start <= start && start < e.end
	}
	return e.start < end && start < e.end
}

func renderSide(base []string, edits []edit, start, end int) []string {
	out := make([]string, 0, end-start)
	pos := start
	for _, e := range edits {
		if e.start > pos {
			out = append(out, base[pos:e.start]...)
		}
		out = append(out, e.lines...)
		if e.end > pos {
			pos = e.end
		}
	}
	if pos < end {
		out = append(out, base[pos:end]...)
	}
	return out
}

func linesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// conflictLines emits Git-style markers. Common prefix and suffix lines are
// pulled out of the region so non-overlapping edges of an add/add still merge.
func conflictLines(ours, theirs []string, label string) []string {
	prefix, oursRest, theirsRest, suffix := commonEdges(ours, theirs)
	out := make([]string, 0, len(prefix)+len(oursRest)+len(theirsRest)+len(suffix)+3)
	out = append(out, prefix...)
	if len(oursRest) == 0 && len(theirsRest) == 0 {
		out = append(out, suffix...)
		return out
	}
	out = append(out, "<<<<<<< HEAD\n")
	out = append(out, ensureTrailingNewline(oursRest)...)
	out = append(out, "=======\n")
	out = append(out, ensureTrailingNewline(theirsRest)...)
	out = append(out, ">>>>>>> "+label+"\n")
	out = append(out, suffix...)
	return out
}

func commonEdges(ours, theirs []string) (prefix, oursRest, theirsRest, suffix []string) {
	i := 0
	for i < len(ours) && i < len(theirs) && ours[i] == theirs[i] {
		i++
	}
	prefix = ours[:i]
	ours = ours[i:]
	theirs = theirs[i:]

	j := 0
	for j < len(ours) && j < len(theirs) && ours[len(ours)-1-j] == theirs[len(theirs)-1-j] {
		j++
	}
	if j > 0 {
		suffix = ours[len(ours)-j:]
		ours = ours[:len(ours)-j]
		theirs = theirs[:len(theirs)-j]
	}
	return prefix, ours, theirs, suffix
}

func ensureTrailingNewline(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	last := lines[len(lines)-1]
	if strings.HasSuffix(last, "\n") {
		return lines
	}
	out := make([]string, len(lines))
	copy(out, lines)
	out[len(out)-1] = last + "\n"
	return out
}
