package git

import (
	"bytes"
	"errors"
	"fmt"
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

const mergeHeadFile = GitDirName + "/MERGE_HEAD"

// Merge merges the commit identified by target into HEAD.
//
// An empty MergeOptions value fast-forwards when target is a descendant of
// HEAD. Otherwise Merge performs a three-way merge against the best common
// ancestor and creates a merge commit. Non-overlapping edits to the same file
// are combined automatically, and paths that merge cleanly are updated even
// when other paths conflict.
//
// Merge returns ErrUncommittedChanges when the worktree or index is dirty.
// On conflicts it writes conflict markers to the worktree, records stages 1/2/3
// in the index for every side that has a blob, writes target to .git/MERGE_HEAD
// on the worktree filesystem, and returns ErrMergeConflicts.
//
// Repository user configuration is not required.
func (w *Worktree) Merge(target plumbing.Hash, opts *MergeOptions) error {
	if opts == nil {
		opts = &MergeOptions{}
	}
	switch opts.Strategy {
	case FastForwardMerge:
		// Fast-forward when possible; otherwise a recursive three-way merge.
	default:
		return ErrUnsupportedMergeStrategy
	}

	status, err := w.Status()
	if err != nil {
		return err
	}
	if !status.IsClean() {
		return ErrUncommittedChanges
	}

	headRef, err := w.r.Head()
	if err != nil {
		return err
	}
	headHash := headRef.Hash()
	if headHash == target {
		return nil
	}

	ff, err := isFastForward(w.r.Storer, headHash, target, nil)
	if err != nil {
		return err
	}
	if ff {
		return w.Reset(&ResetOptions{Commit: target, Mode: HardReset})
	}

	already, err := isFastForward(w.r.Storer, target, headHash, nil)
	if err != nil {
		return err
	}
	if already {
		return nil
	}

	return w.threeWayMerge(headHash, target)
}

func (w *Worktree) threeWayMerge(head, target plumbing.Hash) error {
	oursCommit, err := w.r.CommitObject(head)
	if err != nil {
		return err
	}
	theirsCommit, err := w.r.CommitObject(target)
	if err != nil {
		return err
	}

	var baseTree *object.Tree
	bases, err := oursCommit.MergeBase(theirsCommit)
	if err != nil {
		return err
	}
	if len(bases) > 0 {
		baseTree, err = bases[0].Tree()
		if err != nil {
			return err
		}
	}

	oursTree, err := oursCommit.Tree()
	if err != nil {
		return err
	}
	theirsTree, err := theirsCommit.Tree()
	if err != nil {
		return err
	}

	baseFiles, err := readTreeFiles(baseTree)
	if err != nil {
		return err
	}
	ourFiles, err := readTreeFiles(oursTree)
	if err != nil {
		return err
	}
	theirFiles, err := readTreeFiles(theirsTree)
	if err != nil {
		return err
	}

	idx, err := w.r.Storer.Index()
	if err != nil {
		return err
	}

	conflicts := false
	label := target.String()
	for _, name := range unionFilePaths(baseFiles, ourFiles, theirFiles) {
		conflict, err := w.mergePath(idx, name, baseFiles, ourFiles, theirFiles, label)
		if err != nil {
			return err
		}
		if conflict {
			conflicts = true
		}
	}

	if err := w.r.Storer.SetIndex(idx); err != nil {
		return err
	}
	if conflicts {
		if err := w.writeMergeHead(target); err != nil {
			return err
		}
		return ErrMergeConflicts
	}
	return w.createMergeCommit(head, target)
}

type fileID struct {
	mode filemode.FileMode
	hash plumbing.Hash
}

type treeFiles struct {
	files map[string]fileID
	dirs  map[string]struct{}
}

func readTreeFiles(t *object.Tree) (treeFiles, error) {
	tf := treeFiles{
		files: map[string]fileID{},
		dirs:  map[string]struct{}{},
	}
	if t == nil {
		return tf, nil
	}

	walker := object.NewTreeWalker(t, true, nil)
	defer walker.Close()
	for {
		name, entry, err := walker.Next()
		if errors.Is(err, io.EOF) {
			return tf, nil
		}
		if err != nil {
			return tf, err
		}
		if entry.Mode == filemode.Dir {
			tf.dirs[name] = struct{}{}
			continue
		}
		tf.files[name] = fileID{mode: entry.Mode, hash: entry.Hash}
	}
}

func unionFilePaths(trees ...treeFiles) []string {
	set := map[string]struct{}{}
	for _, t := range trees {
		for name := range t.files {
			set[name] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (t treeFiles) isDir(name string) bool {
	_, ok := t.dirs[name]
	return ok
}

func changedFrom(base treeFiles, side treeFiles, name string) bool {
	b, bOK := base.files[name]
	s, sOK := side.files[name]
	if bOK != sOK {
		return true
	}
	if !bOK {
		return false
	}
	return b.hash != s.hash || b.mode != s.mode
}

// directTypeClash reports whether the two tips disagree on whether name is a file or a directory.
func directTypeClash(ours, theirs treeFiles, name string) bool {
	_, oFile := ours.files[name]
	_, tFile := theirs.files[name]
	oDir := ours.isDir(name)
	tDir := theirs.isDir(name)
	return (oFile && tDir) || (tFile && oDir)
}

// underTypeClash reports whether a parent of name is a file on one tip and a directory on the other.
func underTypeClash(ours, theirs treeFiles, name string) bool {
	dir := path.Dir(name)
	for dir != "." && dir != "/" && dir != "" {
		if directTypeClash(ours, theirs, dir) {
			return true
		}
		dir = path.Dir(dir)
	}
	return false
}

func (w *Worktree) mergePath(idx *index.Index, name string, base, ours, theirs treeFiles, label string) (bool, error) {
	if directTypeClash(ours, theirs, name) {
		if err := w.conflictStages(idx, name, base, ours, theirs); err != nil {
			return false, err
		}
		// Keep our side of the worktree. Checking out the other type would
		// require replacing a file with a directory or the reverse.
		return true, nil
	}
	if underTypeClash(ours, theirs, name) {
		// A parent path is already a file/directory conflict. Leave this
		// entry as it is in HEAD so the directory (or file) stays intact.
		return true, nil
	}

	oID, oOK := ours.files[name]
	tID, tOK := theirs.files[name]
	oCh := changedFrom(base, ours, name)
	tCh := changedFrom(base, theirs, name)

	if !oCh && !tCh {
		return false, nil
	}
	if oCh && !tCh {
		return false, nil
	}
	if !oCh && tCh {
		if !tOK {
			return false, w.deleteMergedPath(idx, name)
		}
		return false, w.takeFile(idx, name, tID)
	}

	// Both sides changed.
	if !oOK && !tOK {
		return false, nil
	}
	if !oOK || !tOK {
		if err := w.conflictStages(idx, name, base, ours, theirs); err != nil {
			return false, err
		}
		if !oOK && tOK {
			if err := w.takeFile(idx, name, tID); err != nil {
				return false, err
			}
			// takeFile installs a stage-0 entry; put the conflict stages back.
			if err := w.conflictStages(idx, name, base, ours, theirs); err != nil {
				return false, err
			}
		}
		return true, nil
	}

	if oID.hash == tID.hash {
		if !similarFileMode(oID.mode, tID.mode) {
			if err := w.conflictStages(idx, name, base, ours, theirs); err != nil {
				return false, err
			}
			return true, nil
		}
		mode := pickMode(base, name, oID, tID)
		if mode != oID.mode {
			content, err := w.blobBytes(oID.hash)
			if err != nil {
				return false, err
			}
			if err := w.writeAndStage(idx, name, content, mode); err != nil {
				return false, err
			}
		}
		return false, nil
	}

	if !similarFileMode(oID.mode, tID.mode) || fileKind(oID.mode) == kindSubmodule {
		if err := w.conflictStages(idx, name, base, ours, theirs); err != nil {
			return false, err
		}
		return true, nil
	}

	merged, conflict, err := w.mergeBlobs(base, ours, theirs, name, label)
	if err != nil {
		return false, err
	}
	if conflict {
		if err := w.conflictStages(idx, name, base, ours, theirs); err != nil {
			return false, err
		}
		if merged != nil {
			mode := oID.mode
			if err := w.writeWorktreeFile(name, merged, mode); err != nil {
				return false, err
			}
		}
		return true, nil
	}

	mode := pickMode(base, name, oID, tID)
	if err := w.writeAndStage(idx, name, merged, mode); err != nil {
		return false, err
	}
	return false, nil
}

func (w *Worktree) conflictStages(idx *index.Index, name string, base, ours, theirs treeFiles) error {
	removeIndexEntries(idx, name)
	type staged struct {
		stage index.Stage
		id    fileID
		ok    bool
	}
	bID, bOK := base.files[name]
	oID, oOK := ours.files[name]
	tID, tOK := theirs.files[name]
	for _, s := range []staged{
		{index.AncestorMode, bID, bOK},
		{index.OurMode, oID, oOK},
		{index.TheirMode, tID, tOK},
	} {
		if !s.ok {
			continue
		}
		var size uint32
		if sz, err := w.r.Storer.EncodedObjectSize(s.id.hash); err == nil && sz > 0 {
			size = uint32(sz)
		}
		idx.Entries = append(idx.Entries, &index.Entry{
			Name:  name,
			Hash:  s.id.hash,
			Mode:  s.id.mode,
			Stage: s.stage,
			Size:  size,
		})
	}
	return nil
}

func (w *Worktree) mergeBlobs(base, ours, theirs treeFiles, name, label string) ([]byte, bool, error) {
	bID, bOK := base.files[name]
	oID := ours.files[name]
	tID := theirs.files[name]

	var baseContent []byte
	var err error
	if bOK {
		baseContent, err = w.blobBytes(bID.hash)
		if err != nil {
			return nil, false, err
		}
	}
	oursContent, err := w.blobBytes(oID.hash)
	if err != nil {
		return nil, false, err
	}
	theirsContent, err := w.blobBytes(tID.hash)
	if err != nil {
		return nil, false, err
	}

	if bytes.Equal(oursContent, theirsContent) {
		return oursContent, false, nil
	}
	if isBinaryContent(baseContent) || isBinaryContent(oursContent) || isBinaryContent(theirsContent) {
		// Leave the worktree at ours; stages record both blobs.
		return nil, true, nil
	}

	merged, conflict := mergeText(string(baseContent), string(oursContent), string(theirsContent), label)
	return []byte(merged), conflict, nil
}

func pickMode(base treeFiles, name string, ours, theirs fileID) filemode.FileMode {
	if ours.mode == theirs.mode {
		return ours.mode
	}
	if !similarFileMode(ours.mode, theirs.mode) {
		return ours.mode
	}
	if bID, ok := base.files[name]; ok {
		if bID.mode == ours.mode {
			return theirs.mode
		}
		if bID.mode == theirs.mode {
			return ours.mode
		}
	}
	return ours.mode
}

const (
	kindFile = iota
	kindSymlink
	kindSubmodule
	kindOther
)

func fileKind(m filemode.FileMode) int {
	switch m {
	case filemode.Regular, filemode.Deprecated, filemode.Executable:
		return kindFile
	case filemode.Symlink:
		return kindSymlink
	case filemode.Submodule:
		return kindSubmodule
	default:
		return kindOther
	}
}

func similarFileMode(a, b filemode.FileMode) bool {
	return fileKind(a) == fileKind(b)
}

func isBinaryContent(b []byte) bool {
	return bytes.IndexByte(b, 0) >= 0
}

func (w *Worktree) blobBytes(h plumbing.Hash) ([]byte, error) {
	blob, err := object.GetBlob(w.r.Storer, h)
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

func (w *Worktree) takeFile(idx *index.Index, name string, id fileID) error {
	content, err := w.blobBytes(id.hash)
	if err != nil {
		return err
	}
	if err := w.writeWorktreeFile(name, content, id.mode); err != nil {
		return err
	}
	return w.stageBlob(idx, name, id.hash, id.mode)
}

func (w *Worktree) writeAndStage(idx *index.Index, name string, content []byte, mode filemode.FileMode) error {
	if err := w.writeWorktreeFile(name, content, mode); err != nil {
		return err
	}
	hash, err := w.storeBlob(content)
	if err != nil {
		return err
	}
	return w.stageBlob(idx, name, hash, mode)
}

func (w *Worktree) stageBlob(idx *index.Index, name string, hash plumbing.Hash, mode filemode.FileMode) error {
	removeIndexEntries(idx, name)
	if err := w.doAddFileToIndex(idx, name, hash); err != nil {
		return err
	}
	e, err := idx.Entry(name)
	if err != nil {
		return err
	}
	e.Mode = mode
	e.Stage = 0
	return nil
}

func (w *Worktree) storeBlob(content []byte) (plumbing.Hash, error) {
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

func (w *Worktree) writeWorktreeFile(name string, content []byte, mode filemode.FileMode) error {
	dir := path.Dir(name)
	if dir != "." && dir != "/" && dir != "" {
		if err := w.Filesystem.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if fi, err := w.Filesystem.Lstat(name); err == nil {
		if fi.IsDir() {
			if err := util.RemoveAll(w.Filesystem, name); err != nil {
				return err
			}
		} else if err := w.Filesystem.Remove(name); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	if mode == filemode.Symlink {
		return w.Filesystem.Symlink(string(content), name)
	}
	perm := os.FileMode(0o644)
	if mode == filemode.Executable {
		perm = 0o755
	}
	return util.WriteFile(w.Filesystem, name, content, perm)
}

func (w *Worktree) deleteMergedPath(idx *index.Index, name string) error {
	if _, err := w.deleteFromIndex(idx, name); err != nil && !errors.Is(err, index.ErrEntryNotFound) {
		return err
	}
	return rmFileAndDirsIfEmpty(w.Filesystem, name)
}

func (w *Worktree) createMergeCommit(head, target plumbing.Hash) error {
	opts := &CommitOptions{
		Parents:           []plumbing.Hash{head, target},
		AllowEmptyCommits: true,
	}
	if err := opts.Validate(w.r); err != nil {
		if !errors.Is(err, ErrMissingAuthor) {
			return err
		}
		opts.Author = &object.Signature{
			Name:  "go-git",
			Email: "go-git@localhost",
			When:  time.Now(),
		}
		if err := opts.Validate(w.r); err != nil {
			return err
		}
	}

	msg := fmt.Sprintf("Merge commit '%s'\n", target.String())
	_, err := w.Commit(msg, opts)
	return err
}

func (w *Worktree) writeMergeHead(h plumbing.Hash) error {
	if err := w.Filesystem.MkdirAll(GitDirName, 0o755); err != nil {
		return err
	}
	return util.WriteFile(w.Filesystem, mergeHeadFile, []byte(h.String()+"\n"), 0o644)
}

func (w *Worktree) readMergeHead() (plumbing.Hash, bool, error) {
	f, err := w.Filesystem.Open(mergeHeadFile)
	if err != nil {
		if os.IsNotExist(err) {
			return plumbing.ZeroHash, false, nil
		}
		return plumbing.ZeroHash, false, err
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return plumbing.ZeroHash, false, err
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return plumbing.ZeroHash, false, nil
	}
	// MERGE_HEAD may contain one hash. Extra lines are ignored.
	line, _, _ := strings.Cut(text, "\n")
	line = strings.TrimSpace(line)
	h := plumbing.NewHash(line)
	if h.IsZero() {
		return plumbing.ZeroHash, false, fmt.Errorf("invalid %s", mergeHeadFile)
	}
	return h, true, nil
}

func (w *Worktree) removeMergeHead() error {
	err := w.Filesystem.Remove(mergeHeadFile)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// mergeText performs a line-based three-way merge. Identical lines are matched
// by position, so repeated lines do not collapse into a single occurrence.
func mergeText(base, ours, theirs, theirLabel string) (string, bool) {
	if ours == theirs {
		return ours, false
	}
	if ours == base {
		return theirs, false
	}
	if theirs == base {
		return ours, false
	}

	b := splitKeepLines(base)
	o := splitKeepLines(ours)
	t := splitKeepLines(theirs)
	merged, conflict := mergeLines(b, o, t, theirLabel)
	return joinLines(merged), conflict
}

func splitKeepLines(s string) []string {
	if s == "" {
		return nil
	}
	var lines []string
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

type lineEdit struct {
	start int // inclusive index in base
	end   int // exclusive index in base
	lines []string
}

func mergeLines(base, ours, theirs []string, theirLabel string) ([]string, bool) {
	aEdits := lineDiff(base, ours)
	bEdits := lineDiff(base, theirs)

	var out []string
	conflict := false
	ia, ib := 0, 0
	i := 0
	n := len(base)

	for i < n || ia < len(aEdits) || ib < len(bEdits) {
		nextA, nextB := n+1, n+1
		if ia < len(aEdits) {
			nextA = aEdits[ia].start
		}
		if ib < len(bEdits) {
			nextB = bEdits[ib].start
		}

		if i < n && i < nextA && i < nextB {
			end := n
			if nextA < end {
				end = nextA
			}
			if nextB < end {
				end = nextB
			}
			out = append(out, base[i:end]...)
			i = end
			continue
		}

		aHere := ia < len(aEdits) && editTouches(aEdits[ia], i)
		bHere := ib < len(bEdits) && editTouches(bEdits[ib], i)
		if !aHere && !bHere {
			// Edits only remain past the end of base, or i ran off.
			if i >= n {
				break
			}
			out = append(out, base[i])
			i++
			continue
		}

		if aHere && !bHere && (ib >= len(bEdits) || !editsOverlap(aEdits[ia], bEdits[ib])) {
			out = append(out, aEdits[ia].lines...)
			i = aEdits[ia].end
			ia++
			continue
		}
		if bHere && !aHere && (ia >= len(aEdits) || !editsOverlap(bEdits[ib], aEdits[ia])) {
			out = append(out, bEdits[ib].lines...)
			i = bEdits[ib].end
			ib++
			continue
		}

		end := i
		for {
			progress := false
			var p bool
			ia, end, p = absorbEdits(aEdits, ia, i, end)
			progress = progress || p
			ib, end, p = absorbEdits(bEdits, ib, i, end)
			progress = progress || p
			if !progress {
				break
			}
		}
		oursLines := sliceSide(base, aEdits, i, end)
		theirLines := sliceSide(base, bEdits, i, end)
		// sliceSide walks the full edit lists and keeps every edit that
		// overlaps the span absorb just consumed.
		if linesEqual(oursLines, theirLines) {
			out = appendLines(out, oursLines)
		} else {
			conflict = true
			out = appendConflict(out, oursLines, theirLines, theirLabel)
		}
		if end == i {
			// Inserts do not advance through base. The consumed edits are
			// already behind ia/ib, so the next iteration emits what follows.
			if ia >= len(aEdits) && ib >= len(bEdits) {
				if i < n {
					out = append(out, base[i:]...)
				}
				break
			}
			continue
		}
		i = end
	}
	return out, conflict
}

func appendLines(dst, src []string) []string {
	if len(src) == 0 {
		return dst
	}
	if len(dst) > 0 && !strings.HasSuffix(dst[len(dst)-1], "\n") {
		dst = append([]string{}, dst...)
		dst[len(dst)-1] += "\n"
	}
	return append(dst, src...)
}

func appendConflict(dst, ours, theirs []string, theirLabel string) []string {
	dst = appendLines(dst, []string{"<<<<<<< HEAD\n"})
	dst = appendLines(dst, ours)
	dst = appendLines(dst, []string{"=======\n"})
	dst = appendLines(dst, theirs)
	dst = appendLines(dst, []string{fmt.Sprintf(">>>>>>> %s\n", theirLabel)})
	return dst
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

func editTouches(e lineEdit, i int) bool {
	if e.start == e.end {
		return e.start == i
	}
	return e.start <= i && i < e.end
}

func editsOverlap(a, b lineEdit) bool {
	if a.start == a.end && b.start == b.end {
		return a.start == b.start
	}
	if a.start == a.end {
		return a.start >= b.start && a.start < b.end
	}
	if b.start == b.end {
		return b.start >= a.start && b.start < a.end
	}
	return a.start < b.end && b.start < a.end
}

func absorbEdits(edits []lineEdit, idx, start, end int) (int, int, bool) {
	progress := false
	for idx < len(edits) && editIntersectsSpan(edits[idx], start, end) {
		if edits[idx].end > end {
			end = edits[idx].end
		}
		idx++
		progress = true
	}
	return idx, end, progress
}

func editIntersectsSpan(e lineEdit, start, end int) bool {
	if e.start == e.end {
		if end == start {
			return e.start == start
		}
		return e.start >= start && e.start < end
	}
	if end == start {
		return e.start <= start && start < e.end
	}
	return e.start < end && e.end > start
}

func sliceSide(base []string, edits []lineEdit, start, end int) []string {
	var out []string
	i := start
	for _, e := range edits {
		if e.start == e.end {
			include := e.start == start || (e.start > start && e.start < end)
			if !include {
				continue
			}
			if e.start > i && e.start <= len(base) {
				limit := e.start
				if limit > end && end > start {
					limit = end
				}
				if limit > i && limit <= len(base) {
					out = append(out, base[i:limit]...)
					i = limit
				}
			}
			out = append(out, e.lines...)
			continue
		}
		if e.end <= start || e.start >= end {
			continue
		}
		from := e.start
		if from < start {
			from = start
		}
		if i < from && from <= len(base) {
			out = append(out, base[i:from]...)
			i = from
		}
		out = append(out, e.lines...)
		if e.end > i {
			i = e.end
		}
	}
	if i < end && end <= len(base) {
		out = append(out, base[i:end]...)
	}
	return out
}

// lineDiff returns the edits that transform base into other. Matching is
// positional, so repeated identical lines stay distinct.
func lineDiff(base, other []string) []lineEdit {
	n, m := len(base), len(other)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if base[i] == other[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	var edits []lineEdit
	var cur *lineEdit
	flush := func() {
		if cur != nil {
			edits = append(edits, *cur)
			cur = nil
		}
	}
	i, j := 0, 0
	for i < n && j < m {
		if base[i] == other[j] && dp[i][j] == dp[i+1][j+1]+1 {
			flush()
			i++
			j++
			continue
		}
		if cur == nil {
			cur = &lineEdit{start: i, end: i}
		}
		if dp[i+1][j] >= dp[i][j+1] {
			i++
			cur.end = i
			continue
		}
		cur.lines = append(cur.lines, other[j])
		j++
	}
	for i < n {
		if cur == nil {
			cur = &lineEdit{start: i, end: i}
		}
		i++
		cur.end = i
	}
	for j < m {
		if cur == nil {
			cur = &lineEdit{start: i, end: i}
		}
		cur.lines = append(cur.lines, other[j])
		j++
	}
	flush()
	return edits
}
