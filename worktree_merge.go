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

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/format/index"
	"github.com/go-git/go-git/v6/plumbing/object"
)

const mergeHeadPath = ".git/MERGE_HEAD"

// Merge merges the commit identified by target into the current worktree.
//
// With empty MergeOptions the operation fast-forwards when target is a
// descendant of HEAD. Otherwise it performs a 3-way merge and creates a
// merge commit. Non-overlapping edits to the same file are combined
// automatically. Files without conflicts are written even when other
// paths conflict.
//
// Merge works with an empty MergeOptions value when repository user
// configuration is not set.
//
// If the worktree has uncommitted changes, Merge returns
// ErrUncommittedChanges. If conflicts remain, Merge writes conflict
// markers, records stages 1/2/3 in the index for blobs that exist,
// writes .git/MERGE_HEAD on the worktree filesystem, and returns
// ErrMergeConflicts.
func (w *Worktree) Merge(target plumbing.Hash, opts *MergeOptions) error {
	if opts == nil {
		opts = &MergeOptions{}
	}

	dirty, err := w.hasUncommittedChanges()
	if err != nil {
		return err
	}
	if dirty {
		return ErrUncommittedChanges
	}

	head, err := w.r.Head()
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
	theirs, err := w.r.CommitObject(target)
	if err != nil {
		return err
	}

	isAncestor, err := ours.IsAncestor(theirs)
	if err != nil {
		return err
	}
	if isAncestor {
		return w.Reset(&ResetOptions{Mode: HardReset, Commit: target})
	}

	theirsIsAncestor, err := theirs.IsAncestor(ours)
	if err != nil {
		return err
	}
	if theirsIsAncestor {
		return nil
	}

	return w.threeWayMerge(ours, theirs)
}

func (w *Worktree) hasUncommittedChanges() (bool, error) {
	s, err := w.Status()
	if err != nil {
		return false, err
	}
	for _, st := range s {
		if st.Staging == Untracked && st.Worktree == Untracked {
			continue
		}
		if st.Staging != Unmodified || st.Worktree != Unmodified {
			return true, nil
		}
	}
	return false, nil
}

func (w *Worktree) threeWayMerge(ours, theirs *object.Commit) error {
	bases, err := ours.MergeBase(theirs)
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

	oursTree, err := ours.Tree()
	if err != nil {
		return err
	}
	theirsTree, err := theirs.Tree()
	if err != nil {
		return err
	}

	baseSide, err := loadTreeSide(baseTree)
	if err != nil {
		return err
	}
	oursSide, err := loadTreeSide(oursTree)
	if err != nil {
		return err
	}
	theirsSide, err := loadTreeSide(theirsTree)
	if err != nil {
		return err
	}

	paths := make(map[string]struct{})
	for p := range baseSide.files {
		paths[p] = struct{}{}
	}
	for p := range oursSide.files {
		paths[p] = struct{}{}
	}
	for p := range theirsSide.files {
		paths[p] = struct{}{}
	}

	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)

	var results []fileResult
	conflicts := false
	theirsLabel := theirs.Hash.String()

	for _, name := range sorted {
		b, hasB := baseSide.files[name]
		o, hasO := oursSide.files[name]
		t, hasT := theirsSide.files[name]

		df := isFileDirConflict(name, oursSide, theirsSide)
		if df {
			conflicts = true
			res := fileResult{name: name, conflict: true}
			if hasB {
				res.stages = append(res.stages, index.Entry{Name: name, Hash: b.hash, Mode: b.mode, Stage: index.AncestorMode})
			}
			if hasO {
				res.stages = append(res.stages, index.Entry{Name: name, Hash: o.hash, Mode: o.mode, Stage: index.OurMode})
			}
			if hasT {
				res.stages = append(res.stages, index.Entry{Name: name, Hash: t.hash, Mode: t.mode, Stage: index.TheirMode})
			}
			switch {
			case oursSide.isDir(name) || hasChildFiles(name, oursSide):
				// keep the directory already in the worktree
			case theirsSide.isDir(name) || hasChildFiles(name, theirsSide):
				// directory comes from theirs; remove our file so children can be written
				res.delete = hasO
			case hasO:
				content, err := w.blobBytes(o.hash)
				if err != nil {
					return err
				}
				res.worktree = content
				res.worktreeMode = o.mode
			case hasT:
				content, err := w.blobBytes(t.hash)
				if err != nil {
					return err
				}
				res.worktree = content
				res.worktreeMode = t.mode
			}
			results = append(results, res)
			continue
		}

		switch {
		case !hasO && !hasT:
			// both deleted
			continue
		case hasO && hasT && o.hash == t.hash && o.mode == t.mode:
			results = append(results, fileResult{name: name, hash: o.hash, mode: o.mode})
		case hasO && !hasT && !hasB:
			results = append(results, fileResult{name: name, hash: o.hash, mode: o.mode})
		case hasT && !hasO && !hasB:
			results = append(results, fileResult{name: name, hash: t.hash, mode: t.mode})
		case hasO && !hasT && hasB && o.hash == b.hash && o.mode == b.mode:
			// deleted by them, unchanged by us
			continue
		case hasT && !hasO && hasB && t.hash == b.hash && t.mode == b.mode:
			// deleted by us, unchanged by them
			continue
		case hasO && !hasT && hasB:
			// delete vs modify (ours modified, theirs deleted)
			conflicts = true
			content, err := w.blobBytes(o.hash)
			if err != nil {
				return err
			}
			results = append(results, fileResult{
				name:         name,
				conflict:     true,
				worktree:     content,
				worktreeMode: o.mode,
				stages: []index.Entry{
					{Name: name, Hash: b.hash, Mode: b.mode, Stage: index.AncestorMode},
					{Name: name, Hash: o.hash, Mode: o.mode, Stage: index.OurMode},
				},
			})
		case hasT && !hasO && hasB:
			// delete vs modify (theirs modified, ours deleted)
			conflicts = true
			content, err := w.blobBytes(t.hash)
			if err != nil {
				return err
			}
			results = append(results, fileResult{
				name:         name,
				conflict:     true,
				worktree:     content,
				worktreeMode: t.mode,
				stages: []index.Entry{
					{Name: name, Hash: b.hash, Mode: b.mode, Stage: index.AncestorMode},
					{Name: name, Hash: t.hash, Mode: t.mode, Stage: index.TheirMode},
				},
			})
		case hasO && hasT && !hasB:
			// add-add
			if o.hash == t.hash {
				mode := o.mode
				results = append(results, fileResult{name: name, hash: o.hash, mode: mode})
				continue
			}
			conflicts = true
			oursContent, err := w.blobBytes(o.hash)
			if err != nil {
				return err
			}
			theirsContent, err := w.blobBytes(t.hash)
			if err != nil {
				return err
			}
			merged, hadConflict := merge3(nil, oursContent, theirsContent, theirsLabel)
			if !hadConflict {
				hash, err := w.putBlob(merged)
				if err != nil {
					return err
				}
				results = append(results, fileResult{name: name, hash: hash, mode: o.mode})
				continue
			}
			results = append(results, fileResult{
				name:         name,
				conflict:     true,
				worktree:     merged,
				worktreeMode: o.mode,
				stages: []index.Entry{
					{Name: name, Hash: o.hash, Mode: o.mode, Stage: index.OurMode},
					{Name: name, Hash: t.hash, Mode: t.mode, Stage: index.TheirMode},
				},
			})
		case hasO && hasT && hasB:
			if o.hash == b.hash && o.mode == b.mode {
				results = append(results, fileResult{name: name, hash: t.hash, mode: t.mode})
				continue
			}
			if t.hash == b.hash && t.mode == b.mode {
				results = append(results, fileResult{name: name, hash: o.hash, mode: o.mode})
				continue
			}
			if o.hash == t.hash {
				mode := o.mode
				if o.mode == b.mode {
					mode = t.mode
				}
				results = append(results, fileResult{name: name, hash: o.hash, mode: mode})
				continue
			}

			baseContent, err := w.blobBytes(b.hash)
			if err != nil {
				return err
			}
			oursContent, err := w.blobBytes(o.hash)
			if err != nil {
				return err
			}
			theirsContent, err := w.blobBytes(t.hash)
			if err != nil {
				return err
			}
			merged, hadConflict := merge3(baseContent, oursContent, theirsContent, theirsLabel)
			if !hadConflict {
				hash, err := w.putBlob(merged)
				if err != nil {
					return err
				}
				mode := o.mode
				if o.hash == b.hash {
					mode = t.mode
				}
				results = append(results, fileResult{name: name, hash: hash, mode: mode})
				continue
			}
			conflicts = true
			results = append(results, fileResult{
				name:         name,
				conflict:     true,
				worktree:     merged,
				worktreeMode: o.mode,
				stages: []index.Entry{
					{Name: name, Hash: b.hash, Mode: b.mode, Stage: index.AncestorMode},
					{Name: name, Hash: o.hash, Mode: o.mode, Stage: index.OurMode},
					{Name: name, Hash: t.hash, Mode: t.mode, Stage: index.TheirMode},
				},
			})
		}
	}

	if err := w.applyMergeResults(oursSide, results); err != nil {
		return err
	}

	if conflicts {
		if err := w.writeMergeHead(theirs.Hash); err != nil {
			return err
		}
		return ErrMergeConflicts
	}

	return w.createMergeCommit(ours, theirs)
}

func (w *Worktree) applyMergeResults(ours *treeSide, results []fileResult) error {
	keep := make(map[string]struct{}, len(results))
	for _, r := range results {
		if !r.delete && (r.worktree != nil || !r.hash.IsZero()) {
			keep[r.name] = struct{}{}
		}
	}

	for name := range ours.files {
		if _, ok := keep[name]; ok {
			continue
		}
		if err := w.removePath(name); err != nil {
			return err
		}
	}

	for _, r := range results {
		if r.worktree == nil && r.hash.IsZero() {
			continue
		}
		for dir := path.Dir(r.name); dir != "." && dir != "/" && dir != ""; dir = path.Dir(dir) {
			fi, err := w.Filesystem.Lstat(dir)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return err
			}
			if !fi.IsDir() {
				if err := w.Filesystem.Remove(dir); err != nil {
					return err
				}
			}
		}
	}

	idx, err := w.r.Storer.Index()
	if err != nil {
		return err
	}

	entries := make([]*index.Entry, 0, len(results)+len(idx.Entries))

	for _, r := range results {
		if r.conflict {
			for i := range r.stages {
				e := r.stages[i]
				entries = append(entries, &e)
			}
			if r.delete {
				if err := w.removePath(r.name); err != nil {
					return err
				}
				continue
			}
			if r.worktree != nil {
				if err := w.writeWorktreeFile(r.name, r.worktree, r.worktreeMode); err != nil {
					return err
				}
			}
			continue
		}
		if r.delete || r.hash.IsZero() {
			if err := w.removePath(r.name); err != nil {
				return err
			}
			continue
		}
		if err := w.checkoutHash(r.name, r.hash, r.mode); err != nil {
			return err
		}
		entries = append(entries, &index.Entry{
			Name: r.name,
			Hash: r.hash,
			Mode: r.mode,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Name != entries[j].Name {
			return entries[i].Name < entries[j].Name
		}
		return entries[i].Stage < entries[j].Stage
	})

	idx.Entries = entries
	if idx.Version == 0 {
		idx.Version = 2
	}
	return w.r.Storer.SetIndex(idx)
}

func (w *Worktree) createMergeCommit(ours, theirs *object.Commit) error {
	idx, err := w.r.Storer.Index()
	if err != nil {
		return err
	}

	h := &buildTreeHelper{
		fs: w.Filesystem,
		s:  w.r.Storer,
	}
	treeHash, err := h.BuildTree(idx, nil)
	if err != nil {
		return err
	}

	author := ours.Author
	if author.Name == "" {
		author.Name = "go-git"
	}
	if author.Email == "" {
		author.Email = "go-git@example.com"
	}
	author.When = time.Now()
	committer := author

	msg := fmt.Sprintf("Merge commit '%s'", theirs.Hash.String())
	commit, err := w.buildCommitObject(msg, &CommitOptions{
		Author:    &author,
		Committer: &committer,
		Parents:   []plumbing.Hash{ours.Hash, theirs.Hash},
	}, treeHash)
	if err != nil {
		return err
	}
	return w.updateHEAD(commit)
}

func (w *Worktree) writeMergeHead(h plumbing.Hash) error {
	if err := w.Filesystem.MkdirAll(GitDirName, 0o755); err != nil {
		return err
	}
	f, err := w.Filesystem.Create(mergeHeadPath)
	if err != nil {
		return err
	}
	_, err = io.WriteString(f, h.String()+"\n")
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

func (w *Worktree) readMergeHead() (plumbing.Hash, bool, error) {
	f, err := w.Filesystem.Open(mergeHeadPath)
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
	s := strings.TrimSpace(string(data))
	if s == "" {
		return plumbing.ZeroHash, false, nil
	}
	return plumbing.NewHash(s), true, nil
}

func (w *Worktree) blobBytes(h plumbing.Hash) ([]byte, error) {
	b, err := w.r.BlobObject(h)
	if err != nil {
		return nil, err
	}
	r, err := b.Reader()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func (w *Worktree) putBlob(data []byte) (plumbing.Hash, error) {
	o := w.r.Storer.NewEncodedObject()
	o.SetType(plumbing.BlobObject)
	o.SetSize(int64(len(data)))
	wr, err := o.Writer()
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
	return w.r.Storer.SetEncodedObject(o)
}

func (w *Worktree) checkoutHash(name string, h plumbing.Hash, mode filemode.FileMode) error {
	content, err := w.blobBytes(h)
	if err != nil {
		return err
	}
	return w.writeWorktreeFile(name, content, mode)
}

func (w *Worktree) writeWorktreeFile(name string, data []byte, mode filemode.FileMode) error {
	if err := w.removePath(name); err != nil {
		return err
	}
	if dir := path.Dir(name); dir != "." && dir != "" {
		if err := w.Filesystem.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	osMode, err := mode.ToOSFileMode()
	if err != nil {
		osMode = 0o644
	}
	f, err := w.Filesystem.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, osMode.Perm())
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

func (w *Worktree) removePath(name string) error {
	fi, err := w.Filesystem.Lstat(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.IsDir() {
		return w.removeDir(name)
	}
	return w.Filesystem.Remove(name)
}

func (w *Worktree) removeDir(name string) error {
	entries, err := w.Filesystem.ReadDir(name)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := w.removePath(path.Join(name, e.Name())); err != nil {
			return err
		}
	}
	return w.Filesystem.Remove(name)
}

type fileResult struct {
	name         string
	conflict     bool
	delete       bool
	hash         plumbing.Hash
	mode         filemode.FileMode
	worktree     []byte
	worktreeMode filemode.FileMode
	stages       []index.Entry
}

type sideEntry struct {
	hash plumbing.Hash
	mode filemode.FileMode
}

type treeSide struct {
	files map[string]sideEntry
	dirs  map[string]struct{}
}

func loadTreeSide(t *object.Tree) (*treeSide, error) {
	s := &treeSide{
		files: make(map[string]sideEntry),
		dirs:  make(map[string]struct{}),
	}
	if t == nil {
		return s, nil
	}
	return s, t.Files().ForEach(func(f *object.File) error {
		s.files[f.Name] = sideEntry{hash: f.Hash, mode: f.Mode}
		for dir := path.Dir(f.Name); dir != "." && dir != "/" && dir != ""; dir = path.Dir(dir) {
			s.dirs[dir] = struct{}{}
		}
		return nil
	})
}

func (s *treeSide) isDir(name string) bool {
	_, ok := s.dirs[name]
	return ok
}

func (s *treeSide) isFile(name string) bool {
	_, ok := s.files[name]
	return ok
}

func ancestorIsFile(name string, side *treeSide) bool {
	for dir := path.Dir(name); dir != "." && dir != "/" && dir != ""; dir = path.Dir(dir) {
		if side.isFile(dir) {
			return true
		}
	}
	return false
}

func hasChildFiles(name string, side *treeSide) bool {
	prefix := name + "/"
	for p := range side.files {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

func isFileDirConflict(name string, ours, theirs *treeSide) bool {
	if ours.isFile(name) && theirs.isDir(name) {
		return true
	}
	if theirs.isFile(name) && ours.isDir(name) {
		return true
	}
	if ours.isFile(name) && ancestorIsFile(name, theirs) {
		return true
	}
	if theirs.isFile(name) && ancestorIsFile(name, ours) {
		return true
	}
	return false
}

type mergeHunk struct {
	baseStart, baseEnd int
	newStart, newEnd   int
}

func merge3(base, ours, theirs []byte, theirsLabel string) ([]byte, bool) {
	if bytes.Equal(ours, theirs) {
		return ours, false
	}
	if bytes.Equal(ours, base) {
		return theirs, false
	}
	if bytes.Equal(theirs, base) {
		return ours, false
	}
	if bytes.IndexByte(base, 0) >= 0 || bytes.IndexByte(ours, 0) >= 0 || bytes.IndexByte(theirs, 0) >= 0 {
		return conflictBytes(ours, theirs, theirsLabel), true
	}

	baseLines := splitMergeLines(base)
	oursLines := splitMergeLines(ours)
	theirsLines := splitMergeLines(theirs)

	oursHunks := hunksFromMatch(baseLines, oursLines, lcsMatch(baseLines, oursLines))
	theirsHunks := hunksFromMatch(baseLines, theirsLines, lcsMatch(baseLines, theirsLines))

	merged, conflict := mergeHunks(baseLines, oursLines, theirsLines, oursHunks, theirsHunks, theirsLabel)
	return joinMergeLines(merged), conflict
}

func splitMergeLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	return strings.SplitAfter(string(b), "\n")
}

func joinMergeLines(lines []string) []byte {
	var buf bytes.Buffer
	for _, l := range lines {
		buf.WriteString(l)
	}
	return buf.Bytes()
}

func lcsMatch(a, b []string) []int {
	n, m := len(a), len(b)
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

	match := make([]int, n)
	for i := range match {
		match[i] = -1
	}
	i, j := 0, 0
	for i < n && j < m {
		if a[i] == b[j] {
			match[i] = j
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			i++
		} else {
			j++
		}
	}
	return match
}

func hunksFromMatch(base, other []string, baseToOther []int) []mergeHunk {
	var hunks []mergeHunk
	bi, oi := 0, 0
	for bi < len(base) || oi < len(other) {
		nextBi, nextOi := -1, -1
		for i := bi; i < len(base); i++ {
			if baseToOther[i] >= oi {
				nextBi = i
				nextOi = baseToOther[i]
				break
			}
		}
		if nextBi == -1 {
			if bi < len(base) || oi < len(other) {
				hunks = append(hunks, mergeHunk{bi, len(base), oi, len(other)})
			}
			break
		}
		if nextBi > bi || nextOi > oi {
			hunks = append(hunks, mergeHunk{bi, nextBi, oi, nextOi})
		}
		bi = nextBi + 1
		oi = nextOi + 1
	}
	return hunks
}

func hunksOverlap(a, b mergeHunk) bool {
	if a.baseStart == a.baseEnd && b.baseStart == b.baseEnd {
		return a.baseStart == b.baseStart
	}
	return a.baseStart < b.baseEnd && b.baseStart < a.baseEnd
}

func hunkBefore(a, b mergeHunk) bool {
	if a.baseEnd < b.baseStart {
		return true
	}
	if a.baseEnd == b.baseStart && !(a.baseStart == a.baseEnd && b.baseStart == b.baseEnd) {
		return true
	}
	return false
}

func hunkOverlapsRange(h mergeHunk, start, end int) bool {
	if h.baseStart == h.baseEnd {
		return (h.baseStart >= start && h.baseStart < end) || (start == end && h.baseStart == start)
	}
	return h.baseStart < end && start < h.baseEnd
}

func mergeHunks(base, ours, theirs []string, oursH, theirsH []mergeHunk, theirsLabel string) ([]string, bool) {
	var result []string
	conflict := false
	oi, ti := 0, 0
	pos := 0

	for oi < len(oursH) || ti < len(theirsH) {
		var oh, th *mergeHunk
		if oi < len(oursH) {
			oh = &oursH[oi]
		}
		if ti < len(theirsH) {
			th = &theirsH[ti]
		}

		if oh != nil && th != nil && hunksOverlap(*oh, *th) {
			start := min(oh.baseStart, th.baseStart)
			end := max(oh.baseEnd, th.baseEnd)
			oFirst, tFirst := oi, ti
			oi++
			ti++
			for {
				expanded := false
				if oi < len(oursH) && hunkOverlapsRange(oursH[oi], start, end) {
					start = min(start, oursH[oi].baseStart)
					end = max(end, oursH[oi].baseEnd)
					oi++
					expanded = true
				}
				if ti < len(theirsH) && hunkOverlapsRange(theirsH[ti], start, end) {
					start = min(start, theirsH[ti].baseStart)
					end = max(end, theirsH[ti].baseEnd)
					ti++
					expanded = true
				}
				if !expanded {
					break
				}
			}

			result = append(result, base[pos:start]...)
			oursSlice := otherCover(ours, oursH[oFirst:oi], start, end)
			theirsSlice := otherCover(theirs, theirsH[tFirst:ti], start, end)
			if sameLines(oursSlice, theirsSlice) {
				result = append(result, oursSlice...)
			} else {
				conflict = true
				result = append(result, conflictBlock(oursSlice, theirsSlice, theirsLabel)...)
			}
			pos = end
			continue
		}

		if oh != nil && (th == nil || hunkBefore(*oh, *th)) {
			result = append(result, base[pos:oh.baseStart]...)
			result = append(result, ours[oh.newStart:oh.newEnd]...)
			pos = oh.baseEnd
			oi++
			continue
		}

		result = append(result, base[pos:th.baseStart]...)
		result = append(result, theirs[th.newStart:th.newEnd]...)
		pos = th.baseEnd
		ti++
	}

	result = append(result, base[pos:]...)
	return result, conflict
}

func otherCover(other []string, hunks []mergeHunk, baseStart, baseEnd int) []string {
	if len(hunks) == 0 {
		return nil
	}
	first := hunks[0]
	last := hunks[len(hunks)-1]
	prefix := first.baseStart - baseStart
	if prefix < 0 {
		prefix = 0
	}
	start := first.newStart - prefix
	if start < 0 {
		start = 0
	}
	suffix := baseEnd - last.baseEnd
	if suffix < 0 {
		suffix = 0
	}
	end := last.newEnd + suffix
	if end > len(other) {
		end = len(other)
	}
	if start > end {
		return nil
	}
	return other[start:end]
}

func sameLines(a, b []string) bool {
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

func lineNeedsNL(lines []string) bool {
	if len(lines) == 0 {
		return false
	}
	last := lines[len(lines)-1]
	return last != "" && !strings.HasSuffix(last, "\n")
}

func conflictBlock(ours, theirs []string, theirsLabel string) []string {
	block := []string{"<<<<<<< HEAD\n"}
	block = append(block, ours...)
	if lineNeedsNL(ours) {
		block = append(block, "\n")
	}
	block = append(block, "=======\n")
	block = append(block, theirs...)
	if lineNeedsNL(theirs) {
		block = append(block, "\n")
	}
	if theirsLabel != "" {
		block = append(block, ">>>>>>> "+theirsLabel+"\n")
	} else {
		block = append(block, ">>>>>>>\n")
	}
	return block
}

func conflictBytes(ours, theirs []byte, theirsLabel string) []byte {
	return joinMergeLines(conflictBlock(splitMergeLines(ours), splitMergeLines(theirs), theirsLabel))
}
