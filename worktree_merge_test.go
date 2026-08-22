package git

import (
	"io"
	"path"
	"strings"
	"testing"

	"github.com/go-git/go-billy/v6"
	"github.com/go-git/go-billy/v6/memfs"
	"github.com/go-git/go-billy/v6/util"
	"github.com/stretchr/testify/require"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/format/index"
	"github.com/go-git/go-git/v6/storage/memory"
)

func mergeTestRepo(t *testing.T) (*Repository, *Worktree, billy.Filesystem) {
	t.Helper()
	fs := memfs.New()
	r, err := Init(memory.NewStorage(), WithWorkTree(fs))
	require.NoError(t, err)
	w, err := r.Worktree()
	require.NoError(t, err)
	return r, w, fs
}

func mergeCommitFiles(t *testing.T, w *Worktree, files map[string]string, msg string) plumbing.Hash {
	t.Helper()
	for name, content := range files {
		if dir := path.Dir(name); dir != "." && dir != "" {
			require.NoError(t, w.Filesystem.MkdirAll(dir, 0o755))
		}
		require.NoError(t, util.WriteFile(w.Filesystem, name, []byte(content), 0o644))
		_, err := w.Add(name)
		require.NoError(t, err)
	}
	h, err := w.Commit(msg, &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)
	return h
}

func readWorktreeFile(t *testing.T, fs billy.Filesystem, name string) string {
	t.Helper()
	f, err := fs.Open(name)
	require.NoError(t, err)
	defer f.Close()
	data, err := io.ReadAll(f)
	require.NoError(t, err)
	return string(data)
}

func stagesFor(t *testing.T, w *Worktree, name string) []index.Stage {
	t.Helper()
	idx, err := w.r.Storer.Index()
	require.NoError(t, err)
	var stages []index.Stage
	for _, e := range idx.Entries {
		if e.Name == name {
			stages = append(stages, e.Stage)
		}
	}
	return stages
}

func TestMerge3NonOverlapping(t *testing.T) {
	base := []byte("A\nB\nC\nD\n")
	ours := []byte("A\nX\nC\nD\n")
	theirs := []byte("A\nB\nC\nY\n")
	got, conflict := merge3(base, ours, theirs, "theirs")
	require.False(t, conflict)
	require.Equal(t, "A\nX\nC\nY\n", string(got))
}

func TestMerge3OverlappingConflict(t *testing.T) {
	base := []byte("A\nB\nC\n")
	ours := []byte("A\nX\nC\n")
	theirs := []byte("A\nY\nC\n")
	got, conflict := merge3(base, ours, theirs, "abc")
	require.True(t, conflict)
	require.Contains(t, string(got), "<<<<<<< HEAD")
	require.Contains(t, string(got), "=======")
	require.Contains(t, string(got), ">>>>>>>")
	require.Contains(t, string(got), "X")
	require.Contains(t, string(got), "Y")
}

func TestMerge3RepeatedLineOverlap(t *testing.T) {
	base := []byte("foo\nfoo\nfoo\n")
	ours := []byte("foo\nbar\nfoo\n")
	theirs := []byte("foo\nbaz\nfoo\n")
	got, conflict := merge3(base, ours, theirs, "other")
	require.True(t, conflict, "overlapping edits of repeated lines must conflict")
	require.Contains(t, string(got), "<<<<<<< HEAD")
}

func TestMerge3RepeatedLineNonOverlapping(t *testing.T) {
	base := []byte("foo\nfoo\nfoo\nfoo\n")
	ours := []byte("foo\nbar\nfoo\nfoo\n")
	theirs := []byte("foo\nfoo\nbaz\nfoo\n")
	got, conflict := merge3(base, ours, theirs, "other")
	require.False(t, conflict)
	require.Equal(t, "foo\nbar\nbaz\nfoo\n", string(got))
}

func TestWorktreeMergeFastForward(t *testing.T) {
	r, w, _ := mergeTestRepo(t)
	base := mergeCommitFiles(t, w, map[string]string{"a": "one\n"}, "base")

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Hash:   base,
		Branch: plumbing.NewBranchReferenceName("feature"),
		Create: true,
	}))
	feature := mergeCommitFiles(t, w, map[string]string{"a": "two\n"}, "feature")

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	err := w.Merge(feature, &MergeOptions{})
	require.NoError(t, err)

	head, err := r.Head()
	require.NoError(t, err)
	require.Equal(t, feature, head.Hash())
	require.Equal(t, "two\n", readWorktreeFile(t, w.Filesystem, "a"))
}

func TestWorktreeMergeAlreadyUpToDate(t *testing.T) {
	_, w, _ := mergeTestRepo(t)
	base := mergeCommitFiles(t, w, map[string]string{"a": "one\n"}, "base")
	require.NoError(t, w.Checkout(&CheckoutOptions{
		Hash:   base,
		Branch: plumbing.NewBranchReferenceName("feature"),
		Create: true,
	}))
	_ = mergeCommitFiles(t, w, map[string]string{"a": "two\n"}, "feature")
	head, err := w.r.Head()
	require.NoError(t, err)
	require.NoError(t, w.Merge(base, &MergeOptions{}))
	after, err := w.r.Head()
	require.NoError(t, err)
	require.Equal(t, head.Hash(), after.Hash())
}

func TestWorktreeMergeThreeWayClean(t *testing.T) {
	r, w, _ := mergeTestRepo(t)
	base := mergeCommitFiles(t, w, map[string]string{
		"shared": "base\n",
		"keep":   "keep\n",
	}, "base")

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Hash:   base,
		Branch: plumbing.NewBranchReferenceName("feature"),
		Create: true,
	}))
	feature := mergeCommitFiles(t, w, map[string]string{"theirs": "from-feature\n"}, "feature")

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	_ = mergeCommitFiles(t, w, map[string]string{"ours": "from-master\n"}, "master")

	err := w.Merge(feature, &MergeOptions{})
	require.NoError(t, err)

	head, err := r.Head()
	require.NoError(t, err)
	c, err := r.CommitObject(head.Hash())
	require.NoError(t, err)
	require.Len(t, c.ParentHashes, 2)
	require.Equal(t, "from-master\n", readWorktreeFile(t, w.Filesystem, "ours"))
	require.Equal(t, "from-feature\n", readWorktreeFile(t, w.Filesystem, "theirs"))
	require.Equal(t, "keep\n", readWorktreeFile(t, w.Filesystem, "keep"))
}

func TestWorktreeMergeAutoMergeSameFile(t *testing.T) {
	r, w, _ := mergeTestRepo(t)
	base := mergeCommitFiles(t, w, map[string]string{"file": "A\nB\nC\nD\n"}, "base")

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Hash:   base,
		Branch: plumbing.NewBranchReferenceName("feature"),
		Create: true,
	}))
	feature := mergeCommitFiles(t, w, map[string]string{"file": "A\nB\nC\nY\n"}, "feature")

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	_ = mergeCommitFiles(t, w, map[string]string{"file": "A\nX\nC\nD\n"}, "master")

	err := w.Merge(feature, &MergeOptions{})
	require.NoError(t, err)

	head, err := r.Head()
	require.NoError(t, err)
	c, err := r.CommitObject(head.Hash())
	require.NoError(t, err)
	require.Len(t, c.ParentHashes, 2)
	require.Equal(t, "A\nX\nC\nY\n", readWorktreeFile(t, w.Filesystem, "file"))
}

func TestWorktreeMergeWithoutUserConfig(t *testing.T) {
	r, w, _ := mergeTestRepo(t)
	base := mergeCommitFiles(t, w, map[string]string{"a": "1\n"}, "base")

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Hash:   base,
		Branch: plumbing.NewBranchReferenceName("feature"),
		Create: true,
	}))
	feature := mergeCommitFiles(t, w, map[string]string{"b": "2\n"}, "feature")

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	_ = mergeCommitFiles(t, w, map[string]string{"c": "3\n"}, "master")

	err := w.Merge(feature, &MergeOptions{})
	require.NoError(t, err)
	head, err := r.Head()
	require.NoError(t, err)
	c, err := r.CommitObject(head.Hash())
	require.NoError(t, err)
	require.Len(t, c.ParentHashes, 2)
}

func TestWorktreeMergeContentConflict(t *testing.T) {
	r, w, fs := mergeTestRepo(t)
	base := mergeCommitFiles(t, w, map[string]string{
		"conflict": "A\nB\nC\n",
		"ok":       "ok\n",
	}, "base")

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Hash:   base,
		Branch: plumbing.NewBranchReferenceName("feature"),
		Create: true,
	}))
	feature := mergeCommitFiles(t, w, map[string]string{
		"conflict": "A\nY\nC\n",
		"ok":       "ok-feature\n",
	}, "feature")

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	_ = mergeCommitFiles(t, w, map[string]string{"conflict": "A\nX\nC\n"}, "master")

	err := w.Merge(feature, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)

	content := readWorktreeFile(t, fs, "conflict")
	require.Contains(t, content, "<<<<<<< HEAD")
	require.Contains(t, content, "=======")
	require.Contains(t, content, ">>>>>>>")
	require.Contains(t, content, "X")
	require.Contains(t, content, "Y")

	require.Equal(t, "ok-feature\n", readWorktreeFile(t, fs, "ok"))

	require.Equal(t, []index.Stage{index.AncestorMode, index.OurMode, index.TheirMode}, stagesFor(t, w, "conflict"))
	require.Equal(t, []index.Stage{0}, stagesFor(t, w, "ok"))

	mh := readWorktreeFile(t, fs, mergeHeadPath)
	require.Equal(t, feature.String(), strings.TrimSpace(mh))

	_, err = r.Storer.Reference("MERGE_HEAD")
	require.Error(t, err)
}

func TestWorktreeMergeDeleteModify(t *testing.T) {
	_, w, fs := mergeTestRepo(t)
	base := mergeCommitFiles(t, w, map[string]string{"file": "base\n"}, "base")

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Hash:   base,
		Branch: plumbing.NewBranchReferenceName("feature"),
		Create: true,
	}))
	require.NoError(t, w.Filesystem.Remove("file"))
	_, err := w.Add("file")
	require.NoError(t, err)
	feature, err := w.Commit("delete", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	_ = mergeCommitFiles(t, w, map[string]string{"file": "modified\n"}, "modify")

	err = w.Merge(feature, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)
	require.Equal(t, []index.Stage{index.AncestorMode, index.OurMode}, stagesFor(t, w, "file"))
	require.Equal(t, feature.String(), strings.TrimSpace(readWorktreeFile(t, fs, mergeHeadPath)))
	require.Equal(t, "modified\n", readWorktreeFile(t, fs, "file"))
}

func TestWorktreeMergeAddAddConflict(t *testing.T) {
	_, w, fs := mergeTestRepo(t)
	base := mergeCommitFiles(t, w, map[string]string{"keep": "x\n"}, "base")

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Hash:   base,
		Branch: plumbing.NewBranchReferenceName("feature"),
		Create: true,
	}))
	feature := mergeCommitFiles(t, w, map[string]string{"new": "theirs\n"}, "feature")

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	_ = mergeCommitFiles(t, w, map[string]string{"new": "ours\n"}, "master")

	err := w.Merge(feature, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)
	require.Equal(t, []index.Stage{index.OurMode, index.TheirMode}, stagesFor(t, w, "new"))
	content := readWorktreeFile(t, fs, "new")
	require.Contains(t, content, "<<<<<<< HEAD")
	require.Contains(t, content, "ours")
	require.Contains(t, content, "theirs")
}

func TestWorktreeMergeAddAddSameContent(t *testing.T) {
	r, w, _ := mergeTestRepo(t)
	base := mergeCommitFiles(t, w, map[string]string{"keep": "x\n"}, "base")

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Hash:   base,
		Branch: plumbing.NewBranchReferenceName("feature"),
		Create: true,
	}))
	feature := mergeCommitFiles(t, w, map[string]string{"new": "same\n"}, "feature")

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	_ = mergeCommitFiles(t, w, map[string]string{"new": "same\n"}, "master")

	err := w.Merge(feature, &MergeOptions{})
	require.NoError(t, err)
	head, err := r.Head()
	require.NoError(t, err)
	c, err := r.CommitObject(head.Hash())
	require.NoError(t, err)
	require.Len(t, c.ParentHashes, 2)
	require.Equal(t, "same\n", readWorktreeFile(t, w.Filesystem, "new"))
}

func TestWorktreeMergeFileVsDirectory(t *testing.T) {
	_, w, fs := mergeTestRepo(t)
	base := mergeCommitFiles(t, w, map[string]string{"keep": "x\n"}, "base")

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Hash:   base,
		Branch: plumbing.NewBranchReferenceName("feature"),
		Create: true,
	}))
	feature := mergeCommitFiles(t, w, map[string]string{"clash/inner": "dir\n"}, "feature")

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	_ = mergeCommitFiles(t, w, map[string]string{"clash": "file\n"}, "master")

	err := w.Merge(feature, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)
	require.Equal(t, feature.String(), strings.TrimSpace(readWorktreeFile(t, fs, mergeHeadPath)))

	idx, err := w.r.Storer.Index()
	require.NoError(t, err)
	var found bool
	for _, e := range idx.Entries {
		if e.Name == "clash" || strings.HasPrefix(e.Name, "clash/") {
			if e.Stage != 0 {
				found = true
			}
		}
	}
	require.True(t, found, "expected conflict stages for file/directory clash")
}

func TestWorktreeMergeDeleteModifyTheirs(t *testing.T) {
	_, w, fs := mergeTestRepo(t)
	base := mergeCommitFiles(t, w, map[string]string{"file": "base\n"}, "base")

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Hash:   base,
		Branch: plumbing.NewBranchReferenceName("feature"),
		Create: true,
	}))
	feature := mergeCommitFiles(t, w, map[string]string{"file": "modified\n"}, "modify")

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	require.NoError(t, w.Filesystem.Remove("file"))
	_, err := w.Add("file")
	require.NoError(t, err)
	_, err = w.Commit("delete", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	err = w.Merge(feature, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)
	require.Equal(t, []index.Stage{index.AncestorMode, index.TheirMode}, stagesFor(t, w, "file"))
	require.Equal(t, feature.String(), strings.TrimSpace(readWorktreeFile(t, fs, mergeHeadPath)))
	require.Equal(t, "modified\n", readWorktreeFile(t, fs, "file"))
}

func TestWorktreeMergeDirty(t *testing.T) {
	_, w, _ := mergeTestRepo(t)
	base := mergeCommitFiles(t, w, map[string]string{"a": "one\n"}, "base")
	require.NoError(t, w.Checkout(&CheckoutOptions{
		Hash:   base,
		Branch: plumbing.NewBranchReferenceName("feature"),
		Create: true,
	}))
	feature := mergeCommitFiles(t, w, map[string]string{"a": "two\n"}, "feature")
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	require.NoError(t, util.WriteFile(w.Filesystem, "a", []byte("dirty\n"), 0o644))

	err := w.Merge(feature, &MergeOptions{})
	require.ErrorIs(t, err, ErrUncommittedChanges)
}

func TestWorktreeMergeCommitReadsMergeHead(t *testing.T) {
	r, w, fs := mergeTestRepo(t)
	base := mergeCommitFiles(t, w, map[string]string{"file": "A\nB\nC\n"}, "base")

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Hash:   base,
		Branch: plumbing.NewBranchReferenceName("feature"),
		Create: true,
	}))
	feature := mergeCommitFiles(t, w, map[string]string{"file": "A\nY\nC\n"}, "feature")

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	ours := mergeCommitFiles(t, w, map[string]string{"file": "A\nX\nC\n"}, "master")

	err := w.Merge(feature, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)

	require.NoError(t, util.WriteFile(fs, "file", []byte("A\nZ\nC\n"), 0o644))
	_, err = w.Add("file")
	require.NoError(t, err)
	require.Equal(t, []index.Stage{0}, stagesFor(t, w, "file"))

	h, err := w.Commit("resolve", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)
	c, err := r.CommitObject(h)
	require.NoError(t, err)
	require.Len(t, c.ParentHashes, 2)
	require.Equal(t, ours, c.ParentHashes[0])
	require.Equal(t, feature, c.ParentHashes[1])

	_, err = fs.Open(mergeHeadPath)
	require.Error(t, err)
}

func TestWorktreeMergeAddClearsConflictStages(t *testing.T) {
	_, w, fs := mergeTestRepo(t)
	base := mergeCommitFiles(t, w, map[string]string{"file": "A\nB\nC\n"}, "base")

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Hash:   base,
		Branch: plumbing.NewBranchReferenceName("feature"),
		Create: true,
	}))
	feature := mergeCommitFiles(t, w, map[string]string{"file": "A\nY\nC\n"}, "feature")

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	_ = mergeCommitFiles(t, w, map[string]string{"file": "A\nX\nC\n"}, "master")

	require.ErrorIs(t, w.Merge(feature, &MergeOptions{}), ErrMergeConflicts)
	require.Len(t, stagesFor(t, w, "file"), 3)

	require.NoError(t, util.WriteFile(fs, "file", []byte("resolved\n"), 0o644))
	_, err := w.Add("file")
	require.NoError(t, err)

	idx, err := w.r.Storer.Index()
	require.NoError(t, err)
	var entries []*index.Entry
	for _, e := range idx.Entries {
		if e.Name == "file" {
			entries = append(entries, e)
		}
	}
	require.Len(t, entries, 1)
	require.Equal(t, index.Stage(0), entries[0].Stage)
	obj, err := w.r.BlobObject(entries[0].Hash)
	require.NoError(t, err)
	rd, err := obj.Reader()
	require.NoError(t, err)
	defer rd.Close()
	data, err := io.ReadAll(rd)
	require.NoError(t, err)
	require.Equal(t, "resolved\n", string(data))
}

func TestWorktreeMergeSameCommit(t *testing.T) {
	_, w, _ := mergeTestRepo(t)
	h := mergeCommitFiles(t, w, map[string]string{"a": "one\n"}, "base")
	require.NoError(t, w.Merge(h, &MergeOptions{}))
}
