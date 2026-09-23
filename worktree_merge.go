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

	"github.com/go-git/go-billy/v6"
	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/format/index"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/utils/diff"
)

// mergeHeadPath is the worktree-filesystem file that records the commit being
// merged. It is plain text, not a reference stored in the object backend.
const mergeHeadPath = GitDirName + "/MERGE_HEAD"

// Merge incorporates the commit identified by target into HEAD.
//
// With an empty MergeOptions value the merge fast-forwards when target is a
// descendant of HEAD. Otherwise it performs a three-way merge against the
// best common ancestor and creates a merge commit. Non-overlapping edits to
// the same file are combined automatically. When conflicts remain, every
// non-conflicting path is still updated, conflict markers are written to the
// worktree, the index records stages 1/2/3 for the blobs that exist, the
// target hash is written to .git/MERGE_HEAD, and ErrMergeConflicts is returned.
//
// Merge works when repository user.name and user.email are unset.
// A dirty worktree or index returns ErrUncommittedChanges.
func (w *Worktree) Merge(target plumbing.Hash, opts *MergeOptions) error {
	if opts == nil {
		opts = &MergeOptions{}
	}
	_ = opts

	status, err := w.Status()
	if err != nil {
		return err
	}
	if dirtyStatus(status) {
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
		// MergeReset updates tracked paths and leaves untracked files in place.
		return w.Reset(&ResetOptions{Commit: target, Mode: MergeReset})
	}

	// HEAD already contains target: nothing to do.
	contains, err := isFastForward(w.r.Storer, target, headHash, nil)
	if err != nil {
		return err
	}
	if contains {
		return nil
	}

	return w.mergeThreeWay(headHash, target)
}

func (w *Worktree) mergeThreeWay(headHash, target plumbing.Hash) error {
	headCommit, err := object.GetCommit(w.r.Storer, headHash)
	if err != nil {
		return err
	}
	targetCommit, err := object.GetCommit(w.r.Storer, target)
	if err != nil {
		return err
	}

	oursTree, err := headCommit.Tree()
	if err != nil {
		return err
	}
	theirsTree, err := targetCommit.Tree()
	if err != nil {
		return err
	}

	var baseTree *object.Tree
	bases, err := headCommit.MergeBase(targetCommit)
	if err != nil {
		return err
	}
	if len(bases) > 0 {
		baseTree, err = bases[0].Tree()
		if err != nil {
			return err
		}
	}

	ours, err := treeFiles(oursTree)
	if err != nil {
		return err
	}
	theirs, err := treeFiles(theirsTree)
	if err != nil {
		return err
	}
	base, err := treeFiles(baseTree)
	if err != nil {
		return err
	}

	idx, err := w.r.Storer.Index()
	if err != nil {
		return err
	}

	label := target.String()
	oursDirs := directorySet(ours)
	theirsDirs := directorySet(theirs)

	var fsOps []fsOp
	var ixOps []indexOp
	conflict := false

	paths := unionPaths(ours, theirs, base)
	handled := map[string]struct{}{}

	for _, p := range paths {
		o := lookupEntry(ours, p)
		t := lookupEntry(theirs, p)
		b := lookupEntry(base, p)

		// A file on one side and a directory on the other conflicts only when
		// both sides changed that path. An unchanged file yields to the
		// directory, and an unchanged directory yields to the file.
		if o != nil && theirsDirs[p] && !entriesEqual(o, b) && !sideDirUnchanged(p, theirs, base) {
			conflict = true
			handled[p] = struct{}{}
			fsOps, ixOps = planFileDir(fsOps, ixOps, p, p+"~HEAD", b, o, nil)
			continue
		}
		if t != nil && oursDirs[p] && !entriesEqual(t, b) && !sideDirUnchanged(p, ours, base) {
			conflict = true
			handled[p] = struct{}{}
			fsOps, ixOps = planFileDir(fsOps, ixOps, p, p+"~"+label, b, nil, t)
			continue
		}
	}

	for _, p := range paths {
		if _, ok := handled[p]; ok {
			continue
		}
		o := lookupEntry(ours, p)
		t := lookupEntry(theirs, p)
		b := lookupEntry(base, p)

		ops, ix, pathConflict, err := w.planFile(p, b, o, t, label)
		if err != nil {
			return err
		}
		if pathConflict {
			conflict = true
		}
		fsOps = append(fsOps, ops...)
		if ix != nil {
			ixOps = append(ixOps, *ix)
		}
	}

	if err := w.applyFilesystem(fsOps); err != nil {
		return err
	}
	if err := w.applyIndex(idx, ixOps); err != nil {
		return err
	}
	sortIndexEntries(idx)
	if err := w.r.Storer.SetIndex(idx); err != nil {
		return err
	}

	if conflict {
		if err := w.writeMergeHead(target); err != nil {
			return err
		}
		return ErrMergeConflicts
	}

	sig := w.mergeSignature(headCommit)
	msg := fmt.Sprintf("Merge commit '%s'\n", target.String())
	_, err = w.Commit(msg, &CommitOptions{
		Author:            &sig,
		Committer:         &sig,
		Parents:           []plumbing.Hash{headHash, target},
		AllowEmptyCommits: true,
	})
	return err
}

type sideEntry struct {
	Mode filemode.FileMode
	Hash plumbing.Hash
}

func lookupEntry(m map[string]sideEntry, p string) *sideEntry {
	e, ok := m[p]
	if !ok {
		return nil
	}
	return &e
}

func entriesEqual(a, b *sideEntry) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Hash == b.Hash && a.Mode == b.Mode
}

type fsOp struct {
	path   string
	remove bool
	blob   *sideEntry
	raw    []byte
	rawSet bool
}

type indexOp struct {
	path   string
	stage0 *sideEntry
	stages []*index.Entry
}

func planFileDir(fsOps []fsOp, ixOps []indexOp, original, renamed string, base, ours, theirs *sideEntry) ([]fsOp, []indexOp) {
	file := ours
	stage := index.OurMode
	if file == nil {
		file = theirs
		stage = index.TheirMode
	}

	// The file currently checked out at original (ours) has to move aside so
	// the directory from the other side can be written.
	if ours != nil {
		fsOps = append(fsOps, fsOp{path: original, remove: true})
	}
	fsOps = append(fsOps, fsOp{path: renamed, blob: file})

	var stages []*index.Entry
	if base != nil {
		stages = append(stages, stageEntry(renamed, base, index.AncestorMode))
	}
	stages = append(stages, stageEntry(renamed, file, stage))
	ixOps = append(ixOps, indexOp{path: original}, indexOp{path: renamed, stages: stages})
	return fsOps, ixOps
}

func stageEntry(name string, e *sideEntry, stage index.Stage) *index.Entry {
	return &index.Entry{
		Name:  name,
		Hash:  e.Hash,
		Mode:  e.Mode,
		Stage: stage,
	}
}

func (w *Worktree) planFile(p string, base, ours, theirs *sideEntry, label string) ([]fsOp, *indexOp, bool, error) {
	switch {
	case entriesEqual(ours, theirs):
		// Both sides agree, including both deleting the path.
		if entriesEqual(ours, base) {
			return nil, nil, false, nil
		}
		ops, ix := planResolved(p, ours)
		return ops, ix, false, nil
	case entriesEqual(ours, base):
		ops, ix := planResolved(p, theirs)
		return ops, ix, false, nil
	case entriesEqual(theirs, base):
		ops, ix := planResolved(p, ours)
		return ops, ix, false, nil
	}

	// Both sides changed.
	if ours == nil || theirs == nil {
		survivor := ours
		if survivor == nil {
			survivor = theirs
		}
		stages := conflictStages(p, base, ours, theirs)
		ops := []fsOp{{path: p, blob: survivor}}
		return ops, &indexOp{path: p, stages: stages}, true, nil
	}

	if !isMergeableText(ours.Mode) || !isMergeableText(theirs.Mode) {
		stages := conflictStages(p, base, ours, theirs)
		ops := []fsOp{{path: p, blob: ours}}
		return ops, &indexOp{path: p, stages: stages}, true, nil
	}

	baseText, err := w.entryText(base)
	if err != nil {
		return nil, nil, false, err
	}
	oursText, err := w.entryText(ours)
	if err != nil {
		return nil, nil, false, err
	}
	theirsText, err := w.entryText(theirs)
	if err != nil {
		return nil, nil, false, err
	}

	if isBinaryText(baseText) || isBinaryText(oursText) || isBinaryText(theirsText) {
		stages := conflictStages(p, base, ours, theirs)
		ops := []fsOp{{path: p, blob: ours}}
		return ops, &indexOp{path: p, stages: stages}, true, nil
	}

	merged, pathConflict := mergeFileText(baseText, oursText, theirsText, label)
	if pathConflict {
		stages := conflictStages(p, base, ours, theirs)
		ops := []fsOp{{path: p, raw: []byte(merged), rawSet: true}}
		return ops, &indexOp{path: p, stages: stages}, true, nil
	}

	mode, ok := pickMode(base, ours, theirs)
	if !ok {
		stages := conflictStages(p, base, ours, theirs)
		ops := []fsOp{{path: p, blob: ours}}
		return ops, &indexOp{path: p, stages: stages}, true, nil
	}

	hash, err := w.hashForContent(merged, oursText, theirsText, baseText, ours, theirs, base)
	if err != nil {
		return nil, nil, false, err
	}
	resolved := &sideEntry{Mode: mode, Hash: hash}
	ops, ix := planResolved(p, resolved)
	return ops, ix, false, nil
}

func planResolved(p string, result *sideEntry) ([]fsOp, *indexOp) {
	if result == nil {
		return []fsOp{{path: p, remove: true}}, &indexOp{path: p}
	}
	return []fsOp{{path: p, blob: result}}, &indexOp{path: p, stage0: result}
}

func conflictStages(name string, base, ours, theirs *sideEntry) []*index.Entry {
	var stages []*index.Entry
	if base != nil {
		stages = append(stages, stageEntry(name, base, index.AncestorMode))
	}
	if ours != nil {
		stages = append(stages, stageEntry(name, ours, index.OurMode))
	}
	if theirs != nil {
		stages = append(stages, stageEntry(name, theirs, index.TheirMode))
	}
	return stages
}

func isMergeableText(m filemode.FileMode) bool {
	return m == filemode.Regular || m == filemode.Deprecated || m == filemode.Executable
}

func pickMode(base, ours, theirs *sideEntry) (filemode.FileMode, bool) {
	if ours.Mode == theirs.Mode {
		return ours.Mode, true
	}
	if base == nil {
		return 0, false
	}
	if ours.Mode == base.Mode {
		return theirs.Mode, true
	}
	if theirs.Mode == base.Mode {
		return ours.Mode, true
	}
	return 0, false
}

func (w *Worktree) hashForContent(merged, oursText, theirsText, baseText string, ours, theirs, base *sideEntry) (plumbing.Hash, error) {
	switch {
	case ours != nil && merged == oursText:
		return ours.Hash, nil
	case theirs != nil && merged == theirsText:
		return theirs.Hash, nil
	case base != nil && merged == baseText:
		return base.Hash, nil
	default:
		return w.storeBlob([]byte(merged))
	}
}

func (w *Worktree) entryText(e *sideEntry) (string, error) {
	if e == nil || e.Mode == filemode.Submodule || e.Mode == filemode.Dir {
		return "", nil
	}
	return w.readBlob(e.Hash)
}

func (w *Worktree) readBlob(h plumbing.Hash) (string, error) {
	blob, err := object.GetBlob(w.r.Storer, h)
	if err != nil {
		return "", err
	}
	r, err := blob.Reader()
	if err != nil {
		return "", err
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return string(b), nil
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

func (w *Worktree) applyFilesystem(ops []fsOp) error {
	for _, op := range ops {
		if !op.remove {
			continue
		}
		if err := removeWorktreeFile(w.Filesystem, op.path); err != nil {
			return err
		}
	}
	for _, op := range ops {
		if op.remove {
			continue
		}
		if op.rawSet {
			if err := writeRawFile(w.Filesystem, op.path, op.raw); err != nil {
				return err
			}
			continue
		}
		if op.blob != nil {
			if err := w.checkoutEntry(op.path, *op.blob); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *Worktree) applyIndex(idx *index.Index, ops []indexOp) error {
	for _, op := range ops {
		if err := removeAllIndexEntries(idx, op.path); err != nil {
			return err
		}
		if op.stage0 != nil {
			if err := w.stageResolved(idx, op.path, *op.stage0); err != nil {
				return err
			}
			continue
		}
		idx.Entries = append(idx.Entries, op.stages...)
	}
	return nil
}

func (w *Worktree) stageResolved(idx *index.Index, name string, e sideEntry) error {
	if e.Mode == filemode.Submodule {
		idx.Entries = append(idx.Entries, &index.Entry{
			Name: name,
			Hash: e.Hash,
			Mode: filemode.Submodule,
		})
		return nil
	}
	entry := idx.Add(name)
	if err := w.doUpdateFileToIndex(entry, name, e.Hash); err != nil {
		return err
	}
	entry.Mode = e.Mode
	entry.Stage = 0
	return nil
}

func (w *Worktree) checkoutEntry(name string, e sideEntry) error {
	if e.Mode == filemode.Submodule {
		return w.Filesystem.MkdirAll(name, os.ModePerm)
	}
	if err := removeWorktreeFile(w.Filesystem, name); err != nil {
		return err
	}
	if err := ensureParent(w.Filesystem, name); err != nil {
		return err
	}
	blob, err := object.GetBlob(w.r.Storer, e.Hash)
	if err != nil {
		return err
	}
	return w.checkoutFile(object.NewFile(name, e.Mode, blob))
}

func removeWorktreeFile(fs billy.Filesystem, name string) error {
	fi, err := fs.Lstat(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.IsDir() {
		return nil
	}
	err = rmFileAndDirsIfEmpty(fs, name)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func writeRawFile(fs billy.Filesystem, name string, data []byte) error {
	if err := removeWorktreeFile(fs, name); err != nil {
		return err
	}
	if err := ensureParent(fs, name); err != nil {
		return err
	}
	f, err := fs.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}

func ensureParent(fs billy.Filesystem, name string) error {
	dir := path.Dir(name)
	if dir == "." || dir == "" || dir == "/" {
		return nil
	}
	return fs.MkdirAll(dir, 0o755)
}

func treeFiles(t *object.Tree) (map[string]sideEntry, error) {
	out := map[string]sideEntry{}
	if t == nil {
		return out, nil
	}
	walker := object.NewTreeWalker(t, true, nil)
	defer walker.Close()
	for {
		name, entry, err := walker.Next()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if entry.Mode == filemode.Dir {
			continue
		}
		out[name] = sideEntry{Mode: entry.Mode, Hash: entry.Hash}
	}
}

// dirtyStatus reports tracked worktree or index changes. Untracked files are
// not uncommitted changes.
func dirtyStatus(s Status) bool {
	for _, st := range s {
		if st.Staging == Untracked && st.Worktree == Untracked {
			continue
		}
		if st.Staging != Unmodified || st.Worktree != Unmodified {
			return true
		}
	}
	return false
}

// sideDirUnchanged reports whether every file under dir is identical in side and base.
func sideDirUnchanged(dir string, side, base map[string]sideEntry) bool {
	prefix := dir + "/"
	for p, e := range side {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		b, ok := base[p]
		if !ok || b.Hash != e.Hash || b.Mode != e.Mode {
			return false
		}
	}
	for p, e := range base {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		s, ok := side[p]
		if !ok || s.Hash != e.Hash || s.Mode != e.Mode {
			return false
		}
	}
	return true
}

func directorySet(files map[string]sideEntry) map[string]bool {
	dirs := map[string]bool{}
	for p := range files {
		for d := path.Dir(p); d != "." && d != "/" && d != ""; d = path.Dir(d) {
			if dirs[d] {
				break
			}
			dirs[d] = true
		}
	}
	return dirs
}

func unionPaths(maps ...map[string]sideEntry) []string {
	set := map[string]struct{}{}
	for _, m := range maps {
		for p := range m {
			set[p] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func sortIndexEntries(idx *index.Index) {
	sort.Slice(idx.Entries, func(i, j int) bool {
		if idx.Entries[i].Name == idx.Entries[j].Name {
			return idx.Entries[i].Stage < idx.Entries[j].Stage
		}
		return idx.Entries[i].Name < idx.Entries[j].Name
	})
}

func (w *Worktree) mergeSignature(head *object.Commit) object.Signature {
	sig := object.Signature{When: time.Now()}
	if cfg, err := w.r.Config(); err == nil {
		if name, email, ok := signatureFromConfig(cfg); ok {
			sig.Name = name
			sig.Email = email
			return sig
		}
	}
	if head != nil && head.Author.Name != "" && head.Author.Email != "" {
		sig.Name = head.Author.Name
		sig.Email = head.Author.Email
		return sig
	}
	sig.Name = "go-git"
	sig.Email = "go-git@go-git.local"
	return sig
}

func signatureFromConfig(cfg *config.Config) (string, string, bool) {
	switch {
	case cfg.Author.Name != "" && cfg.Author.Email != "":
		return cfg.Author.Name, cfg.Author.Email, true
	case cfg.Committer.Name != "" && cfg.Committer.Email != "":
		return cfg.Committer.Name, cfg.Committer.Email, true
	case cfg.User.Name != "" && cfg.User.Email != "":
		return cfg.User.Name, cfg.User.Email, true
	default:
		return "", "", false
	}
}

func (w *Worktree) readMergeHead() (plumbing.Hash, error) {
	f, err := w.Filesystem.Open(mergeHeadPath)
	if err != nil {
		if os.IsNotExist(err) {
			return plumbing.ZeroHash, nil
		}
		return plumbing.ZeroHash, err
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return plumbing.ZeroHash, nil
	}
	if i := strings.IndexAny(text, " \t"); i >= 0 {
		text = text[:i]
	}
	hash, ok := plumbing.FromHex(text)
	if !ok {
		return plumbing.ZeroHash, fmt.Errorf("invalid %s", mergeHeadPath)
	}
	return hash, nil
}

func (w *Worktree) writeMergeHead(h plumbing.Hash) error {
	if err := w.Filesystem.MkdirAll(GitDirName, 0o755); err != nil {
		return err
	}
	return writeRawFile(w.Filesystem, mergeHeadPath, []byte(h.String()+"\n"))
}

func (w *Worktree) removeMergeHead() error {
	err := w.Filesystem.Remove(mergeHeadPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func isBinaryText(s string) bool {
	return bytes.IndexByte([]byte(s), 0) >= 0
}

func mergeFileText(base, ours, theirs, label string) (string, bool) {
	if ours == theirs {
		return ours, false
	}
	lines, conflict := mergeLines(splitKeepLines(base), splitKeepLines(ours), splitKeepLines(theirs), label)
	return joinLines(lines), conflict
}

func splitKeepLines(s string) []string {
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

type lineEdit struct {
	start int
	end   int
	lines []string
}

func mergeLines(base, ours, theirs []string, label string) ([]string, bool) {
	oe := lineEdits(base, ours)
	te := lineEdits(base, theirs)

	var out []string
	conflict := false
	oi, ti := 0, 0
	pos := 0

	for pos < len(base) || oi < len(oe) || ti < len(te) {
		var oEdit, tEdit *lineEdit
		if oi < len(oe) {
			oEdit = &oe[oi]
		}
		if ti < len(te) {
			tEdit = &te[ti]
		}
		if oEdit == nil && tEdit == nil {
			if pos < len(base) {
				out = append(out, base[pos:]...)
			}
			break
		}

		next := len(base)
		if oEdit != nil && oEdit.start < next {
			next = oEdit.start
		}
		if tEdit != nil && tEdit.start < next {
			next = tEdit.start
		}
		if pos < next {
			out = append(out, base[pos:next]...)
			pos = next
			continue
		}

		oHere := oEdit != nil && oEdit.start == pos
		tHere := tEdit != nil && tEdit.start == pos

		if oHere && tHere && editsEqual(*oEdit, *tEdit) &&
			!followingAdjacent(oe, oi) && !followingAdjacent(te, ti) {
			out = append(out, oEdit.lines...)
			pos = oEdit.end
			oi++
			ti++
			continue
		}

		if oHere && !tHere && (tEdit == nil || tEdit.start > oEdit.end) {
			out = append(out, oEdit.lines...)
			pos = oEdit.end
			oi++
			continue
		}
		if tHere && !oHere && (oEdit == nil || oEdit.start > tEdit.end) {
			out = append(out, tEdit.lines...)
			pos = tEdit.end
			ti++
			continue
		}

		end := pos
		if oHere && oEdit.end > end {
			end = oEdit.end
		}
		if tHere && tEdit.end > end {
			end = tEdit.end
		}
		if oEdit != nil && oEdit.start <= end && oEdit.end > end {
			end = oEdit.end
		}
		if tEdit != nil && tEdit.start <= end && tEdit.end > end {
			end = tEdit.end
		}

		for {
			extended := false
			if oi < len(oe) && oe[oi].start <= end {
				if oe[oi].end > end {
					end = oe[oi].end
					extended = true
				}
				oi++
				extended = true
			}
			if ti < len(te) && te[ti].start <= end {
				if te[ti].end > end {
					end = te[ti].end
					extended = true
				}
				ti++
				extended = true
			}
			if !extended {
				break
			}
		}

		includeEnd := hasInsertionAt(oe, end) || hasInsertionAt(te, end)
		oursText := renderRegion(base, oe, pos, end, includeEnd)
		theirsText := renderRegion(base, te, pos, end, includeEnd)
		if linesEqual(oursText, theirsText) {
			out = append(out, oursText...)
		} else {
			conflict = true
			out = append(out, conflictMarkers(oursText, theirsText, label)...)
		}
		pos = end
	}

	return out, conflict
}

func followingAdjacent(edits []lineEdit, idx int) bool {
	if idx+1 >= len(edits) {
		return false
	}
	return edits[idx+1].start <= edits[idx].end
}

func editsEqual(a, b lineEdit) bool {
	if a.start != b.start || a.end != b.end || len(a.lines) != len(b.lines) {
		return false
	}
	for i := range a.lines {
		if a.lines[i] != b.lines[i] {
			return false
		}
	}
	return true
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

func hasInsertionAt(edits []lineEdit, at int) bool {
	for i := range edits {
		e := &edits[i]
		if e.start > at {
			break
		}
		if e.start == at && e.end == at && len(e.lines) > 0 {
			return true
		}
	}
	return false
}

func renderRegion(base []string, edits []lineEdit, start, end int, includeEnd bool) []string {
	var out []string
	pos := start
	for i := range edits {
		e := &edits[i]
		if e.start < start && e.end <= start {
			continue
		}
		if e.start > end || (e.start == end && !(includeEnd && e.end == end)) {
			break
		}
		if e.start == end && e.end == end {
			out = append(out, e.lines...)
			continue
		}
		if e.start > pos {
			limit := e.start
			if limit > end {
				limit = end
			}
			if pos < limit {
				out = append(out, base[pos:limit]...)
				pos = limit
			}
		}
		// Skip edits that begin before this region; their replacement was
		// accounted for by an earlier region.
		if e.start < start {
			continue
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

func conflictMarkers(ours, theirs []string, label string) []string {
	var b strings.Builder
	b.WriteString("<<<<<<< HEAD\n")
	writeMarkedSide(&b, ours)
	b.WriteString("=======\n")
	writeMarkedSide(&b, theirs)
	b.WriteString(">>>>>>> ")
	b.WriteString(label)
	b.WriteByte('\n')
	return splitKeepLines(b.String())
}

func writeMarkedSide(b *strings.Builder, lines []string) {
	for _, line := range lines {
		b.WriteString(line)
	}
	if n := len(lines); n > 0 && !strings.HasSuffix(lines[n-1], "\n") {
		b.WriteByte('\n')
	}
}

func lineEdits(baseLines, otherLines []string) []lineEdit {
	diffs := diff.Do(joinLines(baseLines), joinLines(otherLines))
	var edits []lineEdit
	var pending *lineEdit
	baseLine := 0
	flush := func() {
		if pending == nil {
			return
		}
		if pending.end > pending.start || len(pending.lines) > 0 {
			edits = append(edits, *pending)
		}
		pending = nil
	}
	for _, d := range diffs {
		lines := splitKeepLines(d.Text)
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			flush()
			baseLine += len(lines)
		case diffmatchpatch.DiffDelete:
			if pending == nil {
				pending = &lineEdit{start: baseLine, end: baseLine}
			}
			pending.end += len(lines)
			baseLine += len(lines)
		case diffmatchpatch.DiffInsert:
			if pending == nil {
				pending = &lineEdit{start: baseLine, end: baseLine}
			}
			pending.lines = append(pending.lines, lines...)
		}
	}
	flush()
	if baseLine != len(baseLines) {
		// The line diff could not be aligned. One coarse edit is still a
		// correct description of the change and keeps conflict detection honest.
		return []lineEdit{{start: 0, end: len(baseLines), lines: otherLines}}
	}
	return edits
}
