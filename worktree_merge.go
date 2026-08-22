package git

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/go-git/go-billy/v6/util"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/format/index"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/plumbing/storer"
)

var (
	// ErrMergeConflicts is returned when a merge produces conflicts.
	ErrMergeConflicts = errors.New("merge conflict")
	// ErrUncommittedChanges is returned when Merge is called on a dirty worktree.
	ErrUncommittedChanges = errors.New("worktree contains uncommitted changes")
)

const mergeHeadFile = "MERGE_HEAD"

type treePath struct {
	hash  plumbing.Hash
	mode  filemode.FileMode
	isDir bool
}

func (w *Worktree) mergeHeadPath() string {
	return path.Join(GitDirName, mergeHeadFile)
}

// Merge incorporates the given commit into the current branch. With empty
// MergeOptions the worktree is fast-forwarded when possible; otherwise a
// 3-way merge is performed and a merge commit is created.
func (w *Worktree) Merge(target plumbing.Hash, opts *MergeOptions) error {
	if opts == nil {
		opts = &MergeOptions{}
	}
	_ = opts

	st, err := w.Status()
	if err != nil {
		return err
	}
	if !st.IsClean() {
		return ErrUncommittedChanges
	}

	head, err := w.r.Head()
	if err != nil {
		return err
	}
	if head.Hash() == target {
		return nil
	}

	oursCommit, err := w.r.CommitObject(head.Hash())
	if err != nil {
		return err
	}
	theirsCommit, err := w.r.CommitObject(target)
	if err != nil {
		return err
	}

	isFF, err := oursCommit.IsAncestor(theirsCommit)
	if err != nil {
		return err
	}
	if isFF {
		return w.Reset(&ResetOptions{Mode: HardReset, Commit: target})
	}

	alreadyMerged, err := theirsCommit.IsAncestor(oursCommit)
	if err != nil {
		return err
	}
	if alreadyMerged {
		return nil
	}

	oursTree, err := oursCommit.Tree()
	if err != nil {
		return err
	}
	theirsTree, err := theirsCommit.Tree()
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

	ours, err := collectTreePaths(oursTree)
	if err != nil {
		return err
	}
	theirs, err := collectTreePaths(theirsTree)
	if err != nil {
		return err
	}
	var base map[string]treePath
	if baseTree != nil {
		base, err = collectTreePaths(baseTree)
		if err != nil {
			return err
		}
	} else {
		base = map[string]treePath{}
	}

	paths := map[string]struct{}{}
	for p := range ours {
		paths[p] = struct{}{}
	}
	for p := range theirs {
		paths[p] = struct{}{}
	}
	for p := range base {
		paths[p] = struct{}{}
	}

	idx := &index.Index{Version: 2}
	var conflicts bool

	for p := range paths {
		o, oOK := ours[p]
		t, tOK := theirs[p]
		b, bOK := base[p]

		if typeClash(oOK, o, tOK, t) {
			conflicts = true
			if err := w.writeTypeClash(p, bOK, b, oOK, o, tOK, t, idx); err != nil {
				return err
			}
			continue
		}

		if o.isDir || t.isDir || b.isDir {
			continue
		}

		switch {
		case oOK && tOK && o.hash == t.hash && o.mode == t.mode:
			if err := w.applyMergedFile(p, o, idx); err != nil {
				return err
			}
		case bOK && oOK && !tOK && o.hash == b.hash:
			if err := w.removeWorktreePath(p); err != nil {
				return err
			}
		case bOK && tOK && !oOK && t.hash == b.hash:
			if err := w.removeWorktreePath(p); err != nil {
				return err
			}
		case bOK && !oOK && !tOK:
			if err := w.removeWorktreePath(p); err != nil {
				return err
			}
		case oOK && !tOK && (!bOK || o.hash != b.hash):
			if bOK && o.hash != b.hash {
				conflicts = true
				if err := w.writeDeleteModifyConflict(p, &b, &o, nil, idx); err != nil {
					return err
				}
				break
			}
			if err := w.applyMergedFile(p, o, idx); err != nil {
				return err
			}
		case tOK && !oOK && (!bOK || t.hash != b.hash):
			if bOK && t.hash != b.hash {
				conflicts = true
				if err := w.writeDeleteModifyConflict(p, &b, nil, &t, idx); err != nil {
					return err
				}
				break
			}
			if err := w.applyMergedFile(p, t, idx); err != nil {
				return err
			}
		case oOK && tOK && o.hash != t.hash:
			if bOK && o.hash == b.hash {
				if err := w.applyMergedFile(p, t, idx); err != nil {
					return err
				}
				break
			}
			if bOK && t.hash == b.hash {
				if err := w.applyMergedFile(p, o, idx); err != nil {
					return err
				}
				break
			}

			merged, ok, err := w.tryContentMerge(p, baseSide(bOK, b), o, t, target)
			if err != nil {
				return err
			}
			if !ok {
				conflicts = true
				if err := w.writeContentConflict(p, baseSide(bOK, b), o, t, target, idx); err != nil {
					return err
				}
				break
			}
			if err := w.writeMergedContent(p, merged, o.mode, idx); err != nil {
				return err
			}
		default:
			if oOK {
				if err := w.applyMergedFile(p, o, idx); err != nil {
					return err
				}
			} else if tOK {
				if err := w.applyMergedFile(p, t, idx); err != nil {
					return err
				}
			}
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

	msg := fmt.Sprintf("Merge commit '%s'", target.String())
	author, err := w.mergeAuthor()
	if err != nil {
		return err
	}
	_, err = w.Commit(msg, &CommitOptions{
		Author:            author,
		Parents:           []plumbing.Hash{head.Hash(), target},
		AllowEmptyCommits: true,
	})
	return err
}

func baseSide(ok bool, p treePath) *treePath {
	if !ok {
		return nil
	}
	return &p
}

func typeClash(oOK bool, o treePath, tOK bool, t treePath) bool {
	if !oOK || !tOK {
		return false
	}
	return o.isDir != t.isDir
}

func collectTreePaths(t *object.Tree) (map[string]treePath, error) {
	out := map[string]treePath{}
	if t == nil {
		return out, nil
	}

	err := t.Files().ForEach(func(f *object.File) error {
		out[f.Name] = treePath{hash: f.Hash, mode: f.Mode, isDir: false}
		dir := path.Dir(f.Name)
		for dir != "." && dir != "/" && dir != "" {
			if _, exists := out[dir]; !exists {
				out[dir] = treePath{isDir: true, mode: filemode.Dir}
			}
			dir = path.Dir(dir)
		}
		return nil
	})
	return out, err
}

func (w *Worktree) applyMergedFile(name string, p treePath, idx *index.Index) error {
	f, err := object.GetBlob(w.r.Storer, p.hash)
	if err != nil {
		return err
	}
	of := object.NewFile(name, p.mode, f)
	if err := w.preparePathForFile(name); err != nil {
		return err
	}
	if err := w.checkoutFile(of); err != nil {
		return err
	}
	return w.addStage0(idx, name, p.hash, p.mode)
}

func (w *Worktree) writeMergedContent(name string, content []byte, mode filemode.FileMode, idx *index.Index) error {
	h, err := w.storeBlob(content)
	if err != nil {
		return err
	}
	if err := w.preparePathForFile(name); err != nil {
		return err
	}
	if err := w.writeWorktreeFile(name, content); err != nil {
		return err
	}
	return w.addStage0(idx, name, h, mode)
}

func (w *Worktree) tryContentMerge(name string, base *treePath, ours, theirs treePath, target plumbing.Hash) ([]byte, bool, error) {
	oContent, err := blobString(w.r.Storer, ours.hash)
	if err != nil {
		return nil, false, err
	}
	tContent, err := blobString(w.r.Storer, theirs.hash)
	if err != nil {
		return nil, false, err
	}
	var bContent string
	if base != nil {
		bContent, err = blobString(w.r.Storer, base.hash)
		if err != nil {
			return nil, false, err
		}
	}

	if looksBinary(bContent) || looksBinary(oContent) || looksBinary(tContent) {
		return nil, false, nil
	}

	merged, conflict := merge3(bContent, oContent, tContent, target)
	if conflict {
		return nil, false, nil
	}
	_ = name
	return []byte(merged), true, nil
}

func (w *Worktree) writeContentConflict(name string, base *treePath, ours, theirs treePath, target plumbing.Hash, idx *index.Index) error {
	oContent, err := blobString(w.r.Storer, ours.hash)
	if err != nil {
		return err
	}
	tContent, err := blobString(w.r.Storer, theirs.hash)
	if err != nil {
		return err
	}
	var bContent string
	if base != nil {
		bContent, err = blobString(w.r.Storer, base.hash)
		if err != nil {
			return err
		}
	}

	merged, _ := merge3(bContent, oContent, tContent, target)
	if err := w.preparePathForFile(name); err != nil {
		return err
	}
	if err := w.writeWorktreeFile(name, []byte(merged)); err != nil {
		return err
	}

	if base != nil {
		if err := w.addConflictStage(idx, name, *base, index.AncestorMode); err != nil {
			return err
		}
	}
	if err := w.addConflictStage(idx, name, ours, index.OurMode); err != nil {
		return err
	}
	return w.addConflictStage(idx, name, theirs, index.TheirMode)
}

func (w *Worktree) writeDeleteModifyConflict(name string, base, ours, theirs *treePath, idx *index.Index) error {
	var keep *treePath
	if ours != nil {
		keep = ours
	} else {
		keep = theirs
	}
	if keep != nil {
		f, err := object.GetBlob(w.r.Storer, keep.hash)
		if err != nil {
			return err
		}
		of := object.NewFile(name, keep.mode, f)
		if err := w.preparePathForFile(name); err != nil {
			return err
		}
		if err := w.checkoutFile(of); err != nil {
			return err
		}
	}

	if base != nil {
		if err := w.addConflictStage(idx, name, *base, index.AncestorMode); err != nil {
			return err
		}
	}
	if ours != nil {
		if err := w.addConflictStage(idx, name, *ours, index.OurMode); err != nil {
			return err
		}
	}
	if theirs != nil {
		if err := w.addConflictStage(idx, name, *theirs, index.TheirMode); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worktree) writeTypeClash(name string, bOK bool, b treePath, oOK bool, o treePath, tOK bool, t treePath, idx *index.Index) error {
	if bOK && !b.isDir {
		if err := w.addConflictStage(idx, name, b, index.AncestorMode); err != nil {
			return err
		}
	}
	if oOK && !o.isDir {
		if err := w.addConflictStage(idx, name, o, index.OurMode); err != nil {
			return err
		}
		f, err := object.GetBlob(w.r.Storer, o.hash)
		if err != nil {
			return err
		}
		of := object.NewFile(name, o.mode, f)
		// Only write the file if the other side is not a directory we need to keep.
		if !t.isDir {
			if err := w.preparePathForFile(name); err != nil {
				return err
			}
			if err := w.checkoutFile(of); err != nil {
				return err
			}
		}
	}
	if tOK && !t.isDir {
		if err := w.addConflictStage(idx, name, t, index.TheirMode); err != nil {
			return err
		}
		if !oOK || o.isDir {
			f, err := object.GetBlob(w.r.Storer, t.hash)
			if err != nil {
				return err
			}
			of := object.NewFile(name, t.mode, f)
			if err := w.preparePathForFile(name); err != nil {
				return err
			}
			if err := w.checkoutFile(of); err != nil {
				return err
			}
		}
	}

	if (oOK && o.isDir) || (tOK && t.isDir) {
		markers := conflictText("", fileContentOrEmpty(w, oOK && !o.isDir, o), fileContentOrEmpty(w, tOK && !t.isDir, t), plumbing.ZeroHash)
		_ = markers
	}
	return nil
}

func fileContentOrEmpty(w *Worktree, ok bool, p treePath) string {
	if !ok {
		return ""
	}
	s, err := blobString(w.r.Storer, p.hash)
	if err != nil {
		return ""
	}
	return s
}

func (w *Worktree) addStage0(idx *index.Index, name string, h plumbing.Hash, mode filemode.FileMode) error {
	e := idx.Add(name)
	e.Hash = h
	e.Mode = mode
	e.Stage = 0
	if fi, err := w.Filesystem.Lstat(name); err == nil {
		e.ModifiedAt = fi.ModTime()
		e.Size = uint32(fi.Size())
	}
	return nil
}

func (w *Worktree) addConflictStage(idx *index.Index, name string, p treePath, stage index.Stage) error {
	e := &index.Entry{
		Name:  name,
		Hash:  p.hash,
		Mode:  p.mode,
		Stage: stage,
	}
	idx.Entries = append(idx.Entries, e)
	return nil
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

func (w *Worktree) writeWorktreeFile(name string, content []byte) error {
	if err := w.preparePathForFile(name); err != nil {
		return err
	}
	dir := path.Dir(name)
	if dir != "." && dir != "/" && dir != "" {
		if err := w.Filesystem.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return util.WriteFile(w.Filesystem, name, content, 0o644)
}

func (w *Worktree) preparePathForFile(name string) error {
	if fi, err := w.Filesystem.Lstat(name); err == nil {
		if fi.IsDir() {
			if err := util.RemoveAll(w.Filesystem, name); err != nil {
				return err
			}
		} else if err := w.Filesystem.Remove(name); err != nil {
			return err
		}
	}
	dir := path.Dir(name)
	for dir != "." && dir != "/" && dir != "" {
		fi, err := w.Filesystem.Lstat(dir)
		if err != nil {
			break
		}
		if !fi.IsDir() {
			if err := w.Filesystem.Remove(dir); err != nil {
				return err
			}
			break
		}
		dir = path.Dir(dir)
	}
	dir = path.Dir(name)
	if dir != "." && dir != "/" && dir != "" {
		return w.Filesystem.MkdirAll(dir, 0o755)
	}
	return nil
}

func (w *Worktree) removeWorktreePath(name string) error {
	err := w.Filesystem.Remove(name)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (w *Worktree) writeMergeHead(target plumbing.Hash) error {
	if err := w.Filesystem.MkdirAll(GitDirName, 0o755); err != nil {
		return err
	}
	return util.WriteFile(w.Filesystem, w.mergeHeadPath(), []byte(target.String()+"\n"), 0o644)
}

func (w *Worktree) readMergeHead() (plumbing.Hash, bool, error) {
	f, err := w.Filesystem.Open(w.mergeHeadPath())
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
	h := plumbing.NewHash(strings.TrimSpace(string(b)))
	if h.IsZero() {
		return plumbing.ZeroHash, false, nil
	}
	return h, true, nil
}

func (w *Worktree) removeMergeHead() error {
	err := w.Filesystem.Remove(w.mergeHeadPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (w *Worktree) mergeAuthor() (*object.Signature, error) {
	o := &CommitOptions{}
	if err := o.loadConfigAuthorAndCommitter(w.r); err == nil && o.Author != nil {
		return o.Author, nil
	}

	head, err := w.r.Head()
	if err != nil {
		return &object.Signature{
			Name:  "go-git",
			Email: "go-git@noreply.go-git",
			When:  time.Now(),
		}, nil
	}
	c, err := w.r.CommitObject(head.Hash())
	if err != nil {
		return &object.Signature{
			Name:  "go-git",
			Email: "go-git@noreply.go-git",
			When:  time.Now(),
		}, nil
	}
	a := c.Author
	a.When = time.Now()
	return &a, nil
}

func blobString(s storer.EncodedObjectStorer, h plumbing.Hash) (string, error) {
	b, err := object.GetBlob(s, h)
	if err != nil {
		return "", err
	}
	f := object.NewFile("", filemode.Regular, b)
	return f.Contents()
}

func looksBinary(s string) bool {
	return strings.ContainsRune(s, 0)
}

type mergeSpan struct {
	o0, o1 int
	s0, s1 int
}

func merge3(base, ours, theirs string, target plumbing.Hash) (string, bool) {
	if ours == theirs {
		return ours, false
	}
	if ours == base {
		return theirs, false
	}
	if theirs == base {
		return ours, false
	}

	oLines := splitMergeLines(base)
	aLines := splitMergeLines(ours)
	bLines := splitMergeLines(theirs)

	matchOA := lcsMatches(oLines, aLines)
	matchOB := lcsMatches(oLines, bLines)
	sa := changedSpans(oLines, aLines, matchOA)
	sb := changedSpans(oLines, bLines, matchOB)

	var out strings.Builder
	conflict := false
	oCur, aCur, bCur := 0, 0, 0
	ia, ib := 0, 0

	for ia < len(sa) || ib < len(sb) {
		nextO := len(oLines)
		if ia < len(sa) && sa[ia].o0 < nextO {
			nextO = sa[ia].o0
		}
		if ib < len(sb) && sb[ib].o0 < nextO {
			nextO = sb[ib].o0
		}

		n := nextO - oCur
		for i := 0; i < n; i++ {
			out.WriteString(oLines[oCur+i])
		}
		oCur += n
		aCur += n
		bCur += n

		o0 := nextO
		o1 := o0
		useA, useB := false, false
		a0, a1 := aCur, aCur
		b0, b1 := bCur, bCur

		if ia < len(sa) && sa[ia].o0 == o0 {
			useA = true
			o1 = max(o1, sa[ia].o1)
			a0, a1 = sa[ia].s0, sa[ia].s1
			ia++
		}
		if ib < len(sb) && sb[ib].o0 == o0 {
			useB = true
			o1 = max(o1, sb[ib].o1)
			b0, b1 = sb[ib].s0, sb[ib].s1
			ib++
		}
		for {
			expanded := false
			if ia < len(sa) && sa[ia].o0 < o1 {
				useA = true
				o1 = max(o1, sa[ia].o1)
				if !useA {
					a0 = sa[ia].s0
				}
				a1 = sa[ia].s1
				ia++
				expanded = true
			}
			if ib < len(sb) && sb[ib].o0 < o1 {
				useB = true
				o1 = max(o1, sb[ib].o1)
				b1 = sb[ib].s1
				ib++
				expanded = true
			}
			if !expanded {
				break
			}
		}

		as := strings.Join(aLines[a0:a1], "")
		bs := strings.Join(bLines[b0:b1], "")
		switch {
		case useA && useB:
			if as == bs {
				out.WriteString(as)
			} else {
				out.WriteString(conflictText("", as, bs, target))
				conflict = true
			}
			aCur = a1
			bCur = b1
		case useA:
			out.WriteString(as)
			aCur = a1
			bCur += o1 - o0
		default:
			out.WriteString(bs)
			bCur = b1
			aCur += o1 - o0
		}
		oCur = o1
	}

	for oCur < len(oLines) {
		out.WriteString(oLines[oCur])
		oCur++
	}

	return out.String(), conflict
}

func changedSpans(o, s []string, match []int) []mergeSpan {
	var spans []mergeSpan
	i, j := 0, 0
	for i < len(o) || j < len(s) {
		for i < len(o) && j < len(s) && match[i] == j {
			i++
			j++
		}
		if i >= len(o) && j >= len(s) {
			break
		}
		o0, s0 := i, j
		next := -1
		for k := i; k < len(o); k++ {
			if match[k] >= j {
				next = k
				break
			}
		}
		if next < 0 {
			spans = append(spans, mergeSpan{o0, len(o), s0, len(s)})
			break
		}
		spans = append(spans, mergeSpan{o0, next, s0, match[next]})
		i = next
		j = match[next]
	}
	return spans
}

func conflictText(_, ours, theirs string, target plumbing.Hash) string {
	label := target.String()
	if target.IsZero() {
		label = ""
	}
	var b strings.Builder
	b.WriteString("<<<<<<< HEAD\n")
	b.WriteString(ensureLines(ours))
	b.WriteString("=======\n")
	b.WriteString(ensureLines(theirs))
	if label != "" {
		b.WriteString(">>>>>>> ")
		b.WriteString(label)
		b.WriteByte('\n')
	} else {
		b.WriteString(">>>>>>>\n")
	}
	return b.String()
}

func ensureLines(s string) string {
	if s == "" {
		return ""
	}
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

func splitMergeLines(s string) []string {
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

func lcsMatches(a, b []string) []int {
	match := make([]int, len(a))
	for i := range match {
		match[i] = -1
	}
	n, m := len(a), len(b)
	if n == 0 || m == 0 {
		return match
	}

	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	i, j := 0, 0
	for i < n && j < m {
		if a[i] == b[j] {
			match[i] = j
			i++
			j++
			continue
		}
		if dp[i+1][j] >= dp[i][j+1] {
			i++
		} else {
			j++
		}
	}
	return match
}

func removeIndexEntriesByName(idx *index.Index, filename string) {
	filename = strings.ReplaceAll(filename, "\\", "/")
	filename = path.Clean(filename)
	if filename == "." {
		filename = ""
	}
	filtered := idx.Entries[:0]
	for _, e := range idx.Entries {
		if e.Name != filename {
			filtered = append(filtered, e)
		}
	}
	// If all entries were removed, keep a non-nil empty slice.
	if len(filtered) == 0 {
		idx.Entries = []*index.Entry{}
		return
	}
	idx.Entries = filtered
}

func indexHasConflictStages(idx *index.Index, filename string) bool {
	for _, e := range idx.Entries {
		if e.Name == filename && e.Stage != 0 {
			return true
		}
	}
	return false
}
