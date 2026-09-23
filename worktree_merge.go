package git

import (
	"bytes"
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

// Merge incorporates the commit identified by target into HEAD.
//
// With an empty MergeOptions value the merge fast-forwards when HEAD is an
// ancestor of target. Otherwise it performs a 3-way merge and, when that merge
// is clean, creates a merge commit. Conflicts are written to the worktree and
// the index and ErrMergeConflicts is returned. A dirty worktree returns
// ErrUncommittedChanges.
func (w *Worktree) Merge(target plumbing.Hash, opts *MergeOptions) error {
	if opts == nil {
		opts = &MergeOptions{}
	}
	_ = opts

	if err := w.ensureClean(); err != nil {
		return err
	}

	headRef, err := w.r.Head()
	if err != nil {
		return err
	}
	headHash := headRef.Hash()
	if headHash == target {
		return nil
	}

	headCommit, err := w.r.CommitObject(headHash)
	if err != nil {
		return err
	}
	targetCommit, err := w.r.CommitObject(target)
	if err != nil {
		return err
	}

	ff, err := headCommit.IsAncestor(targetCommit)
	if err != nil {
		return err
	}
	if ff {
		return w.Reset(&ResetOptions{Commit: target, Mode: MergeReset})
	}

	already, err := targetCommit.IsAncestor(headCommit)
	if err != nil {
		return err
	}
	if already {
		return nil
	}

	return w.threeWayMerge(headCommit, targetCommit)
}

func (w *Worktree) ensureClean() error {
	status, err := w.Status()
	if err != nil {
		return err
	}
	for _, st := range status {
		if st.Staging == Untracked && st.Worktree == Untracked {
			continue
		}
		if st.Staging != Unmodified || st.Worktree != Unmodified {
			return ErrUncommittedChanges
		}
	}
	return nil
}

func (w *Worktree) threeWayMerge(head, target *object.Commit) error {
	bases, err := head.MergeBase(target)
	if err != nil {
		return err
	}

	var baseTree *object.Tree
	if len(bases) > 0 {
		baseTree, err = bases[0].Tree()
		if err != nil {
			return err
		}
	}
	oursTree, err := head.Tree()
	if err != nil {
		return err
	}
	theirsTree, err := target.Tree()
	if err != nil {
		return err
	}

	baseFiles, err := treeBlobs(baseTree)
	if err != nil {
		return err
	}
	oursFiles, err := treeBlobs(oursTree)
	if err != nil {
		return err
	}
	theirsFiles, err := treeBlobs(theirsTree)
	if err != nil {
		return err
	}

	idx, err := w.r.Storer.Index()
	if err != nil {
		return err
	}

	paths := map[string]struct{}{}
	for p := range baseFiles {
		paths[p] = struct{}{}
	}
	for p := range oursFiles {
		paths[p] = struct{}{}
	}
	for p := range theirsFiles {
		paths[p] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for p := range paths {
		ordered = append(ordered, p)
	}
	sort.Strings(ordered)

	baseDirs := directoriesOf(baseFiles)
	oursDirs := directoriesOf(oursFiles)
	theirsDirs := directoriesOf(theirsFiles)

	conflicted := false
	for _, p := range ordered {
		c, err := w.mergeOne(idx, p, target, baseFiles, oursFiles, theirsFiles, baseDirs, oursDirs, theirsDirs)
		if err != nil {
			return err
		}
		if c {
			conflicted = true
		}
	}

	if err := w.r.Storer.SetIndex(idx); err != nil {
		return err
	}

	if conflicted {
		if err := w.writeMergeHead(target.Hash); err != nil {
			return err
		}
		return ErrMergeConflicts
	}

	// Commit reads MERGE_HEAD from the worktree and records it as the second parent.
	if err := w.writeMergeHead(target.Hash); err != nil {
		return err
	}
	msg := fmt.Sprintf("Merge commit '%s'\n", target.Hash.String())
	_, err = w.Commit(msg, &CommitOptions{
		Author: w.mergeSignature(),
		Parents: []plumbing.Hash{
			head.Hash,
		},
	})
	return err
}

type blobEntry struct {
	hash plumbing.Hash
	mode filemode.FileMode
}

func treeBlobs(t *object.Tree) (map[string]blobEntry, error) {
	out := map[string]blobEntry{}
	if t == nil {
		return out, nil
	}
	err := t.Files().ForEach(func(f *object.File) error {
		out[f.Name] = blobEntry{hash: f.Hash, mode: f.Mode}
		return nil
	})
	return out, err
}

func directoriesOf(files map[string]blobEntry) map[string]struct{} {
	dirs := map[string]struct{}{}
	for p := range files {
		for {
			p = path.Dir(p)
			if p == "." || p == "/" || p == "" {
				break
			}
			dirs[p] = struct{}{}
		}
	}
	return dirs
}

func (w *Worktree) mergeOne(
	idx *index.Index,
	p string,
	target *object.Commit,
	base, ours, theirs map[string]blobEntry,
	baseDirs, oursDirs, theirsDirs map[string]struct{},
) (bool, error) {
	b, bOK := base[p]
	o, oOK := ours[p]
	t, tOK := theirs[p]

	if fileDirClash(p, bOK, oOK, tOK, baseDirs, oursDirs, theirsDirs) || blockedByFile(p, base, ours, theirs) {
		return true, w.recordConflict(idx, p, target.Hash, b, bOK, o, oOK, t, tOK, true)
	}

	switch {
	case oOK && tOK && o == t:
		if bOK && b == o {
			return false, nil
		}
		return false, w.stageBlob(idx, p, o)
	case oOK && tOK && bOK && o == b && t != b:
		return false, w.stageBlob(idx, p, t)
	case oOK && tOK && bOK && t == b && o != b:
		return false, nil
	case !bOK && oOK && !tOK:
		return false, nil
	case !bOK && tOK && !oOK:
		return false, w.stageBlob(idx, p, t)
	case bOK && !oOK && tOK && t == b:
		return false, w.removeMerged(idx, p)
	case bOK && !tOK && oOK && o == b:
		return false, w.removeMerged(idx, p)
	case bOK && !oOK && tOK:
		// Deleted by us, modified by them.
		return true, w.recordConflict(idx, p, target.Hash, b, true, blobEntry{}, false, t, true, false)
	case bOK && !tOK && oOK:
		return true, w.recordConflict(idx, p, target.Hash, b, true, o, true, blobEntry{}, false, false)
	case !oOK && !tOK:
		if bOK {
			return false, w.removeMerged(idx, p)
		}
		return false, nil
	case oOK && tOK && kindOf(o.mode) != kindOf(t.mode):
		return true, w.recordConflict(idx, p, target.Hash, b, bOK, o, true, t, true, true)
	case !bOK && oOK && tOK:
		return true, w.recordConflict(idx, p, target.Hash, blobEntry{}, false, o, true, t, true, true)
	default:
		merged, clean, err := w.mergeBlobs(b, bOK, o, t)
		if err != nil {
			return false, err
		}
		if !clean {
			return true, w.recordConflict(idx, p, target.Hash, b, bOK, o, true, t, true, true)
		}
		mode, ok := pickMode(b.mode, o.mode, t.mode, bOK)
		if !ok {
			return true, w.recordConflict(idx, p, target.Hash, b, bOK, o, true, t, true, true)
		}
		return false, w.stageContent(idx, p, merged, mode)
	}
}

func fileDirClash(p string, bOK, oOK, tOK bool, baseDirs, oursDirs, theirsDirs map[string]struct{}) bool {
	_, bd := baseDirs[p]
	_, od := oursDirs[p]
	_, td := theirsDirs[p]
	file := bOK || oOK || tOK
	// A side counts as a directory only when it does not also have a blob here.
	dir := (bd && !bOK) || (od && !oOK) || (td && !tOK)
	return file && dir
}

func blockedByFile(p string, sides ...map[string]blobEntry) bool {
	parent := path.Dir(p)
	for parent != "." && parent != "/" && parent != "" {
		for _, files := range sides {
			if _, ok := files[parent]; ok {
				return true
			}
		}
		parent = path.Dir(parent)
	}
	return false
}

func kindOf(m filemode.FileMode) int {
	switch m {
	case filemode.Symlink:
		return 1
	case filemode.Submodule:
		return 2
	default:
		return 0
	}
}

func pickMode(base, ours, theirs filemode.FileMode, hasBase bool) (filemode.FileMode, bool) {
	if ours == theirs {
		return ours, true
	}
	if hasBase && ours == base {
		return theirs, true
	}
	if hasBase && theirs == base {
		return ours, true
	}
	if !hasBase {
		return 0, false
	}
	return 0, false
}

func (w *Worktree) mergeBlobs(base blobEntry, hasBase bool, ours, theirs blobEntry) ([]byte, bool, error) {
	var baseBytes []byte
	var err error
	if hasBase {
		baseBytes, err = w.readBlob(base.hash)
		if err != nil {
			return nil, false, err
		}
	}
	oursBytes, err := w.readBlob(ours.hash)
	if err != nil {
		return nil, false, err
	}
	theirsBytes, err := w.readBlob(theirs.hash)
	if err != nil {
		return nil, false, err
	}
	if isBinary(baseBytes) || isBinary(oursBytes) || isBinary(theirsBytes) {
		return nil, false, nil
	}
	merged, clean := mergeText(baseBytes, oursBytes, theirsBytes)
	return merged, clean, nil
}

func isBinary(b []byte) bool {
	return bytes.IndexByte(b, 0) >= 0
}

func (w *Worktree) recordConflict(idx *index.Index, name string, target plumbing.Hash, base blobEntry, hasBase bool, ours blobEntry, hasOurs bool, theirs blobEntry, hasTheirs bool, withMarkers bool) error {
	removeAllIndexEntries(idx, name)
	if hasBase {
		if err := w.addConflictStage(idx, name, index.AncestorMode, base); err != nil {
			return err
		}
	}
	if hasOurs {
		if err := w.addConflictStage(idx, name, index.OurMode, ours); err != nil {
			return err
		}
	}
	if hasTheirs {
		if err := w.addConflictStage(idx, name, index.TheirMode, theirs); err != nil {
			return err
		}
	}

	if !withMarkers {
		switch {
		case hasOurs:
			return w.checkoutBlob(name, ours)
		case hasTheirs:
			return w.checkoutBlob(name, theirs)
		default:
			return nil
		}
	}
	if !hasOurs && blockedByFile(name, map[string]blobEntry{path.Dir(name): {}}) {
		return nil
	}

	var oursText, theirsText []byte
	var err error
	if hasOurs && kindOf(ours.mode) == 0 {
		oursText, err = w.readBlob(ours.hash)
		if err != nil {
			return err
		}
	}
	if hasTheirs && kindOf(theirs.mode) == 0 {
		theirsText, err = w.readBlob(theirs.hash)
		if err != nil {
			return err
		}
	}
	marked := formatConflict(oursText, theirsText, target)
	mode := filemode.Regular
	if hasOurs && kindOf(ours.mode) == 0 {
		mode = ours.mode
	} else if hasTheirs && kindOf(theirs.mode) == 0 {
		mode = theirs.mode
	}
	if hasOurs && kindOf(ours.mode) != 0 && !hasTheirs {
		return w.checkoutBlob(name, ours)
	}
	return w.writeWorktreeFile(name, marked, mode)
}

func formatConflict(ours, theirs []byte, target plumbing.Hash) []byte {
	var buf bytes.Buffer
	buf.WriteString("<<<<<<< HEAD\n")
	writeSection(&buf, ours)
	buf.WriteString("=======\n")
	writeSection(&buf, theirs)
	fmt.Fprintf(&buf, ">>>>>>> %s\n", target.String())
	return buf.Bytes()
}

func writeSection(buf *bytes.Buffer, content []byte) {
	if len(content) == 0 {
		return
	}
	buf.Write(content)
	if content[len(content)-1] != '\n' {
		buf.WriteByte('\n')
	}
}

func (w *Worktree) addConflictStage(idx *index.Index, name string, stage index.Stage, e blobEntry) error {
	blob, err := object.GetBlob(w.r.Storer, e.hash)
	if err != nil {
		return err
	}
	entry := idx.Add(name)
	entry.Hash = e.hash
	entry.Mode = e.mode
	entry.Stage = stage
	entry.Size = uint32(blob.Size)
	entry.ModifiedAt = time.Now()
	return nil
}

func (w *Worktree) stageBlob(idx *index.Index, name string, e blobEntry) error {
	content, err := w.readBlob(e.hash)
	if err != nil {
		return err
	}
	if err := w.writeWorktreeFile(name, content, e.mode); err != nil {
		return err
	}
	removeAllIndexEntries(idx, name)
	entry := idx.Add(name)
	if err := w.doUpdateFileToIndex(entry, name, e.hash); err != nil {
		return err
	}
	entry.Mode = e.mode
	entry.Stage = 0
	return nil
}

func (w *Worktree) stageContent(idx *index.Index, name string, content []byte, mode filemode.FileMode) error {
	hash, err := w.writeBlob(content)
	if err != nil {
		return err
	}
	if err := w.writeWorktreeFile(name, content, mode); err != nil {
		return err
	}
	removeAllIndexEntries(idx, name)
	entry := idx.Add(name)
	if err := w.doUpdateFileToIndex(entry, name, hash); err != nil {
		return err
	}
	entry.Mode = mode
	entry.Stage = 0
	return nil
}

func (w *Worktree) removeMerged(idx *index.Index, name string) error {
	removeAllIndexEntries(idx, name)
	if err := util.RemoveAll(w.Filesystem, name); err != nil && !os.IsNotExist(err) {
		return err
	}
	dir := path.Dir(name)
	for dir != "." && dir != "/" && dir != "" {
		removed, err := removeDirIfEmpty(w.Filesystem, dir)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if !removed {
			break
		}
		dir = path.Dir(dir)
	}
	return nil
}

func (w *Worktree) checkoutBlob(name string, e blobEntry) error {
	if e.mode == filemode.Submodule {
		return w.Filesystem.MkdirAll(name, 0o755)
	}
	content, err := w.readBlob(e.hash)
	if err != nil {
		return err
	}
	return w.writeWorktreeFile(name, content, e.mode)
}

func (w *Worktree) writeWorktreeFile(name string, content []byte, mode filemode.FileMode) error {
	if mode == filemode.Submodule {
		return w.Filesystem.MkdirAll(name, 0o755)
	}
	dir := path.Dir(name)
	if dir != "." && dir != "/" {
		if err := w.Filesystem.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	_ = util.RemoveAll(w.Filesystem, name)
	if mode == filemode.Symlink {
		return w.Filesystem.Symlink(string(content), name)
	}
	perm := os.FileMode(0o644)
	if mode == filemode.Executable {
		perm = 0o755
	}
	f, err := w.Filesystem.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, werr := f.Write(content)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}

func (w *Worktree) readBlob(h plumbing.Hash) ([]byte, error) {
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

func (w *Worktree) writeBlob(content []byte) (plumbing.Hash, error) {
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
	b, err := io.ReadAll(f)
	if err != nil {
		return plumbing.ZeroHash, false, err
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return plumbing.ZeroHash, false, nil
	}
	return plumbing.NewHash(s), true, nil
}

func (w *Worktree) clearMergeHead() error {
	err := w.Filesystem.Remove(mergeHeadFile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (w *Worktree) mergeSignature() *object.Signature {
	sig := &object.Signature{
		Name:  "go-git",
		Email: "go-git@local",
		When:  time.Now(),
	}
	cfg, err := w.r.Config()
	if err != nil || cfg == nil {
		return sig
	}
	if cfg.Author.Name != "" && cfg.Author.Email != "" {
		sig.Name = cfg.Author.Name
		sig.Email = cfg.Author.Email
		return sig
	}
	if cfg.User.Name != "" && cfg.User.Email != "" {
		sig.Name = cfg.User.Name
		sig.Email = cfg.User.Email
	}
	return sig
}

type diffHunk struct {
	baseStart int
	baseEnd   int
	lines     []string
}

func mergeText(base, ours, theirs []byte) ([]byte, bool) {
	bLines, bEOL := splitLines(base)
	oLines, oEOL := splitLines(ours)
	tLines, tEOL := splitLines(theirs)
	merged, clean := mergeLineSets(bLines, oLines, tLines)
	if !clean {
		return nil, false
	}
	eol := oEOL || tEOL || bEOL
	if !oEOL && !tEOL {
		eol = false
	}
	return joinLines(merged, eol), true
}

func splitLines(b []byte) ([]string, bool) {
	if len(b) == 0 {
		return nil, false
	}
	eol := b[len(b)-1] == '\n'
	s := string(b)
	if eol {
		s = s[:len(s)-1]
	}
	if s == "" {
		return []string{""}, eol
	}
	return strings.Split(s, "\n"), eol
}

func joinLines(lines []string, eol bool) []byte {
	if len(lines) == 0 {
		if eol {
			return []byte("\n")
		}
		return nil
	}
	s := strings.Join(lines, "\n")
	if eol {
		s += "\n"
	}
	return []byte(s)
}

func mergeLineSets(base, ours, theirs []string) ([]string, bool) {
	aH := diffHunks(base, ours)
	bH := diffHunks(base, theirs)
	var out []string
	i, j := 0, 0
	pos := 0
	for i < len(aH) || j < len(bH) {
		switch {
		case i < len(aH) && (j >= len(bH) || (!hunksOverlap(aH[i], bH[j]) && aH[i].baseEnd <= bH[j].baseStart)):
			out = append(out, base[pos:aH[i].baseStart]...)
			out = append(out, aH[i].lines...)
			pos = aH[i].baseEnd
			i++
		case j < len(bH) && (i >= len(aH) || !hunksOverlap(aH[i], bH[j])):
			out = append(out, base[pos:bH[j].baseStart]...)
			out = append(out, bH[j].lines...)
			pos = bH[j].baseEnd
			j++
		default:
			start := aH[i].baseStart
			if bH[j].baseStart < start {
				start = bH[j].baseStart
			}
			end := start
			ai, bj := i, j
			for {
				moved := false
				if ai < len(aH) && aH[ai].baseStart <= end {
					if aH[ai].baseEnd > end {
						end = aH[ai].baseEnd
					}
					ai++
					moved = true
				}
				if bj < len(bH) && bH[bj].baseStart <= end {
					if bH[bj].baseEnd > end {
						end = bH[bj].baseEnd
					}
					bj++
					moved = true
				}
				if !moved {
					break
				}
			}
			if start > pos {
				out = append(out, base[pos:start]...)
			}
			oLines := applyHunks(base, start, end, aH[i:ai])
			tLines := applyHunks(base, start, end, bH[j:bj])
			if !linesEqual(oLines, tLines) {
				return nil, false
			}
			out = append(out, oLines...)
			pos = end
			i, j = ai, bj
		}
	}
	out = append(out, base[pos:]...)
	return out, true
}

func hunksOverlap(a, b diffHunk) bool {
	if a.baseStart == a.baseEnd && b.baseStart == b.baseEnd {
		return a.baseStart == b.baseStart
	}
	return a.baseStart < b.baseEnd && b.baseStart < a.baseEnd
}

func applyHunks(base []string, start, end int, hunks []diffHunk) []string {
	var out []string
	pos := start
	for _, h := range hunks {
		if h.baseStart > pos {
			out = append(out, base[pos:h.baseStart]...)
		}
		out = append(out, h.lines...)
		pos = h.baseEnd
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

func diffHunks(base, other []string) []diffHunk {
	dp := lcsLens(base, other)
	var hunks []diffHunk
	var cur *diffHunk
	emit := func() {
		if cur != nil {
			hunks = append(hunks, *cur)
			cur = nil
		}
	}
	start := func(i int) {
		if cur == nil {
			cur = &diffHunk{baseStart: i, baseEnd: i}
		}
	}
	i, j := 0, 0
	for i < len(base) && j < len(other) {
		if base[i] == other[j] && dp[i][j] == dp[i+1][j+1]+1 {
			emit()
			i++
			j++
			continue
		}
		if dp[i][j] == dp[i+1][j] {
			start(i)
			cur.baseEnd = i + 1
			i++
			continue
		}
		start(i)
		cur.lines = append(cur.lines, other[j])
		j++
	}
	for i < len(base) {
		start(i)
		cur.baseEnd = i + 1
		i++
	}
	for j < len(other) {
		start(i)
		cur.lines = append(cur.lines, other[j])
		j++
	}
	emit()
	return hunks
}

func lcsLens(a, b []string) [][]int {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		row := dp[i]
		next := dp[i+1]
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				row[j] = next[j+1] + 1
			} else if next[j] >= row[j+1] {
				row[j] = next[j]
			} else {
				row[j] = row[j+1]
			}
		}
	}
	return dp
}
