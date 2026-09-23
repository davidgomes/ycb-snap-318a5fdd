package git

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-billy/v6/util"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/format/index"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/utils/binary"
	"github.com/go-git/go-git/v6/utils/ioutil"
)

var (
	// ErrMergeConflicts is returned by Worktree.Merge when the merge could not
	// be completed automatically. The conflicts are recorded in the index and
	// in the working tree, and the merged commit is stored in MERGE_HEAD.
	ErrMergeConflicts = errors.New("merge conflicts")
	// ErrUncommittedChanges is returned by Worktree.Merge when the worktree
	// contains uncommitted changes.
	ErrUncommittedChanges = errors.New("worktree contains uncommitted changes")
	// ErrUnmergedPaths is returned by Worktree.Commit when the index still
	// contains conflict entries.
	ErrUnmergedPaths = errors.New("cannot commit: index contains unmerged paths")
)

var mergeHeadFile = path.Join(GitDirName, "MERGE_HEAD")

const (
	conflictMarkerOurs   = "<<<<<<<"
	conflictMarkerSep    = "======="
	conflictMarkerTheirs = ">>>>>>>"
	oursLabel            = "HEAD"
)

// defaultMergeSignature is used for merge commits when no identity is
// configured for the repository.
var defaultMergeSignature = object.Signature{
	Name:  "go-git",
	Email: "go-git@localhost",
}

// Merge merges the given commit into the current HEAD, updating the index
// and the working tree.
//
// With the default FastForwardMerge strategy, HEAD is fast-forwarded when
// possible; otherwise a 3-way merge is performed and, if it succeeds, a merge
// commit is created. If the merge results in conflicts, conflict markers are
// written to the affected files, the conflicting stages are recorded in the
// index, the target is written to .git/MERGE_HEAD and ErrMergeConflicts is
// returned. The merge can then be concluded with Add and Commit.
//
// ErrUncommittedChanges is returned if the worktree is not clean.
func (w *Worktree) Merge(target plumbing.Hash, opts *MergeOptions) error {
	if opts == nil {
		opts = &MergeOptions{}
	}

	if opts.Strategy != FastForwardMerge {
		return ErrUnsupportedMergeStrategy
	}

	status, err := w.Status()
	if err != nil {
		return err
	}

	if !status.IsClean() {
		return ErrUncommittedChanges
	}

	theirsCommit, err := w.r.CommitObject(target)
	if err != nil {
		return err
	}

	head, err := w.r.Head()
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return w.fastForwardMerge(target)
	}
	if err != nil {
		return err
	}

	oursCommit, err := w.r.CommitObject(head.Hash())
	if err != nil {
		return err
	}

	if oursCommit.Hash == theirsCommit.Hash {
		return nil
	}

	bases, err := oursCommit.MergeBase(theirsCommit)
	if err != nil {
		return err
	}

	var baseCommit *object.Commit
	for _, b := range bases {
		if b.Hash == theirsCommit.Hash {
			return nil
		}

		if b.Hash == oursCommit.Hash {
			return w.fastForwardMerge(target)
		}
	}

	if len(bases) > 0 {
		baseCommit = bases[0]
	}

	return w.threeWayMerge(baseCommit, oursCommit, theirsCommit)
}

func (w *Worktree) fastForwardMerge(target plumbing.Hash) error {
	if err := w.updateHEAD(target); err != nil {
		return err
	}

	return w.Reset(&ResetOptions{Commit: target, Mode: HardReset})
}

// mergePath holds the outcome of merging a single path.
type mergePath struct {
	name string
	// result is the cleanly merged entry, nil if the path is deleted or
	// conflicted.
	result *object.TreeEntry

	conflict           bool
	base, ours, theirs *object.TreeEntry
	// worktree is the entry written to the working tree for a conflict,
	// ignored when content is set.
	worktree *object.TreeEntry
	// content, when not nil, is written to the working tree for a conflict.
	content []byte
	// worktreeName is where the conflicted file is written in the working
	// tree, when it differs from name.
	worktreeName string
}

func (m *mergePath) worktreeEntry() *object.TreeEntry {
	if m.conflict {
		return m.worktree
	}

	return m.result
}

func (m *mergePath) worktreePath() string {
	if m.worktreeName != "" {
		return m.worktreeName
	}

	return m.name
}

func (w *Worktree) threeWayMerge(base, ours, theirs *object.Commit) error {
	baseFiles, err := w.mergeTreeFiles(base)
	if err != nil {
		return err
	}

	oursFiles, err := w.mergeTreeFiles(ours)
	if err != nil {
		return err
	}

	theirsFiles, err := w.mergeTreeFiles(theirs)
	if err != nil {
		return err
	}

	names := make([]string, 0, len(oursFiles)+len(theirsFiles))
	for _, files := range []map[string]*object.TreeEntry{baseFiles, oursFiles, theirsFiles} {
		for name := range files {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	names = slices.Compact(names)

	theirsLabel := theirs.Hash.String()

	merged := make(map[string]*mergePath, len(names))
	for _, name := range names {
		mp, err := w.mergeFile(name, baseFiles[name], oursFiles[name], theirsFiles[name], theirsLabel)
		if err != nil {
			return err
		}

		merged[name] = mp
	}

	resolveFileDirectoryConflicts(names, merged, theirsLabel)

	if err := w.applyMerge(names, merged, oursFiles); err != nil {
		return err
	}

	for _, name := range names {
		if merged[name].conflict {
			if err := w.writeMergeHead(theirs.Hash); err != nil {
				return err
			}

			return ErrMergeConflicts
		}
	}

	return w.commitMerge(ours.Hash, theirs.Hash)
}

func (w *Worktree) mergeTreeFiles(c *object.Commit) (map[string]*object.TreeEntry, error) {
	files := make(map[string]*object.TreeEntry)
	if c == nil {
		return files, nil
	}

	tree, err := c.Tree()
	if err != nil {
		return nil, err
	}

	walker := object.NewTreeWalker(tree, true, nil)
	defer walker.Close()

	for {
		name, entry, err := walker.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}

		if entry.Mode == filemode.Dir {
			continue
		}

		files[name] = &entry
	}

	return files, nil
}

func sameTreeEntry(a, b *object.TreeEntry) bool {
	if a == nil || b == nil {
		return a == b
	}

	return a.Hash == b.Hash && a.Mode == b.Mode
}

func isRegularFileMode(m filemode.FileMode) bool {
	return m == filemode.Regular || m == filemode.Deprecated || m == filemode.Executable
}

func (w *Worktree) mergeFile(name string, base, ours, theirs *object.TreeEntry, theirsLabel string) (*mergePath, error) {
	mp := &mergePath{name: name, base: base, ours: ours, theirs: theirs}

	switch {
	case sameTreeEntry(ours, theirs), sameTreeEntry(base, theirs):
		mp.result = ours
		return mp, nil
	case sameTreeEntry(base, ours):
		mp.result = theirs
		return mp, nil
	}

	mp.conflict = true
	mp.worktree = ours
	if ours == nil {
		mp.worktree = theirs
	}

	if ours == nil || theirs == nil || !isRegularFileMode(ours.Mode) || !isRegularFileMode(theirs.Mode) {
		return mp, nil
	}

	mode, modeClean := mergeFileMode(base, ours, theirs)

	var baseContent []byte
	if base != nil && isRegularFileMode(base.Mode) {
		var err error
		if baseContent, err = w.blobContent(base.Hash); err != nil {
			return nil, err
		}
	}

	oursContent, err := w.blobContent(ours.Hash)
	if err != nil {
		return nil, err
	}

	theirsContent, err := w.blobContent(theirs.Hash)
	if err != nil {
		return nil, err
	}

	for _, content := range [][]byte{baseContent, oursContent, theirsContent} {
		bin, err := binary.IsBinary(bytes.NewReader(content))
		if err != nil {
			return nil, err
		}

		if bin {
			return mp, nil
		}
	}

	content, contentClean := mergeText(baseContent, oursContent, theirsContent, oursLabel, theirsLabel)
	if contentClean && modeClean {
		h, err := w.storeBlob(content)
		if err != nil {
			return nil, err
		}

		mp.conflict = false
		mp.worktree = nil
		mp.result = &object.TreeEntry{Name: ours.Name, Mode: mode, Hash: h}
		return mp, nil
	}

	mp.worktree = &object.TreeEntry{Name: ours.Name, Mode: mode}
	mp.content = content
	return mp, nil
}

func mergeFileMode(base, ours, theirs *object.TreeEntry) (filemode.FileMode, bool) {
	switch {
	case ours.Mode == theirs.Mode:
		return ours.Mode, true
	case base != nil && base.Mode == ours.Mode:
		return theirs.Mode, true
	case base != nil && base.Mode == theirs.Mode:
		return ours.Mode, true
	}

	return ours.Mode, false
}

// resolveFileDirectoryConflicts detects names that are a file on one side and
// a directory on the other. The file is turned into a conflict and written to
// the working tree under an alternative name, leaving room for the directory.
func resolveFileDirectoryConflicts(names []string, merged map[string]*mergePath, theirsLabel string) {
	occupied := make(map[string]bool, len(names))
	for _, name := range names {
		if merged[name].worktreeEntry() != nil {
			occupied[name] = true
		}
	}

	for _, name := range names {
		if !occupied[name] {
			continue
		}

		for dir := path.Dir(name); dir != "."; dir = path.Dir(dir) {
			mp, ok := merged[dir]
			if !ok || !occupied[dir] || mp.worktreeName != "" {
				continue
			}

			if !mp.conflict {
				mp.conflict = true
				mp.worktree = mp.result
				mp.result = nil
			}

			label := oursLabel
			if mp.ours == nil {
				label = theirsLabel
			}

			alt := dir + "~" + label
			for i := 1; occupied[alt]; i++ {
				alt = fmt.Sprintf("%s~%s_%d", dir, label, i)
			}

			mp.worktreeName = alt
			occupied[alt] = true
		}
	}
}

// applyMerge updates the working tree and the index, which are expected to
// match the ours tree, to the merge outcome.
func (w *Worktree) applyMerge(names []string, merged map[string]*mergePath, ours map[string]*object.TreeEntry) error {
	unchanged := func(mp *mergePath) bool {
		return !mp.conflict && sameTreeEntry(mp.result, ours[mp.name])
	}

	for _, name := range names {
		mp := merged[name]
		if ours[name] == nil || unchanged(mp) {
			continue
		}

		if err := w.removeMergedFile(name); err != nil {
			return err
		}
	}

	for _, name := range names {
		mp := merged[name]
		if mp.worktreeEntry() == nil || unchanged(mp) {
			continue
		}

		if err := w.writeMergedFile(mp); err != nil {
			return err
		}
	}

	idx, err := w.r.Storer.Index()
	if err != nil {
		return err
	}

	current := make(map[string]*index.Entry, len(idx.Entries))
	for _, e := range idx.Entries {
		if e.Stage == 0 {
			current[e.Name] = e
		}
	}

	entries := make([]*index.Entry, 0, len(names))
	for _, name := range names {
		mp := merged[name]

		if mp.conflict {
			for stage, e := range map[index.Stage]*object.TreeEntry{
				index.AncestorMode: mp.base,
				index.OurMode:      mp.ours,
				index.TheirMode:    mp.theirs,
			} {
				if e != nil {
					entries = append(entries, &index.Entry{Name: name, Hash: e.Hash, Mode: e.Mode, Stage: stage})
				}
			}
			continue
		}

		if mp.result == nil {
			continue
		}

		if e, ok := current[name]; ok && unchanged(mp) {
			entries = append(entries, e)
			continue
		}

		e, err := w.mergedIndexEntry(name, mp.result)
		if err != nil {
			return err
		}
		entries = append(entries, e)
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Name != entries[j].Name {
			return entries[i].Name < entries[j].Name
		}
		return entries[i].Stage < entries[j].Stage
	})

	idx.Entries = entries
	idx.Cache = nil

	return w.r.Storer.SetIndex(idx)
}

// removeMergedFile removes a file and its parent directories left empty,
// without ever removing the worktree root.
func (w *Worktree) removeMergedFile(name string) error {
	if err := util.RemoveAll(w.Filesystem, name); err != nil {
		return err
	}

	for dir := path.Dir(name); dir != "."; dir = path.Dir(dir) {
		removed, err := removeDirIfEmpty(w.Filesystem, dir)
		if err != nil && !os.IsNotExist(err) {
			return err
		}

		if !removed {
			break
		}
	}

	return nil
}

func (w *Worktree) writeMergedFile(mp *mergePath) error {
	name := mp.worktreePath()
	entry := mp.worktreeEntry()

	if entry.Mode == filemode.Submodule {
		return w.Filesystem.MkdirAll(name, 0o755)
	}

	if mp.conflict && mp.content != nil {
		mode, err := entry.Mode.ToOSFileMode()
		if err != nil {
			return err
		}

		return util.WriteFile(w.Filesystem, name, mp.content, mode.Perm())
	}

	blob, err := w.r.BlobObject(entry.Hash)
	if err != nil {
		return err
	}

	return w.checkoutFile(object.NewFile(name, entry.Mode, blob))
}

func (w *Worktree) mergedIndexEntry(name string, entry *object.TreeEntry) (*index.Entry, error) {
	e := &index.Entry{Name: name, Hash: entry.Hash, Mode: entry.Mode}
	if entry.Mode == filemode.Submodule {
		return e, nil
	}

	fi, err := w.Filesystem.Lstat(name)
	if err != nil {
		return nil, err
	}

	e.ModifiedAt = fi.ModTime()
	e.Size = uint32(fi.Size())
	if fillSystemInfo != nil {
		fillSystemInfo(e, fi.Sys())
	}

	return e, nil
}

func (w *Worktree) commitMerge(ours, theirs plumbing.Hash) error {
	idx, err := w.r.Storer.Index()
	if err != nil {
		return err
	}

	h := &buildTreeHelper{fs: w.Filesystem, s: w.r.Storer}
	tree, err := h.BuildTree(idx, nil)
	if err != nil {
		return err
	}

	opts := &CommitOptions{Parents: []plumbing.Hash{ours, theirs}}
	if err := opts.loadConfigAuthorAndCommitter(w.r); err != nil && !errors.Is(err, ErrMissingAuthor) {
		return err
	}

	if opts.Author == nil {
		sig := defaultMergeSignature
		sig.When = time.Now()
		opts.Author = &sig
	}

	if opts.Committer == nil {
		opts.Committer = opts.Author
	}

	commit, err := w.buildCommitObject(fmt.Sprintf("Merge commit '%s'\n", theirs), opts, tree)
	if err != nil {
		return err
	}

	return w.updateHEAD(commit)
}

func (w *Worktree) writeMergeHead(h plumbing.Hash) error {
	return util.WriteFile(w.Filesystem, mergeHeadFile, []byte(h.String()+"\n"), 0o644)
}

// readMergeHead returns the hash stored in .git/MERGE_HEAD, or the zero hash
// if no merge is in progress.
func (w *Worktree) readMergeHead() (plumbing.Hash, error) {
	content, err := util.ReadFile(w.Filesystem, mergeHeadFile)
	if os.IsNotExist(err) {
		return plumbing.ZeroHash, nil
	}
	if err != nil {
		return plumbing.ZeroHash, err
	}

	line, _, _ := strings.Cut(strings.TrimSpace(string(content)), "\n")
	h, ok := plumbing.FromHex(strings.TrimSpace(line))
	if !ok {
		return plumbing.ZeroHash, fmt.Errorf("invalid %s: %q", mergeHeadFile, line)
	}

	return h, nil
}

func (w *Worktree) removeMergeHead() error {
	err := w.Filesystem.Remove(mergeHeadFile)
	if os.IsNotExist(err) {
		return nil
	}

	return err
}

func (w *Worktree) blobContent(h plumbing.Hash) (content []byte, err error) {
	blob, err := w.r.BlobObject(h)
	if err != nil {
		return nil, err
	}

	r, err := blob.Reader()
	if err != nil {
		return nil, err
	}
	defer ioutil.CheckClose(r, &err)

	return io.ReadAll(r)
}

func (w *Worktree) storeBlob(content []byte) (h plumbing.Hash, err error) {
	obj := w.r.Storer.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	obj.SetSize(int64(len(content)))

	writer, err := obj.Writer()
	if err != nil {
		return plumbing.ZeroHash, err
	}

	if _, err := writer.Write(content); err != nil {
		_ = writer.Close()
		return plumbing.ZeroHash, err
	}

	if err := writer.Close(); err != nil {
		return plumbing.ZeroHash, err
	}

	return w.r.Storer.SetEncodedObject(obj)
}

// hasUnmergedEntries reports whether the index contains conflict stages for
// the given path.
func hasUnmergedEntries(idx *index.Index, name string) bool {
	for _, e := range idx.Entries {
		if e.Stage != 0 && e.Name == name {
			return true
		}
	}

	return false
}

// removeUnmergedEntries removes the conflict stages of the given path from
// the index.
func removeUnmergedEntries(idx *index.Index, name string) {
	idx.Entries = slices.DeleteFunc(idx.Entries, func(e *index.Entry) bool {
		return e.Stage != 0 && e.Name == name
	})
}

// diffHunk is a changed region: base[baseStart:baseEnd] is replaced by
// side[sideStart:sideEnd].
type diffHunk struct {
	baseStart, baseEnd int
	sideStart, sideEnd int
}

// splitLines splits content into lines, keeping the line terminators.
func splitLines(content []byte) []string {
	var lines []string
	s := string(content)
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			lines = append(lines, s)
			break
		}

		lines = append(lines, s[:i+1])
		s = s[i+1:]
	}

	return lines
}

// diffLines returns the hunks needed to turn a into b. Changes are computed
// with the Myers algorithm and then shifted like git does, so that ambiguous
// changes among repeated lines are placed consistently.
func diffLines(a, b []string) []diffHunk {
	changedA, changedB := myersChanges(a, b)
	compactChanges(a, changedA, changedB)
	compactChanges(b, changedB, changedA)

	var hunks []diffHunk
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		if i < len(a) && j < len(b) && !changedA[i] && !changedB[j] {
			i++
			j++
			continue
		}

		h := diffHunk{baseStart: i, sideStart: j}
		for i < len(a) && changedA[i] {
			i++
		}
		for j < len(b) && changedB[j] {
			j++
		}
		h.baseEnd, h.sideEnd = i, j
		hunks = append(hunks, h)
	}

	return hunks
}

// myersChanges reports, for each line of a and b, whether it is removed from
// a or added in b.
func myersChanges(a, b []string) (changedA, changedB []bool) {
	changedA = make([]bool, len(a))
	changedB = make([]bool, len(b))

	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}

	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}

	ta, tb := a[prefix:len(a)-suffix], b[prefix:len(b)-suffix]
	n, m := len(ta), len(tb)
	if n == 0 || m == 0 {
		for i := range n {
			changedA[prefix+i] = true
		}
		for j := range m {
			changedB[prefix+j] = true
		}
		return changedA, changedB
	}

	// v[k+offset] holds the furthest x reached on diagonal k; trace keeps
	// v[-d..d] as it was before step d, for backtracking.
	maxD := n + m
	offset := maxD + 1
	v := make([]int, 2*maxD+3)
	var trace [][]int

search:
	for d := 0; d <= maxD; d++ {
		trace = append(trace, slices.Clone(v[offset-d:offset+d+1]))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1]
			} else {
				x = v[offset+k-1] + 1
			}

			y := x - k
			for x < n && y < m && ta[x] == tb[y] {
				x++
				y++
			}

			v[offset+k] = x
			if x >= n && y >= m {
				break search
			}
		}
	}

	for i := range n {
		changedA[prefix+i] = true
	}
	for j := range m {
		changedB[prefix+j] = true
	}

	x, y := n, m
	for d := len(trace) - 1; d >= 0 && (x > 0 || y > 0); d-- {
		prev := trace[d]
		at := func(k int) int { return prev[k+d] }
		k := x - y

		prevK := 0
		switch {
		case d == 0:
		case k == -d || (k != d && at(k-1) < at(k+1)):
			prevK = k + 1
		default:
			prevK = k - 1
		}

		prevX := 0
		if d > 0 {
			prevX = at(prevK)
		}
		prevY := prevX - prevK

		for x > prevX && y > prevY {
			x--
			y--
			changedA[prefix+x] = false
			changedB[prefix+y] = false
		}

		x, y = prevX, prevY
	}

	return changedA, changedB
}

// changeGroup is a run of changed lines, possibly empty.
type changeGroup struct {
	start, end int
}

// compactChanges slides groups of changed lines in lines as far down as
// possible, unless they can be aligned with a change in the other file. This
// mirrors xdl_change_compact from git without the indent heuristic.
func compactChanges(lines []string, changed, otherChanged []bool) {
	at := func(c []bool, i int) bool { return i >= 0 && i < len(c) && c[i] }

	initGroup := func(c []bool) changeGroup {
		g := changeGroup{}
		for at(c, g.end) {
			g.end++
		}
		return g
	}
	nextGroup := func(c []bool, g *changeGroup) bool {
		if g.end == len(c) {
			return false
		}
		g.start = g.end + 1
		for g.end = g.start; at(c, g.end); g.end++ {
		}
		return true
	}
	previousGroup := func(c []bool, g *changeGroup) bool {
		if g.start == 0 {
			return false
		}
		g.end = g.start - 1
		for g.start = g.end; at(c, g.start-1); g.start-- {
		}
		return true
	}
	slideDown := func(g *changeGroup) bool {
		if g.end < len(lines) && lines[g.start] == lines[g.end] {
			changed[g.start] = false
			changed[g.end] = true
			g.start++
			g.end++
			for at(changed, g.end) {
				g.end++
			}
			return true
		}
		return false
	}
	slideUp := func(g *changeGroup) bool {
		if g.start > 0 && lines[g.start-1] == lines[g.end-1] {
			g.start--
			g.end--
			changed[g.start] = true
			changed[g.end] = false
			for at(changed, g.start-1) {
				g.start--
			}
			return true
		}
		return false
	}

	g := initGroup(changed)
	og := initGroup(otherChanged)
	for {
		if g.end != g.start {
			var earliestEnd int
			endMatchingOther := -1
			for {
				size := g.end - g.start

				for slideUp(&g) {
					previousGroup(otherChanged, &og)
				}

				earliestEnd = g.end
				endMatchingOther = -1
				if og.end > og.start {
					endMatchingOther = g.end
				}

				for slideDown(&g) {
					nextGroup(otherChanged, &og)
					if og.end > og.start {
						endMatchingOther = g.end
					}
				}

				if size == g.end-g.start {
					break
				}
			}

			if g.end != earliestEnd && endMatchingOther != -1 {
				for og.end == og.start {
					slideUp(&g)
					previousGroup(otherChanged, &og)
				}
			}
		}

		if !nextGroup(changed, &g) {
			break
		}
		nextGroup(otherChanged, &og)
	}
}

// mergeText performs a line based 3-way merge. Changes from both sides that
// touch the same region of base and differ are reported as conflicts,
// delimited by conflict markers in the returned content.
func mergeText(base, ours, theirs []byte, oursName, theirsName string) ([]byte, bool) {
	baseLines := splitLines(base)
	oursLines := splitLines(ours)
	theirsLines := splitLines(theirs)

	sides := [2]struct {
		lines []string
		hunks []diffHunk
	}{
		{oursLines, diffLines(baseLines, oursLines)},
		{theirsLines, diffLines(baseLines, theirsLines)},
	}

	var out bytes.Buffer
	write := func(lines []string) {
		for _, l := range lines {
			out.WriteString(l)
		}
	}
	writeSection := func(lines []string) {
		write(lines)
		if len(lines) > 0 && !strings.HasSuffix(lines[len(lines)-1], "\n") {
			out.WriteByte('\n')
		}
	}

	clean := true
	next := [2]int{}
	pos := 0
	for next[0] < len(sides[0].hunks) || next[1] < len(sides[1].hunks) {
		first := 0
		if next[0] >= len(sides[0].hunks) ||
			(next[1] < len(sides[1].hunks) && sides[1].hunks[next[1]].baseStart < sides[0].hunks[next[0]].baseStart) {
			first = 1
		}

		// Hunks of both sides are grouped while they overlap or touch, as
		// git does; a group with hunks from both sides may conflict.
		var groups [2][]diffHunk
		gStart := sides[first].hunks[next[first]].baseStart
		gEnd := gStart
		for added := true; added; {
			added = false
			for s := range sides {
				for next[s] < len(sides[s].hunks) && sides[s].hunks[next[s]].baseStart <= gEnd {
					h := sides[s].hunks[next[s]]
					groups[s] = append(groups[s], h)
					gEnd = max(gEnd, h.baseEnd)
					next[s]++
					added = true
				}
			}
		}

		write(baseLines[pos:gStart])
		pos = gEnd

		var chunks [2][]string
		for s := range sides {
			hs := groups[s]
			if len(hs) == 0 {
				chunks[s] = baseLines[gStart:gEnd]
				continue
			}

			start := hs[0].sideStart - (hs[0].baseStart - gStart)
			end := hs[len(hs)-1].sideEnd + (gEnd - hs[len(hs)-1].baseEnd)
			chunks[s] = sides[s].lines[start:end]
		}

		switch {
		case len(groups[1]) == 0:
			write(chunks[0])
			continue
		case len(groups[0]) == 0, slices.Equal(chunks[0], chunks[1]):
			write(chunks[1])
			continue
		}

		o, t := chunks[0], chunks[1]
		prefix := 0
		for prefix < len(o) && prefix < len(t) && o[prefix] == t[prefix] {
			prefix++
		}

		suffix := 0
		for suffix < len(o)-prefix && suffix < len(t)-prefix && o[len(o)-1-suffix] == t[len(t)-1-suffix] {
			suffix++
		}

		clean = false
		write(o[:prefix])
		out.WriteString(conflictMarkerOurs + " " + oursName + "\n")
		writeSection(o[prefix : len(o)-suffix])
		out.WriteString(conflictMarkerSep + "\n")
		writeSection(t[prefix : len(t)-suffix])
		out.WriteString(conflictMarkerTheirs + " " + theirsName + "\n")
		write(o[len(o)-suffix:])
	}

	write(baseLines[pos:])

	return out.Bytes(), clean
}
