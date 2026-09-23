package git

import (
	"bytes"
	"cmp"
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
	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/format/index"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/utils/binary"
	"github.com/go-git/go-git/v6/utils/diff"
	"github.com/go-git/go-git/v6/utils/ioutil"
)

var (
	// ErrMergeConflicts is returned by Worktree.Merge when some paths could
	// not be merged automatically. The conflicts are left in the index and
	// the working tree, and must be resolved before committing the merge.
	ErrMergeConflicts = errors.New("merge conflicts must be resolved before committing")
	// ErrUncommittedChanges is returned by Worktree.Merge when the worktree
	// is not clean.
	ErrUncommittedChanges = errors.New("worktree contains uncommitted changes")
)

// mergeHeadPath is where, relative to the worktree filesystem, the commit
// being merged is recorded until the merge is committed.
var mergeHeadPath = path.Join(GitDirName, "MERGE_HEAD")

const (
	oursMergeLabel = "HEAD"

	// Identity used for merge commits when no user is configured.
	defaultMergeAuthorName  = "go-git"
	defaultMergeAuthorEmail = "go-git@localhost"
)

// Merge joins the history of the target commit into the current branch,
// similar to `git merge <target>`.
//
// If HEAD is an ancestor of target the branch is fast-forwarded, and if target
// is already reachable from HEAD nothing is done. Otherwise a three-way merge
// against the merge base is performed: files changed on one side only are
// taken as they are, and files changed on both sides are merged line by line.
// When everything merges cleanly a merge commit with HEAD and target as
// parents is created. The Strategy of opts must be left to its default value;
// unlike Repository.Merge, Worktree.Merge falls back to a three-way merge when
// a fast-forward is not possible.
//
// When conflicts remain, HEAD is left untouched. Cleanly merged paths are
// updated in the index and the working tree, conflicting files are written
// with conflict markers, their base, ours and theirs versions are recorded in
// the index at stages 1, 2 and 3, target is written to .git/MERGE_HEAD and
// ErrMergeConflicts is returned. Add the resolved files and Commit to conclude
// the merge.
//
// ErrUncommittedChanges is returned if the worktree is not clean.
func (w *Worktree) Merge(target plumbing.Hash, opts *MergeOptions) error {
	if opts == nil {
		opts = &MergeOptions{}
	}

	if opts.Strategy != FastForwardMerge {
		return ErrUnsupportedMergeStrategy
	}

	if err := w.checkCleanForMerge(); err != nil {
		return err
	}

	theirs, err := w.r.CommitObject(target)
	if err != nil {
		return err
	}

	if err := w.removeMergeHead(); err != nil {
		return err
	}

	head, err := w.r.Head()
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		if err := w.updateHEAD(target); err != nil {
			return err
		}

		return w.Reset(&ResetOptions{Mode: MergeReset, Commit: target})
	}

	if err != nil {
		return err
	}

	ours, err := w.r.CommitObject(head.Hash())
	if err != nil {
		return err
	}

	bases, err := ours.MergeBase(theirs)
	if err != nil {
		return err
	}

	for _, b := range bases {
		switch b.Hash {
		case theirs.Hash:
			return nil
		case ours.Hash:
			return w.Reset(&ResetOptions{Mode: MergeReset, Commit: target})
		}
	}

	var base *object.Commit
	if len(bases) > 0 {
		base = bases[0]
	}

	return w.mergeCommits(base, ours, theirs)
}

func (w *Worktree) checkCleanForMerge() error {
	idx, err := w.r.Storer.Index()
	if err != nil {
		return err
	}

	for _, e := range idx.Entries {
		if e.Stage != 0 {
			return ErrUncommittedChanges
		}
	}

	s, err := w.Status()
	if err != nil {
		return err
	}

	if !s.IsClean() {
		return ErrUncommittedChanges
	}

	return nil
}

// mergeCommits performs a three-way merge of theirs into ours, using base as
// the common ancestor. A nil base merges unrelated histories.
func (w *Worktree) mergeCommits(base, ours, theirs *object.Commit) error {
	m := &treeMerge{
		r:           w.r,
		theirsLabel: theirs.Hash.String(),
		result:      make(map[string]object.TreeEntry),
		conflicts:   make(map[string][3]*object.TreeEntry),
		untracked:   make(map[string]bool),
	}

	var err error
	if m.base, err = commitFiles(base); err != nil {
		return err
	}

	if m.ours, err = commitFiles(ours); err != nil {
		return err
	}

	if m.theirs, err = commitFiles(theirs); err != nil {
		return err
	}

	if err := m.merge(); err != nil {
		return err
	}

	tree, err := w.writeMergedTree(m.result)
	if err != nil {
		return err
	}

	if len(m.conflicts) == 0 {
		commit, err := w.createMergeCommit(tree, ours.Hash, theirs.Hash)
		if err != nil {
			return err
		}

		return w.Reset(&ResetOptions{Mode: MergeReset, Commit: commit})
	}

	return w.checkoutMergeConflicts(tree, m, theirs.Hash)
}

func (w *Worktree) writeMergedTree(files map[string]object.TreeEntry) (plumbing.Hash, error) {
	idx := &index.Index{}
	for name, e := range files {
		idx.Entries = append(idx.Entries, &index.Entry{Name: name, Hash: e.Hash, Mode: e.Mode})
	}

	h := &buildTreeHelper{fs: w.Filesystem, s: w.r.Storer}
	return h.BuildTree(idx, nil)
}

func (w *Worktree) createMergeCommit(tree, ours, theirs plumbing.Hash) (plumbing.Hash, error) {
	opts := &CommitOptions{Parents: []plumbing.Hash{ours, theirs}}
	if err := opts.loadConfigAuthorAndCommitter(w.r); err != nil && !errors.Is(err, ErrMissingAuthor) {
		return plumbing.ZeroHash, err
	}

	if opts.Author == nil {
		opts.Author = &object.Signature{
			Name:  defaultMergeAuthorName,
			Email: defaultMergeAuthorEmail,
			When:  time.Now(),
		}
	}

	if opts.Committer == nil {
		opts.Committer = opts.Author
	}

	return w.buildCommitObject(fmt.Sprintf("Merge commit '%s'\n", theirs), opts, tree)
}

// checkoutMergeConflicts brings the index and the working tree to the state
// of a merge with conflicts, without moving HEAD. tree is the working tree
// layout of the merge, conflicted files included.
func (w *Worktree) checkoutMergeConflicts(tree plumbing.Hash, m *treeMerge, target plumbing.Hash) error {
	t, err := w.r.TreeObject(tree)
	if err != nil {
		return err
	}

	removed, err := w.resetIndex(t, nil, nil)
	if err != nil {
		return err
	}

	if len(removed) > 0 {
		if err := w.resetWorktree(t, removed); err != nil {
			return err
		}
	}

	idx, err := w.r.Storer.Index()
	if err != nil {
		return err
	}

	entries := make([]*index.Entry, 0, len(idx.Entries))
	for _, e := range idx.Entries {
		if _, ok := m.conflicts[e.Name]; ok || m.untracked[e.Name] {
			continue
		}

		entries = append(entries, e)
	}

	stages := [3]index.Stage{index.AncestorMode, index.OurMode, index.TheirMode}
	for name, versions := range m.conflicts {
		for i, e := range versions {
			if e == nil {
				continue
			}

			entries = append(entries, &index.Entry{
				Name:  name,
				Hash:  e.Hash,
				Mode:  e.Mode,
				Stage: stages[i],
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Name != entries[j].Name {
			return entries[i].Name < entries[j].Name
		}

		return entries[i].Stage < entries[j].Stage
	})

	idx.Entries = entries
	if err := w.r.Storer.SetIndex(idx); err != nil {
		return err
	}

	if err := w.writeMergeHead(target); err != nil {
		return err
	}

	return ErrMergeConflicts
}

func (w *Worktree) writeMergeHead(h plumbing.Hash) error {
	return util.WriteFile(w.Filesystem, mergeHeadPath, []byte(h.String()+"\n"), 0o644)
}

// readMergeHeads returns the commits recorded in MERGE_HEAD, if any.
func (w *Worktree) readMergeHeads() ([]plumbing.Hash, error) {
	data, err := util.ReadFile(w.Filesystem, mergeHeadPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	var heads []plumbing.Hash
	for _, line := range strings.Fields(string(data)) {
		h, ok := plumbing.FromHex(line)
		if !ok {
			return nil, fmt.Errorf("invalid commit %q in %s", line, mergeHeadPath)
		}

		heads = append(heads, h)
	}

	return heads, nil
}

func (w *Worktree) removeMergeHead() error {
	err := w.Filesystem.Remove(mergeHeadPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	return err
}

// commitFiles returns every non-tree entry reachable from the tree of c,
// keyed by its full path. A nil commit has no files.
func commitFiles(c *object.Commit) (map[string]object.TreeEntry, error) {
	files := make(map[string]object.TreeEntry)
	if c == nil {
		return files, nil
	}

	t, err := c.Tree()
	if err != nil {
		return nil, err
	}

	walker := object.NewTreeWalker(t, true, nil)
	defer walker.Close()

	for {
		name, e, err := walker.Next()
		if err == io.EOF {
			return files, nil
		}

		if err != nil {
			return nil, err
		}

		if e.Mode != filemode.Dir {
			files[name] = e
		}
	}
}

// treeMerge holds the state of a three-way merge of the files of two commits.
type treeMerge struct {
	r           *Repository
	theirsLabel string

	base, ours, theirs map[string]object.TreeEntry

	// result holds the merged files as they are laid out in the working
	// tree, conflicted versions included.
	result map[string]object.TreeEntry
	// conflicts holds the base, ours and theirs versions of every unmerged
	// path. Missing versions are nil.
	conflicts map[string][3]*object.TreeEntry
	// untracked holds result paths that must not be staged: files moved out
	// of the way of a directory.
	untracked map[string]bool
}

func (m *treeMerge) merge() error {
	paths := make(map[string]struct{})
	for _, files := range []map[string]object.TreeEntry{m.base, m.ours, m.theirs} {
		for name := range files {
			paths[name] = struct{}{}
		}
	}

	for name := range paths {
		b, o, t := entryAt(m.base, name), entryAt(m.ours, name), entryAt(m.theirs, name)
		switch {
		case sameEntry(o, t), sameEntry(b, t):
			m.keep(name, o)
		case sameEntry(b, o):
			m.keep(name, t)
		case o == nil || t == nil:
			m.conflict(name, b, o, t, cmp.Or(o, t))
		default:
			if err := m.mergeFile(name, b, o, t); err != nil {
				return err
			}
		}
	}

	m.resolveFileDirectoryClashes()
	return nil
}

func (m *treeMerge) keep(name string, e *object.TreeEntry) {
	if e != nil {
		m.result[name] = *e
	}
}

// conflict records name as unmerged, with worktree as its working tree
// version.
func (m *treeMerge) conflict(name string, b, o, t, worktree *object.TreeEntry) {
	m.conflicts[name] = [3]*object.TreeEntry{b, o, t}
	m.result[name] = *worktree
}

// mergeFile merges a file modified differently on both sides. Only text files
// are merged line by line; for anything else ours is kept in the working tree.
func (m *treeMerge) mergeFile(name string, b, o, t *object.TreeEntry) error {
	if !isTextMode(o.Mode) || !isTextMode(t.Mode) {
		m.conflict(name, b, o, t, o)
		return nil
	}

	var baseData []byte
	if b != nil && isTextMode(b.Mode) {
		var err error
		if baseData, err = m.blobData(b.Hash); err != nil {
			return err
		}
	}

	oursData, err := m.blobData(o.Hash)
	if err != nil {
		return err
	}

	theirsData, err := m.blobData(t.Hash)
	if err != nil {
		return err
	}

	for _, data := range [][]byte{baseData, oursData, theirsData} {
		if isBin, _ := binary.IsBinary(bytes.NewReader(data)); isBin {
			m.conflict(name, b, o, t, o)
			return nil
		}
	}

	merged, clean := mergeLines(string(baseData), string(oursData), string(theirsData), oursMergeLabel, m.theirsLabel)
	h, err := m.storeBlob([]byte(merged))
	if err != nil {
		return err
	}

	mode, modeClean := mergeModes(b, o, t)
	e := &object.TreeEntry{Name: path.Base(name), Mode: mode, Hash: h}
	if clean && modeClean {
		m.keep(name, e)
	} else {
		m.conflict(name, b, o, t, e)
	}

	return nil
}

// resolveFileDirectoryClashes turns every path that the merge left as both a
// file and a directory into a conflict. The directory is kept in the working
// tree, and the file is moved next to it as "<path>~<side>", like git does.
func (m *treeMerge) resolveFileDirectoryClashes() {
	dirs := make(map[string]bool)
	for name := range m.result {
		for d := path.Dir(name); d != "." && !dirs[d]; d = path.Dir(d) {
			dirs[d] = true
		}
	}

	var clashes []string
	for name := range m.result {
		if dirs[name] {
			clashes = append(clashes, name)
		}
	}

	sort.Strings(clashes)
	for _, name := range clashes {
		label := m.theirsLabel
		if _, ok := m.ours[name]; ok {
			label = oursMergeLabel
		}

		aside := uniqueMergePath(name+"~"+label, m.result, dirs)
		m.result[aside] = m.result[name]
		m.untracked[aside] = true
		delete(m.result, name)

		if _, ok := m.conflicts[name]; !ok {
			m.conflicts[name] = [3]*object.TreeEntry{
				entryAt(m.base, name), entryAt(m.ours, name), entryAt(m.theirs, name),
			}
		}
	}
}

func uniqueMergePath(name string, files map[string]object.TreeEntry, dirs map[string]bool) string {
	candidate := name
	for i := 0; ; i++ {
		if _, ok := files[candidate]; !ok && !dirs[candidate] {
			return candidate
		}

		candidate = fmt.Sprintf("%s_%d", name, i)
	}
}

func (m *treeMerge) blobData(h plumbing.Hash) (data []byte, err error) {
	blob, err := m.r.BlobObject(h)
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

func (m *treeMerge) storeBlob(data []byte) (plumbing.Hash, error) {
	obj := m.r.Storer.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	obj.SetSize(int64(len(data)))

	wr, err := obj.Writer()
	if err != nil {
		return plumbing.ZeroHash, err
	}

	if _, err := wr.Write(data); err != nil {
		_ = wr.Close()
		return plumbing.ZeroHash, err
	}

	if err := wr.Close(); err != nil {
		return plumbing.ZeroHash, err
	}

	return m.r.Storer.SetEncodedObject(obj)
}

func entryAt(files map[string]object.TreeEntry, name string) *object.TreeEntry {
	if e, ok := files[name]; ok {
		return &e
	}

	return nil
}

func sameEntry(a, b *object.TreeEntry) bool {
	if a == nil || b == nil {
		return a == b
	}

	return a.Hash == b.Hash && a.Mode == b.Mode
}

func isTextMode(m filemode.FileMode) bool {
	return m == filemode.Regular || m == filemode.Executable || m == filemode.Deprecated
}

// mergeModes returns the mode resulting of the changes made on each side, and
// whether both sides agree on it.
func mergeModes(b, o, t *object.TreeEntry) (filemode.FileMode, bool) {
	switch {
	case o.Mode == t.Mode:
		return o.Mode, true
	case b != nil && b.Mode == o.Mode:
		return t.Mode, true
	case b != nil && b.Mode == t.Mode:
		return o.Mode, true
	default:
		return o.Mode, false
	}
}

// lineHunk is a region of base lines [baseStart, baseEnd) replaced by the
// lines [sideStart, sideEnd) of a modified version.
type lineHunk struct {
	baseStart, baseEnd int
	sideStart, sideEnd int
}

// mergeLines merges the line changes made to base by ours and by theirs.
// Changes that overlap or touch each other, and are not identical, conflict:
// both versions are written delimited by conflict markers, and clean is false.
func mergeLines(base, ours, theirs, oursLabel, theirsLabel string) (merged string, clean bool) {
	baseLines, oursLines, theirsLines := splitLines(base), splitLines(ours), splitLines(theirs)
	oursHunks, theirsHunks := lineHunks(base, ours), lineHunks(base, theirs)

	var out strings.Builder
	clean = true
	pos := 0
	for len(oursHunks) > 0 || len(theirsHunks) > 0 {
		var start, end int
		if len(theirsHunks) == 0 || (len(oursHunks) > 0 && oursHunks[0].baseStart <= theirsHunks[0].baseStart) {
			start, end = oursHunks[0].baseStart, oursHunks[0].baseEnd
		} else {
			start, end = theirsHunks[0].baseStart, theirsHunks[0].baseEnd
		}

		var o, t int
		for {
			if o < len(oursHunks) && oursHunks[o].baseStart <= end {
				end = max(end, oursHunks[o].baseEnd)
				o++
			} else if t < len(theirsHunks) && theirsHunks[t].baseStart <= end {
				end = max(end, theirsHunks[t].baseEnd)
				t++
			} else {
				break
			}
		}

		writeLines(&out, baseLines[pos:start])
		oursPart := sideLines(baseLines, oursLines, oursHunks[:o], start, end)
		theirsPart := sideLines(baseLines, theirsLines, theirsHunks[:t], start, end)
		switch {
		case o == 0:
			writeLines(&out, theirsPart)
		case t == 0, slices.Equal(oursPart, theirsPart):
			writeLines(&out, oursPart)
		default:
			clean = false
			writeConflict(&out, oursPart, theirsPart, oursLabel, theirsLabel)
		}

		pos = end
		oursHunks, theirsHunks = oursHunks[o:], theirsHunks[t:]
	}

	writeLines(&out, baseLines[pos:])
	return out.String(), clean
}

// lineHunks returns the regions of base changed in side.
func lineHunks(base, side string) []lineHunk {
	var hunks []lineHunk
	var b, s int
	inHunk := false
	for _, d := range diff.Do(base, side) {
		n := countLines(d.Text)
		if d.Type == diffmatchpatch.DiffEqual {
			b, s = b+n, s+n
			inHunk = false
			continue
		}

		if !inHunk {
			hunks = append(hunks, lineHunk{baseStart: b, baseEnd: b, sideStart: s, sideEnd: s})
			inHunk = true
		}

		h := &hunks[len(hunks)-1]
		if d.Type == diffmatchpatch.DiffDelete {
			b += n
			h.baseEnd = b
		} else {
			s += n
			h.sideEnd = s
		}
	}

	return hunks
}

// sideLines returns the lines of side replacing the base lines [start, end),
// given the hunks of side within that region.
func sideLines(base, side []string, hunks []lineHunk, start, end int) []string {
	if len(hunks) == 0 {
		return base[start:end]
	}

	first, last := hunks[0], hunks[len(hunks)-1]
	return side[first.sideStart-(first.baseStart-start) : last.sideEnd+(end-last.baseEnd)]
}

func writeConflict(out *strings.Builder, ours, theirs []string, oursLabel, theirsLabel string) {
	prefix := 0
	for prefix < len(ours) && prefix < len(theirs) && ours[prefix] == theirs[prefix] {
		prefix++
	}

	suffix := 0
	for suffix < len(ours)-prefix && suffix < len(theirs)-prefix &&
		ours[len(ours)-1-suffix] == theirs[len(theirs)-1-suffix] {
		suffix++
	}

	writeLines(out, ours[:prefix])
	out.WriteString("<<<<<<< " + oursLabel + "\n")
	writeTerminatedLines(out, ours[prefix:len(ours)-suffix])
	out.WriteString("=======\n")
	writeTerminatedLines(out, theirs[prefix:len(theirs)-suffix])
	out.WriteString(">>>>>>> " + theirsLabel + "\n")
	writeLines(out, ours[len(ours)-suffix:])
}

func writeLines(out *strings.Builder, lines []string) {
	for _, l := range lines {
		out.WriteString(l)
	}
}

// writeTerminatedLines writes lines ensuring the output ends with a newline,
// so a following conflict marker starts on its own line.
func writeTerminatedLines(out *strings.Builder, lines []string) {
	writeLines(out, lines)
	if len(lines) > 0 && !strings.HasSuffix(lines[len(lines)-1], "\n") {
		out.WriteString("\n")
	}
}

// splitLines splits s after every newline, keeping the newlines.
func splitLines(s string) []string {
	var lines []string
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
