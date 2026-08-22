package git

import (
	"os"
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

func initMergeRepo(t *testing.T) (*Repository, *Worktree, billy.Filesystem) {
	t.Helper()
	fs := memfs.New()
	r, err := Init(memory.NewStorage(), WithWorkTree(fs))
	require.NoError(t, err)
	w, err := r.Worktree()
	require.NoError(t, err)
	return r, w, fs
}

func writeAddCommit(t *testing.T, w *Worktree, fs billy.Filesystem, name, content, msg string) plumbing.Hash {
	t.Helper()
	require.NoError(t, util.WriteFile(fs, name, []byte(content), 0o644))
	_, err := w.Add(name)
	require.NoError(t, err)
	h, err := w.Commit(msg, &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)
	return h
}

func checkoutNewBranch(t *testing.T, w *Worktree, name string) {
	t.Helper()
	err := w.Checkout(&CheckoutOptions{
		Create: true,
		Branch: plumbing.NewBranchReferenceName(name),
	})
	require.NoError(t, err)
}

func checkoutBranch(t *testing.T, w *Worktree, name string) {
	t.Helper()
	err := w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName(name),
	})
	require.NoError(t, err)
}

func TestWorktreeMergeFastForward(t *testing.T) {
	r, w, fs := initMergeRepo(t)
	writeAddCommit(t, w, fs, "a.txt", "base\n", "base")
	checkoutNewBranch(t, w, "feature")
	feature := writeAddCommit(t, w, fs, "a.txt", "base\nfeature\n", "feature")
	checkoutBranch(t, w, "master")

	err := w.Merge(feature, &MergeOptions{})
	require.NoError(t, err)

	head, err := r.Head()
	require.NoError(t, err)
	require.Equal(t, feature, head.Hash())

	got, err := util.ReadFile(fs, "a.txt")
	require.NoError(t, err)
	require.Equal(t, "base\nfeature\n", string(got))
}

func TestWorktreeMergeThreeWayClean(t *testing.T) {
	r, w, fs := initMergeRepo(t)
	writeAddCommit(t, w, fs, "shared.txt", "line1\nline2\nline3\n", "base")
	checkoutNewBranch(t, w, "feature")
	feature := writeAddCommit(t, w, fs, "shared.txt", "line1\nfeature\nline3\n", "feature")
	checkoutBranch(t, w, "master")
	writeAddCommit(t, w, fs, "shared.txt", "line1\nline2\nmaster\n", "master")

	err := w.Merge(feature, &MergeOptions{})
	require.NoError(t, err)

	got, err := util.ReadFile(fs, "shared.txt")
	require.NoError(t, err)
	require.Equal(t, "line1\nfeature\nmaster\n", string(got))

	head, err := r.Head()
	require.NoError(t, err)
	c, err := r.CommitObject(head.Hash())
	require.NoError(t, err)
	require.Len(t, c.ParentHashes, 2)
	require.Equal(t, feature, c.ParentHashes[1])
}

func TestWorktreeMergeWithoutUserConfig(t *testing.T) {
	_, w, fs := initMergeRepo(t)
	writeAddCommit(t, w, fs, "a.txt", "one\n", "base")
	checkoutNewBranch(t, w, "feature")
	feature := writeAddCommit(t, w, fs, "b.txt", "two\n", "feature")
	checkoutBranch(t, w, "master")
	writeAddCommit(t, w, fs, "c.txt", "three\n", "master")

	err := w.Merge(feature, &MergeOptions{})
	require.NoError(t, err)
}

func TestWorktreeMergePartialNonConflictFile(t *testing.T) {
	_, w, fs := initMergeRepo(t)
	require.NoError(t, util.WriteFile(fs, "conflict.txt", []byte("base\n"), 0o644))
	require.NoError(t, util.WriteFile(fs, "clean.txt", []byte("clean-base\n"), 0o644))
	_, err := w.Add("conflict.txt")
	require.NoError(t, err)
	_, err = w.Add("clean.txt")
	require.NoError(t, err)
	_, err = w.Commit("base", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	checkoutNewBranch(t, w, "feature")
	require.NoError(t, util.WriteFile(fs, "conflict.txt", []byte("theirs\n"), 0o644))
	require.NoError(t, util.WriteFile(fs, "clean.txt", []byte("clean-theirs\n"), 0o644))
	_, err = w.Add("conflict.txt")
	require.NoError(t, err)
	_, err = w.Add("clean.txt")
	require.NoError(t, err)
	feature, err := w.Commit("feature", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	checkoutBranch(t, w, "master")
	require.NoError(t, util.WriteFile(fs, "conflict.txt", []byte("ours\n"), 0o644))
	_, err = w.Add("conflict.txt")
	require.NoError(t, err)
	_, err = w.Commit("master", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	err = w.Merge(feature, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)

	clean, err := util.ReadFile(fs, "clean.txt")
	require.NoError(t, err)
	require.Equal(t, "clean-theirs\n", string(clean))

	mh, err := util.ReadFile(fs, path.Join(GitDirName, mergeHeadFile))
	require.NoError(t, err)
	require.Equal(t, feature.String(), strings.TrimSpace(string(mh)))

	idx, err := w.r.Storer.Index()
	require.NoError(t, err)
	var stages []index.Stage
	for _, e := range idx.Entries {
		if e.Name == "conflict.txt" {
			stages = append(stages, e.Stage)
		}
	}
	require.ElementsMatch(t, []index.Stage{index.AncestorMode, index.OurMode, index.TheirMode}, stages)
}

func TestWorktreeMergeRepeatedLineConflict(t *testing.T) {
	_, w, fs := initMergeRepo(t)
	writeAddCommit(t, w, fs, "rep.txt", "x\nx\nx\n", "base")
	checkoutNewBranch(t, w, "feature")
	feature := writeAddCommit(t, w, fs, "rep.txt", "x\nz\nx\n", "feature")
	checkoutBranch(t, w, "master")
	writeAddCommit(t, w, fs, "rep.txt", "x\ny\nx\n", "master")

	err := w.Merge(feature, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)
	got, err := util.ReadFile(fs, "rep.txt")
	require.NoError(t, err)
	require.Contains(t, string(got), "<<<<<<< HEAD")
}

func TestWorktreeMergeDeleteModify(t *testing.T) {
	_, w, fs := initMergeRepo(t)
	writeAddCommit(t, w, fs, "dm.txt", "base\n", "base")
	checkoutNewBranch(t, w, "feature")
	require.NoError(t, fs.Remove("dm.txt"))
	_, err := w.Remove("dm.txt")
	require.NoError(t, err)
	feature, err := w.Commit("delete", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	checkoutBranch(t, w, "master")
	writeAddCommit(t, w, fs, "dm.txt", "modified\n", "modify")

	err = w.Merge(feature, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)

	idx, err := w.r.Storer.Index()
	require.NoError(t, err)
	var stages []index.Stage
	for _, e := range idx.Entries {
		if e.Name == "dm.txt" {
			stages = append(stages, e.Stage)
		}
	}
	require.ElementsMatch(t, []index.Stage{index.AncestorMode, index.OurMode}, stages)
}

func TestWorktreeMergeAddAdd(t *testing.T) {
	_, w, fs := initMergeRepo(t)
	writeAddCommit(t, w, fs, "keep.txt", "keep\n", "base")
	checkoutNewBranch(t, w, "feature")
	feature := writeAddCommit(t, w, fs, "new.txt", "theirs\n", "feature add")
	checkoutBranch(t, w, "master")
	writeAddCommit(t, w, fs, "new.txt", "ours\n", "master add")

	err := w.Merge(feature, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)

	idx, err := w.r.Storer.Index()
	require.NoError(t, err)
	var stages []index.Stage
	for _, e := range idx.Entries {
		if e.Name == "new.txt" {
			stages = append(stages, e.Stage)
		}
	}
	require.ElementsMatch(t, []index.Stage{index.OurMode, index.TheirMode}, stages)
}

func TestWorktreeMergeFileVsDirectory(t *testing.T) {
	_, w, fs := initMergeRepo(t)
	writeAddCommit(t, w, fs, "keep.txt", "keep\n", "base")
	checkoutNewBranch(t, w, "feature")
	require.NoError(t, fs.MkdirAll("clash", 0o755))
	feature := writeAddCommit(t, w, fs, "clash/inner.txt", "dir\n", "feature dir")
	checkoutBranch(t, w, "master")
	writeAddCommit(t, w, fs, "clash", "file\n", "master file")

	err := w.Merge(feature, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)

	idx, err := w.r.Storer.Index()
	require.NoError(t, err)
	var stages []index.Stage
	for _, e := range idx.Entries {
		if e.Name == "clash" {
			stages = append(stages, e.Stage)
		}
	}
	require.NotEmpty(t, stages)
}

func TestWorktreeMergeUncommitted(t *testing.T) {
	_, w, fs := initMergeRepo(t)
	base := writeAddCommit(t, w, fs, "a.txt", "a\n", "base")
	checkoutNewBranch(t, w, "feature")
	feature := writeAddCommit(t, w, fs, "a.txt", "b\n", "feature")
	checkoutBranch(t, w, "master")
	require.Equal(t, base, mustHead(t, w))
	require.NoError(t, util.WriteFile(fs, "a.txt", []byte("dirty\n"), 0o644))

	err := w.Merge(feature, &MergeOptions{})
	require.ErrorIs(t, err, ErrUncommittedChanges)
}

func TestWorktreeCommitReadsMergeHead(t *testing.T) {
	r, w, fs := initMergeRepo(t)
	writeAddCommit(t, w, fs, "conflict.txt", "base\n", "base")
	checkoutNewBranch(t, w, "feature")
	feature := writeAddCommit(t, w, fs, "conflict.txt", "theirs\n", "feature")
	checkoutBranch(t, w, "master")
	writeAddCommit(t, w, fs, "conflict.txt", "ours\n", "master")

	err := w.Merge(feature, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)

	require.NoError(t, util.WriteFile(fs, "conflict.txt", []byte("resolved\n"), 0o644))
	_, err = w.Add("conflict.txt")
	require.NoError(t, err)

	idx, err := r.Storer.Index()
	require.NoError(t, err)
	var stages []index.Stage
	for _, e := range idx.Entries {
		if e.Name == "conflict.txt" {
			stages = append(stages, e.Stage)
		}
	}
	require.Equal(t, []index.Stage{0}, stages)

	h, err := w.Commit("resolve", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)
	c, err := r.CommitObject(h)
	require.NoError(t, err)
	require.Len(t, c.ParentHashes, 2)
	require.Equal(t, feature, c.ParentHashes[1])

	_, err = fs.Open(path.Join(GitDirName, mergeHeadFile))
	require.Error(t, err)
	require.True(t, os.IsNotExist(err))
}

func mustHead(t *testing.T, w *Worktree) plumbing.Hash {
	t.Helper()
	h, err := w.r.Head()
	require.NoError(t, err)
	return h.Hash()
}
