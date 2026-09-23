package git

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-billy/v6/util"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/format/index"
	"github.com/go-git/go-git/v6/plumbing/object"
)

var (
	// ErrMergeConflicts is returned by Worktree.Merge when the merge could not
	// be completed automatically. The conflicting files contain conflict
	// markers, the index holds the conflict stages and MERGE_HEAD is written.
	ErrMergeConflicts = errors.New("merge conflicts")
	// ErrUncommittedChanges is returned by Worktree.Merge when the worktree or
	// the index contain changes that are not committed.
	ErrUncommittedChanges = errors.New("worktree contains uncommitted changes")
)

var mergeHeadPath = path.Join(GitDirName, "MERGE_HEAD")

const (
	conflictMarkerOurs   = "<<<<<<<"
	conflictMarkerSep    = "======="
	conflictMarkerTheirs = ">>>>>>>"
)

// Merge merges the given commit into the current HEAD.
//
// If HEAD is an ancestor of target, HEAD is fast-forwarded. Otherwise a
// three-way merge is performed against the merge base and, if it completes
// without conflicts, a merge commit with HEAD and target as parents is
// created.
//
// When conflicts are found, the working tree files receive conflict markers,
// the index records the conflicting versions in stages 1 (base), 2 (ours) and
// 3 (theirs), MERGE_HEAD is written and ErrMergeConflicts is returned. The
// merge can then be concluded by resolving the conflicts, adding the files and
// calling Commit.
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

	head, err := w.r.Head()
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		if err := w.updateHEAD(target); err != nil {
			return err
		}
		return w.Reset(&ResetOptions{Commit: target, Mode: MergeReset})
	}
	if err != nil {
		return err
	}

	if head.Hash() == target {
		return nil
	}

	ours, err := w.r.CommitObject(head.Hash())
	if err != nil {
		return err
	}

	upToDate, err := theirs.IsAncestor(ours)
	if err != nil {
		return err
	}
	if upToDate {
		return nil
	}

	fastForward, err := ours.IsAncestor(theirs)
	if err != nil {
		return err
	}
	if fastForward {
		return w.Reset(&ResetOptions{Commit: target, Mode: MergeReset})
	}

	return w.threeWayMerge(ours, theirs, opts)
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

	for _, fs := range s {
		if fs.Staging == Untracked && fs.Worktree == Untracked {
			continue
		}

		if fs.Staging != Unmodified || fs.Worktree != Unmodified {
			return ErrUncommittedChanges
		}
	}

	return nil
}

type mergeResult struct {
	path string

	// entry is the merged entry, nil when the path is deleted. For conflicts
	// it is the entry that is materialized in the working tree.
	entry *object.TreeEntry

	conflict bool
	// base, ours and theirs are the versions recorded in stages 1, 2 and 3.
	base, ours, theirs *object.TreeEntry
	// content, when not nil, replaces the blob of entry in the working tree.
	content []byte
	// worktreePath is where entry is written, when it differs from path.
	worktreePath string
}

func (w *Worktree) threeWayMerge(ours, theirs *object.Commit, opts *MergeOptions) error {
	var baseEntries map[string]*object.TreeEntry
	bases, err := ours.MergeBase(theirs)
	if err != nil {
		return err
	}
	if len(bases) > 0 {
		if baseEntries, err = flattenCommitTree(bases[0]); err != nil {
			return err
		}
	}

	oursEntries, err := flattenCommitTree(ours)
	if err != nil {
		return err
	}

	theirsEntries, err := flattenCommitTree(theirs)
	if err != nil {
		return err
	}

	paths := make(map[string]struct{}, len(oursEntries))
	for _, m := range []map[string]*object.TreeEntry{baseEntries, oursEntries, theirsEntries} {
		for p := range m {
			paths[p] = struct{}{}
		}
	}

	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)

	theirsLabel := theirs.Hash.String()
	results := make([]*mergeResult, 0, len(sorted))
	for _, p := range sorted {
		res, err := w.mergePath(p, baseEntries[p], oursEntries[p], theirsEntries[p], theirsLabel)
		if err != nil {
			return err
		}
		results = append(results, res)
	}

	markFileDirectoryConflicts(results, theirsLabel)

	if err := w.checkUntrackedOverwrites(results, oursEntries); err != nil {
		return err
	}

	idx, err := w.applyMergeResults(results, oursEntries)
	if err != nil {
		return err
	}

	for _, res := range results {
		if res.conflict {
			return w.writeMergeHead(theirs.Hash)
		}
	}

	return w.commitMerge(idx, ours.Hash, theirs.Hash, opts)
}

func flattenCommitTree(c *object.Commit) (map[string]*object.TreeEntry, error) {
	t, err := c.Tree()
	if err != nil {
		return nil, err
	}

	entries := make(map[string]*object.TreeEntry)
	walker := object.NewTreeWalker(t, true, nil)
	defer walker.Close()

	for {
		name, e, err := walker.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}

		if e.Mode == filemode.Dir {
			continue
		}

		entries[name] = &object.TreeEntry{Name: name, Mode: e.Mode, Hash: e.Hash}
	}

	return entries, nil
}

func sameTreeEntry(a, b *object.TreeEntry) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Hash == b.Hash && a.Mode == b.Mode
}

func isMergeableFile(e *object.TreeEntry) bool {
	return e != nil && (e.Mode == filemode.Regular || e.Mode == filemode.Executable || e.Mode == filemode.Deprecated)
}

func (w *Worktree) mergePath(p string, base, ours, theirs *object.TreeEntry, theirsLabel string) (*mergeResult, error) {
	res := &mergeResult{path: p, base: base, ours: ours, theirs: theirs}

	switch {
	case sameTreeEntry(ours, theirs):
		res.entry = ours
		return res, nil
	case sameTreeEntry(base, ours):
		res.entry = theirs
		return res, nil
	case sameTreeEntry(base, theirs):
		res.entry = ours
		return res, nil
	}

	res.conflict = true
	res.entry = ours
	if res.entry == nil {
		res.entry = theirs
	}

	if !isMergeableFile(ours) || !isMergeableFile(theirs) {
		return res, nil
	}

	var baseContent []byte
	if isMergeableFile(base) {
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

	if isBinaryContent(baseContent) || isBinaryContent(oursContent) || isBinaryContent(theirsContent) {
		return res, nil
	}

	merged, conflict := mergeLines(baseContent, oursContent, theirsContent, "HEAD", theirsLabel)

	mode := ours.Mode
	if base != nil && ours.Mode == base.Mode {
		mode = theirs.Mode
	}

	if conflict {
		res.entry = &object.TreeEntry{Name: p, Mode: mode, Hash: ours.Hash}
		res.content = merged
		return res, nil
	}

	h, err := w.storeBlob(merged)
	if err != nil {
		return nil, err
	}

	res.conflict = false
	res.entry = &object.TreeEntry{Name: p, Mode: mode, Hash: h}
	return res, nil
}

// markFileDirectoryConflicts turns paths that are files on one side and
// directories on the other into conflicts. As git does, the file is moved
// aside in the working tree, to "<path>~<side>".
func markFileDirectoryConflicts(results []*mergeResult, theirsLabel string) {
	dirs := make(map[string]struct{})
	for _, res := range results {
		if res.entry == nil {
			continue
		}
		for d := path.Dir(res.path); d != "."; d = path.Dir(d) {
			dirs[d] = struct{}{}
		}
	}

	for _, res := range results {
		if res.entry == nil {
			continue
		}
		if _, ok := dirs[res.path]; !ok {
			continue
		}

		label := "HEAD"
		if res.ours == nil {
			label = theirsLabel
		}

		res.conflict = true
		res.worktreePath = res.path + "~" + label
	}
}

func (w *Worktree) checkUntrackedOverwrites(results []*mergeResult, oursEntries map[string]*object.TreeEntry) error {
	for _, res := range results {
		if res.entry == nil {
			continue
		}

		p := res.worktreeTarget()
		if _, tracked := oursEntries[p]; tracked {
			continue
		}

		if _, err := w.Filesystem.Lstat(p); err == nil {
			return ErrUncommittedChanges
		}
	}

	return nil
}

func (res *mergeResult) worktreeTarget() string {
	if res.worktreePath != "" {
		return res.worktreePath
	}
	return res.path
}

func (w *Worktree) applyMergeResults(results []*mergeResult, oursEntries map[string]*object.TreeEntry) (*index.Index, error) {
	for _, res := range results {
		if err := validPath(res.path); err != nil {
			return nil, err
		}
	}

	for _, res := range results {
		if oursEntries[res.path] == nil {
			continue
		}

		if res.entry != nil && res.worktreeTarget() == res.path {
			continue
		}

		if err := rmFileAndDirsIfEmpty(w.Filesystem, res.path); err != nil {
			return nil, err
		}
	}

	idx, err := w.r.Storer.Index()
	if err != nil {
		return nil, err
	}

	current := make(map[string]*index.Entry, len(idx.Entries))
	for _, e := range idx.Entries {
		current[e.Name] = e
	}

	entries := make([]*index.Entry, 0, len(results))
	for _, res := range results {
		if res.entry != nil {
			unchanged := res.content == nil && res.worktreeTarget() == res.path &&
				sameTreeEntry(res.entry, oursEntries[res.path])
			if !unchanged {
				if err := w.writeMergedFile(res); err != nil {
					return nil, err
				}
			}
		}

		if res.conflict {
			for stage, e := range []*object.TreeEntry{res.base, res.ours, res.theirs} {
				if e == nil {
					continue
				}
				entries = append(entries, &index.Entry{
					Name:  res.path,
					Hash:  e.Hash,
					Mode:  e.Mode,
					Stage: index.Stage(stage + 1),
				})
			}
			continue
		}

		if res.entry == nil {
			continue
		}

		if e, ok := current[res.path]; ok && e.Stage == 0 && sameTreeEntry(res.entry, oursEntries[res.path]) {
			entries = append(entries, e)
			continue
		}

		e, err := w.mergedIndexEntry(res.entry)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}

	idx.Entries = entries
	return idx, w.r.Storer.SetIndex(idx)
}

func (w *Worktree) writeMergedFile(res *mergeResult) error {
	e := res.entry
	name := res.worktreeTarget()

	if e.Mode == filemode.Submodule {
		return w.Filesystem.MkdirAll(name, os.ModeDir|0o755)
	}

	if res.content == nil {
		blob, err := w.r.BlobObject(e.Hash)
		if err != nil {
			return err
		}

		if err := w.removeForOverwrite(name); err != nil {
			return err
		}

		return w.checkoutFile(object.NewFile(name, e.Mode, blob))
	}

	if err := w.removeForOverwrite(name); err != nil {
		return err
	}

	mode, err := e.Mode.ToOSFileMode()
	if err != nil {
		return err
	}

	if mode&os.ModeSymlink != 0 {
		mode = 0o644
	}

	return util.WriteFile(w.Filesystem, name, res.content, mode.Perm())
}

// removeForOverwrite removes an existing file, so mode changes are applied
// given that billy does not implement chmod.
func (w *Worktree) removeForOverwrite(name string) error {
	err := w.Filesystem.Remove(name)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (w *Worktree) mergedIndexEntry(te *object.TreeEntry) (*index.Entry, error) {
	e := &index.Entry{Name: te.Name, Hash: te.Hash, Mode: te.Mode}
	if te.Mode == filemode.Submodule {
		return e, nil
	}

	fi, err := w.Filesystem.Lstat(te.Name)
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

func (w *Worktree) writeMergeHead(target plumbing.Hash) error {
	if err := util.WriteFile(w.Filesystem, mergeHeadPath, []byte(target.String()+"\n"), 0o644); err != nil {
		return err
	}
	return ErrMergeConflicts
}

// readMergeHead returns the commit recorded in MERGE_HEAD, if any.
func (w *Worktree) readMergeHead() (plumbing.Hash, bool, error) {
	content, err := util.ReadFile(w.Filesystem, mergeHeadPath)
	if os.IsNotExist(err) {
		return plumbing.ZeroHash, false, nil
	}
	if err != nil {
		return plumbing.ZeroHash, false, err
	}

	h, ok := plumbing.FromHex(strings.TrimSpace(string(content)))
	if !ok {
		return plumbing.ZeroHash, false, errors.New("invalid MERGE_HEAD content")
	}

	return h, true, nil
}

func (w *Worktree) commitMerge(idx *index.Index, ours, theirs plumbing.Hash, opts *MergeOptions) error {
	author, committer, err := w.mergeSignatures(opts)
	if err != nil {
		return err
	}

	h := &buildTreeHelper{fs: w.Filesystem, s: w.r.Storer}
	tree, err := h.BuildTree(idx, nil)
	if err != nil {
		return err
	}

	msg := opts.Message
	if msg == "" {
		msg = "Merge commit '" + theirs.String() + "'\n"
	}

	commit, err := w.buildCommitObject(msg, &CommitOptions{
		Author:    author,
		Committer: committer,
		Parents:   []plumbing.Hash{ours, theirs},
	}, tree)
	if err != nil {
		return err
	}

	return w.updateHEAD(commit)
}

func (w *Worktree) mergeSignatures(opts *MergeOptions) (*object.Signature, *object.Signature, error) {
	co := &CommitOptions{Author: opts.Author, Committer: opts.Committer}
	if co.Author == nil {
		err := co.loadConfigAuthorAndCommitter(w.r)
		if errors.Is(err, ErrMissingAuthor) {
			co.Author = &object.Signature{Name: "go-git", Email: "go-git@localhost", When: time.Now()}
		} else if err != nil {
			return nil, nil, err
		}
	}

	if co.Committer == nil {
		co.Committer = co.Author
	}

	return co.Author, co.Committer, nil
}

func (w *Worktree) blobContent(h plumbing.Hash) ([]byte, error) {
	blob, err := w.r.BlobObject(h)
	if err != nil {
		return nil, err
	}

	r, err := blob.Reader()
	if err != nil {
		return nil, err
	}
	defer r.Close()

	return io.ReadAll(r)
}

func (w *Worktree) storeBlob(content []byte) (plumbing.Hash, error) {
	obj := w.r.Storer.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	obj.SetSize(int64(len(content)))

	wr, err := obj.Writer()
	if err != nil {
		return plumbing.ZeroHash, err
	}

	if _, err := wr.Write(content); err != nil {
		_ = wr.Close()
		return plumbing.ZeroHash, err
	}

	if err := wr.Close(); err != nil {
		return plumbing.ZeroHash, err
	}

	return w.r.Storer.SetEncodedObject(obj)
}

func isBinaryContent(b []byte) bool {
	const sniffLen = 8000
	if len(b) > sniffLen {
		b = b[:sniffLen]
	}
	return bytes.IndexByte(b, 0) >= 0
}

// mergeLines performs a line based three-way merge of ours and theirs against
// base. Changes that do not overlap are combined; overlapping changes are
// written between conflict markers, in which case conflict is true.
func mergeLines(base, ours, theirs []byte, oursLabel, theirsLabel string) (merged []byte, conflict bool) {
	b, o, t := splitLines(base), splitLines(ours), splitLines(theirs)
	mo, mt := matchLines(b, o), matchLines(b, t)

	var out bytes.Buffer
	var bi, oi, ti int
	for {
		for bi < len(b) && mo[bi] == oi && mt[bi] == ti {
			out.WriteString(b[bi])
			bi++
			oi++
			ti++
		}

		if bi == len(b) && oi == len(o) && ti == len(t) {
			break
		}

		nb, no, nt := len(b), len(o), len(t)
		for j := bi; j < len(b); j++ {
			if mo[j] >= 0 && mt[j] >= 0 {
				nb, no, nt = j, mo[j], mt[j]
				break
			}
		}

		bc, oc, tc := b[bi:nb], o[oi:no], t[ti:nt]
		switch {
		case equalLines(oc, bc):
			writeLines(&out, tc)
		case equalLines(tc, bc), equalLines(oc, tc):
			writeLines(&out, oc)
		default:
			conflict = true
			writeConflict(&out, oc, tc, oursLabel, theirsLabel)
		}

		bi, oi, ti = nb, no, nt
	}

	return out.Bytes(), conflict
}

func writeConflict(out *bytes.Buffer, ours, theirs []string, oursLabel, theirsLabel string) {
	pre := 0
	for pre < len(ours) && pre < len(theirs) && ours[pre] == theirs[pre] {
		pre++
	}

	suf := 0
	for suf < len(ours)-pre && suf < len(theirs)-pre &&
		ours[len(ours)-1-suf] == theirs[len(theirs)-1-suf] {
		suf++
	}

	writeLines(out, ours[:pre])
	out.WriteString(conflictMarkerOurs + " " + oursLabel + "\n")
	writeConflictSide(out, ours[pre:len(ours)-suf])
	out.WriteString(conflictMarkerSep + "\n")
	writeConflictSide(out, theirs[pre:len(theirs)-suf])
	out.WriteString(conflictMarkerTheirs + " " + theirsLabel + "\n")
	writeLines(out, ours[len(ours)-suf:])
}

func writeConflictSide(out *bytes.Buffer, lines []string) {
	writeLines(out, lines)
	if n := len(lines); n > 0 && !strings.HasSuffix(lines[n-1], "\n") {
		out.WriteByte('\n')
	}
}

func writeLines(out *bytes.Buffer, lines []string) {
	for _, l := range lines {
		out.WriteString(l)
	}
}

func equalLines(a, b []string) bool {
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

// splitLines splits b into lines, keeping the line terminators.
func splitLines(b []byte) []string {
	var lines []string
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			lines = append(lines, string(b))
			break
		}
		lines = append(lines, string(b[:i+1]))
		b = b[i+1:]
	}
	return lines
}

// matchLines returns, for every line of a, the index of the line of b it is
// matched with in a longest common subsequence of a and b, or -1.
func matchLines(a, b []string) []int {
	res := make([]int, len(a))
	for i := range res {
		res[i] = -1
	}

	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		res[pre] = pre
		pre++
	}

	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		res[len(a)-1-suf] = len(b) - 1 - suf
		suf++
	}

	ids := make(map[string]int)
	toIDs := func(lines []string) []int {
		out := make([]int, len(lines))
		for i, l := range lines {
			id, ok := ids[l]
			if !ok {
				id = len(ids)
				ids[l] = id
			}
			out[i] = id
		}
		return out
	}

	x := toIDs(a[pre : len(a)-suf])
	y := toIDs(b[pre : len(b)-suf])
	for _, m := range myersMatches(x, y) {
		res[pre+m[0]] = pre + m[1]
	}

	return res
}

// myersMatches returns the pairs of matching indexes of a shortest edit
// script between a and b, computed with Myers' O(ND) algorithm.
func myersMatches(a, b []int) [][2]int {
	n, m := len(a), len(b)
	if n == 0 || m == 0 {
		return nil
	}

	maxD := n + m
	offset := maxD + 1
	v := make([]int, 2*maxD+3)
	var trace [][]int

	var d int
search:
	for d = 0; d <= maxD; d++ {
		snapshot := make([]int, len(v))
		copy(snapshot, v)
		trace = append(trace, snapshot)

		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1]
			} else {
				x = v[offset+k-1] + 1
			}

			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}

			v[offset+k] = x
			if x >= n && y >= m {
				break search
			}
		}
	}

	var matches [][2]int
	x, y := n, m
	for ; d > 0; d-- {
		prev := trace[d]
		k := x - y

		var prevK int
		if k == -d || (k != d && prev[offset+k-1] < prev[offset+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}

		prevX := prev[offset+prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x--
			y--
			matches = append(matches, [2]int{x, y})
		}

		x, y = prevX, prevY
	}

	for x > 0 && y > 0 {
		x--
		y--
		matches = append(matches, [2]int{x, y})
	}

	for i, j := 0, len(matches)-1; i < j; i, j = i+1, j-1 {
		matches[i], matches[j] = matches[j], matches[i]
	}

	return matches
}
