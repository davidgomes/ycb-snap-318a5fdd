package git

import (
	"bytes"
	"errors"
	"os"
	"testing"

	"github.com/go-git/go-billy/v6"
	"github.com/go-git/go-billy/v6/memfs"
	"github.com/go-git/go-billy/v6/util"
	"github.com/stretchr/testify/require"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/format/index"
	"github.com/go-git/go-git/v6/storage/memory"
)

func TestMergeLinesNonOverlappingAndConflicts(t *testing.T) {
	t.Parallel()
	label := "abc"

	merged, conflict := mergeFileText(
		"L1\nL2\nL3\nL4\nL5\n",
		"L1\nOURS\nL3\nL4\nL5\n",
		"L1\nL2\nL3\nL4\nTHEIRS\n",
		label,
	)
	require.False(t, conflict)
	require.Equal(t, "L1\nOURS\nL3\nL4\nTHEIRS\n", merged)

	merged, conflict = mergeFileText(
		"A\nB\nC\nD\n",
		"A\nOURS\nC\nD\n",
		"A\nB\nTHEIRS\nD\n",
		label,
	)
	require.True(t, conflict)
	require.Equal(t, "A\n<<<<<<< HEAD\nOURS\nC\n=======\nB\nTHEIRS\n>>>>>>> abc\nD\n", merged)

	merged, conflict = mergeFileText(
		"A\nB\nC\nD\n",
		"A\nOURS\nC\nD\n",
		"A\nTHEIRS\nC\nD\n",
		label,
	)
	require.True(t, conflict)
	require.Equal(t, "A\n<<<<<<< HEAD\nOURS\n=======\nTHEIRS\n>>>>>>> abc\nC\nD\n", merged)

	merged, conflict = mergeFileText(
		"A\nB\nC\nD\n",
		"A\nX\nB\nC\nD\n",
		"A\nY\nB\nC\nD\n",
		label,
	)
	require.True(t, conflict)
	require.Equal(t, "A\n<<<<<<< HEAD\nX\n=======\nY\n>>>>>>> abc\nB\nC\nD\n", merged)

	merged, conflict = mergeFileText(
		"a\na\n",
		"a\nAAA\na\n",
		"a\nBBB\na\n",
		label,
	)
	require.True(t, conflict)
	require.Contains(t, merged, "<<<<<<< HEAD")
	require.Contains(t, merged, "AAA")
	require.Contains(t, merged, "BBB")

	merged, conflict = mergeFileText(
		"a\na\n",
		"a\nAAA\na\n",
		"a\na\nBBB\n",
		label,
	)
	require.False(t, conflict)
	require.Equal(t, "a\nAAA\na\nBBB\n", merged)

	merged, conflict = mergeFileText(
		"A\nB\nC\n",
		"A\nX\nB\nC\n",
		"A\nTHEIRS\nC\n",
		label,
	)
	require.True(t, conflict)
	require.Contains(t, merged, "<<<<<<< HEAD")

	merged, conflict = mergeFileText(
		"A\nB\nC\n",
		"A\nB\nX\nC\n",
		"A\nTHEIRS\nC\n",
		label,
	)
	require.True(t, conflict)
	require.Contains(t, merged, "X")
	require.Contains(t, merged, "THEIRS")

	merged, conflict = mergeFileText("A\nB\n", "A\nZ\n", "A\nZ\n", label)
	require.False(t, conflict)
	require.Equal(t, "A\nZ\n", merged)

	merged, conflict = mergeFileText(
		"A\nB\nC\nD\nE\n",
		"A\nO1\nC\nD\nE\n",
		"A\nT1\nC\nD\nT2\n",
		label,
	)
	require.True(t, conflict)
	require.Equal(t, "A\n<<<<<<< HEAD\nO1\n=======\nT1\n>>>>>>> abc\nC\nD\nT2\n", merged)

	merged, conflict = mergeFileText(
		"A\nB\nC\nD\nE\n",
		"A\nO1\nC\nD\nO2\n",
		"A\nT1\nC\nD\nT2\n",
		label,
	)
	require.True(t, conflict)
	require.Equal(t, "A\n<<<<<<< HEAD\nO1\n=======\nT1\n>>>>>>> abc\nC\nD\n<<<<<<< HEAD\nO2\n=======\nT2\n>>>>>>> abc\n", merged)
}

func TestMergeFastForward(t *testing.T) {
	t.Parallel()
	w, r, fs := newMergeRepo(t)
	commitFile(t, w, fs, "a.txt", "a\n", "base")
	base, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("side"),
		Create: true,
		Hash:   base.Hash(),
	}))
	commitFile(t, w, fs, "b.txt", "b\n", "side")
	side, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	require.NoError(t, w.Merge(side.Hash(), &MergeOptions{}))

	head, err := r.Head()
	require.NoError(t, err)
	require.Equal(t, side.Hash(), head.Hash())
	got, err := util.ReadFile(fs, "b.txt")
	require.NoError(t, err)
	require.Equal(t, "b\n", string(got))

	commit, err := r.CommitObject(head.Hash())
	require.NoError(t, err)
	require.Len(t, commit.ParentHashes, 1)
}

func TestMergeAlreadyUpToDate(t *testing.T) {
	t.Parallel()
	w, r, fs := newMergeRepo(t)
	commitFile(t, w, fs, "a.txt", "a\n", "base")
	head, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("side"),
		Create: true,
		Hash:   head.Hash(),
	}))
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	commitFile(t, w, fs, "b.txt", "b\n", "ahead")
	ahead, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Merge(head.Hash(), &MergeOptions{}))
	got, err := r.Head()
	require.NoError(t, err)
	require.Equal(t, ahead.Hash(), got.Hash())
}

func TestMergeCommitWithoutUserConfig(t *testing.T) {
	t.Parallel()
	w, r, fs := newMergeRepo(t)
	commitFile(t, w, fs, "base.txt", "L1\nL2\nL3\nL4\nL5\n", "base")
	base, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("side"),
		Create: true,
		Hash:   base.Hash(),
	}))
	commitFile(t, w, fs, "base.txt", "L1\nL2\nL3\nL4\nTHEIRS\n", "theirs")
	commitFile(t, w, fs, "only-theirs.txt", "t\n", "theirs-file")
	side, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	commitFile(t, w, fs, "base.txt", "L1\nOURS\nL3\nL4\nL5\n", "ours")
	commitFile(t, w, fs, "only-ours.txt", "o\n", "ours-file")

	require.NoError(t, w.Merge(side.Hash(), &MergeOptions{}))

	head, err := r.Head()
	require.NoError(t, err)
	require.NotEqual(t, side.Hash(), head.Hash())
	commit, err := r.CommitObject(head.Hash())
	require.NoError(t, err)
	require.Equal(t, []plumbing.Hash{mustHeadBefore(t, commit.ParentHashes[0], r), side.Hash()}, commit.ParentHashes)
	require.Contains(t, commit.Message, side.Hash().String())
	require.NotEmpty(t, commit.Author.Name)
	require.NotEmpty(t, commit.Author.Email)

	got, err := util.ReadFile(fs, "base.txt")
	require.NoError(t, err)
	require.Equal(t, "L1\nOURS\nL3\nL4\nTHEIRS\n", string(got))
	got, err = util.ReadFile(fs, "only-theirs.txt")
	require.NoError(t, err)
	require.Equal(t, "t\n", string(got))
	got, err = util.ReadFile(fs, "only-ours.txt")
	require.NoError(t, err)
	require.Equal(t, "o\n", string(got))

	_, err = fs.Open(mergeHeadPath)
	require.Error(t, err)
	require.True(t, os.IsNotExist(err))
}

func TestMergeContentConflictAndResolve(t *testing.T) {
	t.Parallel()
	w, r, fs := newMergeRepo(t)
	commitFile(t, w, fs, "both.txt", "A\nB\nC\nD\n", "base")
	commitFile(t, w, fs, "clean.txt", "keep\n", "base-clean")
	base, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("side"),
		Create: true,
		Hash:   base.Hash(),
	}))
	commitFile(t, w, fs, "both.txt", "A\nB\nTHEIRS\nD\n", "theirs")
	commitFile(t, w, fs, "added-theirs.txt", "new\n", "added")
	side, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	commitFile(t, w, fs, "both.txt", "A\nOURS\nC\nD\n", "ours")
	oursHead, err := r.Head()
	require.NoError(t, err)

	err = w.Merge(side.Hash(), &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)

	head, err := r.Head()
	require.NoError(t, err)
	require.Equal(t, oursHead.Hash(), head.Hash())

	body, err := util.ReadFile(fs, "both.txt")
	require.NoError(t, err)
	require.Contains(t, string(body), "<<<<<<< HEAD")
	require.Contains(t, string(body), "=======")
	require.Contains(t, string(body), ">>>>>>> "+side.Hash().String())
	require.Contains(t, string(body), "OURS")
	require.Contains(t, string(body), "THEIRS")

	// Non-conflicting paths are still merged.
	got, err := util.ReadFile(fs, "added-theirs.txt")
	require.NoError(t, err)
	require.Equal(t, "new\n", string(got))
	got, err = util.ReadFile(fs, "clean.txt")
	require.NoError(t, err)
	require.Equal(t, "keep\n", string(got))

	mh, err := util.ReadFile(fs, mergeHeadPath)
	require.NoError(t, err)
	require.Equal(t, side.Hash().String()+"\n", string(mh))

	idx, err := r.Storer.Index()
	require.NoError(t, err)
	st := stagesFor(idx, "both.txt")
	require.Len(t, st, 3)
	require.Contains(t, st, index.AncestorMode)
	require.Contains(t, st, index.OurMode)
	require.Contains(t, st, index.TheirMode)
	require.NotContains(t, stageNumbers(idx, "both.txt"), index.Stage(0))
	require.Equal(t, []index.Stage{0}, stageNumbers(idx, "added-theirs.txt"))
	require.Equal(t, []index.Stage{0}, stageNumbers(idx, "clean.txt"))

	require.NoError(t, util.WriteFile(fs, "both.txt", []byte("A\nOURS\nTHEIRS\nD\n"), 0o644))
	_, err = w.Add("both.txt")
	require.NoError(t, err)
	idx, err = r.Storer.Index()
	require.NoError(t, err)
	require.Equal(t, []index.Stage{0}, stageNumbers(idx, "both.txt"))

	hash, err := w.Commit("resolved\n", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)
	commit, err := r.CommitObject(hash)
	require.NoError(t, err)
	require.Equal(t, []plumbing.Hash{oursHead.Hash(), side.Hash()}, commit.ParentHashes)
	_, err = fs.Open(mergeHeadPath)
	require.True(t, os.IsNotExist(err))
}

func TestMergeDeleteModifyAndAddAdd(t *testing.T) {
	t.Parallel()
	w, r, fs := newMergeRepo(t)
	commitFile(t, w, fs, "del.txt", "base\n", "base")
	base, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("side"),
		Create: true,
		Hash:   base.Hash(),
	}))
	require.NoError(t, fs.Remove("del.txt"))
	_, err = w.Add("del.txt")
	require.NoError(t, err)
	require.NoError(t, util.WriteFile(fs, "add.txt", []byte("theirs\n"), 0o644))
	_, err = w.Add("add.txt")
	require.NoError(t, err)
	_, err = w.Commit("theirs\n", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)
	side, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	commitFile(t, w, fs, "del.txt", "ours modified\n", "ours")
	commitFile(t, w, fs, "add.txt", "ours\n", "ours-add")

	err = w.Merge(side.Hash(), &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)

	body, err := util.ReadFile(fs, "del.txt")
	require.NoError(t, err)
	require.Equal(t, "ours modified\n", string(body))
	require.NotContains(t, string(body), "<<<<<<<")

	idx, err := r.Storer.Index()
	require.NoError(t, err)
	delStages := stageNumbers(idx, "del.txt")
	require.Equal(t, []index.Stage{index.AncestorMode, index.OurMode}, delStages)
	addStages := stageNumbers(idx, "add.txt")
	require.Equal(t, []index.Stage{index.OurMode, index.TheirMode}, addStages)

	addBody, err := util.ReadFile(fs, "add.txt")
	require.NoError(t, err)
	require.Contains(t, string(addBody), "<<<<<<< HEAD")
	require.Contains(t, string(addBody), "ours")
	require.Contains(t, string(addBody), "theirs")

	mh, err := util.ReadFile(fs, mergeHeadPath)
	require.NoError(t, err)
	require.Equal(t, side.Hash().String()+"\n", string(mh))
}

func TestMergeAddAddIdentical(t *testing.T) {
	t.Parallel()
	w, r, fs := newMergeRepo(t)
	commitFile(t, w, fs, "base.txt", "base\n", "base")
	base, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("side"),
		Create: true,
		Hash:   base.Hash(),
	}))
	commitFile(t, w, fs, "same.txt", "same\n", "theirs")
	commitFile(t, w, fs, "only.txt", "t\n", "only")
	side, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	commitFile(t, w, fs, "same.txt", "same\n", "ours")

	require.NoError(t, w.Merge(side.Hash(), &MergeOptions{}))
	got, err := util.ReadFile(fs, "same.txt")
	require.NoError(t, err)
	require.Equal(t, "same\n", string(got))
	idx, err := r.Storer.Index()
	require.NoError(t, err)
	require.Equal(t, []index.Stage{0}, stageNumbers(idx, "same.txt"))
}

func TestMergeFileDirectoryConflict(t *testing.T) {
	t.Parallel()
	w, r, fs := newMergeRepo(t)
	commitFile(t, w, fs, "a.txt", "a\n", "base")
	base, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("side"),
		Create: true,
		Hash:   base.Hash(),
	}))
	require.NoError(t, fs.MkdirAll("clash", 0o755))
	require.NoError(t, util.WriteFile(fs, "clash/inside.txt", []byte("dir\n"), 0o644))
	_, err = w.Add("clash/inside.txt")
	require.NoError(t, err)
	_, err = w.Commit("dir\n", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)
	side, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	commitFile(t, w, fs, "clash", "file\n", "file")

	err = w.Merge(side.Hash(), &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)

	body, err := util.ReadFile(fs, "clash~HEAD")
	require.NoError(t, err)
	require.Equal(t, "file\n", string(body))
	inside, err := util.ReadFile(fs, "clash/inside.txt")
	require.NoError(t, err)
	require.Equal(t, "dir\n", string(inside))

	idx, err := r.Storer.Index()
	require.NoError(t, err)
	require.Equal(t, []index.Stage{index.OurMode}, stageNumbers(idx, "clash~HEAD"))
	require.Empty(t, stageNumbers(idx, "clash"))
	require.Equal(t, []index.Stage{0}, stageNumbers(idx, "clash/inside.txt"))

	mh, err := util.ReadFile(fs, mergeHeadPath)
	require.NoError(t, err)
	require.Equal(t, side.Hash().String()+"\n", string(mh))
}

func TestMergeDirtyWorktree(t *testing.T) {
	t.Parallel()
	w, r, fs := newMergeRepo(t)
	commitFile(t, w, fs, "a.txt", "a\n", "base")
	base, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("side"),
		Create: true,
		Hash:   base.Hash(),
	}))
	commitFile(t, w, fs, "b.txt", "b\n", "side")
	side, err := r.Head()
	require.NoError(t, err)
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))

	require.NoError(t, util.WriteFile(fs, "a.txt", []byte("dirty\n"), 0o644))
	err = w.Merge(side.Hash(), &MergeOptions{})
	require.ErrorIs(t, err, ErrUncommittedChanges)

	head, err := r.Head()
	require.NoError(t, err)
	require.Equal(t, base.Hash(), head.Hash())
	_, err = fs.Open(mergeHeadPath)
	require.True(t, errors.Is(err, os.ErrNotExist))
}

func TestMergeModeChangeWithContentAndBinary(t *testing.T) {
	t.Parallel()
	w, r, fs := newMergeRepo(t)
	commitFile(t, w, fs, "m.txt", "x\n", "base")
	commitFile(t, w, fs, "b.bin", "a\n", "base-bin")
	base, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("side"),
		Create: true,
		Hash:   base.Hash(),
	}))
	require.NoError(t, w.Filesystem.(interface {
		Chmod(string, os.FileMode) error
	}).Chmod("m.txt", 0o755))
	_, err = w.Add("m.txt")
	require.NoError(t, err)
	_, err = w.Commit("mode\n", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)
	require.NoError(t, util.WriteFile(fs, "b.bin", []byte("a\x00b\n"), 0o644))
	_, err = w.Add("b.bin")
	require.NoError(t, err)
	_, err = w.Commit("bin\n", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)
	side, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	commitFile(t, w, fs, "m.txt", "y\n", "content")
	commitFile(t, w, fs, "b.bin", "a\x00c\n", "bin-ours")

	err = w.Merge(side.Hash(), &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)

	got, err := util.ReadFile(fs, "m.txt")
	require.NoError(t, err)
	require.Equal(t, "y\n", string(got))
	got, err = util.ReadFile(fs, "b.bin")
	require.NoError(t, err)
	require.Equal(t, "a\x00c\n", string(got))

	idx, err := r.Storer.Index()
	require.NoError(t, err)
	require.Equal(t, []index.Stage{0}, stageNumbers(idx, "m.txt"))
	m := stagesFor(idx, "m.txt")
	// Content from ours, executable bit from theirs.
	require.Equal(t, filemode.Executable, modeOf(idx, "m.txt"))
	require.NotEqual(t, plumbing.ZeroHash, m[0])
	require.Equal(t, []index.Stage{index.AncestorMode, index.OurMode, index.TheirMode}, stageNumbers(idx, "b.bin"))
	_ = m
}

func TestMergeBothDeleted(t *testing.T) {
	t.Parallel()
	w, r, fs := newMergeRepo(t)
	commitFile(t, w, fs, "gone.txt", "g\n", "base")
	commitFile(t, w, fs, "stay.txt", "s\n", "base-stay")
	base, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("side"),
		Create: true,
		Hash:   base.Hash(),
	}))
	require.NoError(t, fs.Remove("gone.txt"))
	_, err = w.Add("gone.txt")
	require.NoError(t, err)
	_, err = w.Commit("del\n", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)
	side, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	require.NoError(t, fs.Remove("gone.txt"))
	_, err = w.Add("gone.txt")
	require.NoError(t, err)
	commitFile(t, w, fs, "stay.txt", "ours\n", "ours")

	require.NoError(t, w.Merge(side.Hash(), &MergeOptions{}))
	_, err = fs.Stat("gone.txt")
	require.True(t, os.IsNotExist(err))
	got, err := util.ReadFile(fs, "stay.txt")
	require.NoError(t, err)
	require.Equal(t, "ours\n", string(got))
}

func TestMergeUntrackedDoesNotBlock(t *testing.T) {
	t.Parallel()
	w, r, fs := newMergeRepo(t)
	commitFile(t, w, fs, "a.txt", "a\n", "base")
	base, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("side"),
		Create: true,
		Hash:   base.Hash(),
	}))
	commitFile(t, w, fs, "b.txt", "b\n", "side")
	side, err := r.Head()
	require.NoError(t, err)
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	require.NoError(t, util.WriteFile(fs, "notes.txt", []byte("untracked\n"), 0o644))

	require.NoError(t, w.Merge(side.Hash(), &MergeOptions{}))
	got, err := util.ReadFile(fs, "notes.txt")
	require.NoError(t, err)
	require.Equal(t, "untracked\n", string(got))
}

func TestMergeStagedChangeIsDirty(t *testing.T) {
	t.Parallel()
	w, r, fs := newMergeRepo(t)
	commitFile(t, w, fs, "a.txt", "a\n", "base")
	base, err := r.Head()
	require.NoError(t, err)
	require.NoError(t, w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("side"),
		Create: true,
		Hash:   base.Hash(),
	}))
	commitFile(t, w, fs, "b.txt", "b\n", "side")
	side, err := r.Head()
	require.NoError(t, err)
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	require.NoError(t, util.WriteFile(fs, "a.txt", []byte("staged\n"), 0o644))
	_, err = w.Add("a.txt")
	require.NoError(t, err)
	err = w.Merge(side.Hash(), &MergeOptions{})
	require.ErrorIs(t, err, ErrUncommittedChanges)
}

func TestMergeUnchangedDirectoryYieldsToFile(t *testing.T) {
	t.Parallel()
	w, r, fs := newMergeRepo(t)
	require.NoError(t, fs.MkdirAll("clash", 0o755))
	commitFile(t, w, fs, "clash/inside.txt", "dir\n", "base")
	commitFile(t, w, fs, "a.txt", "keep\n", "base-a")
	base, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("side"),
		Create: true,
		Hash:   base.Hash(),
	}))
	require.NoError(t, fs.Remove("clash/inside.txt"))
	require.NoError(t, fs.Remove("clash"))
	_, err = w.Add("clash/inside.txt")
	require.NoError(t, err)
	commitFile(t, w, fs, "clash", "file\n", "file")
	side, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	commitFile(t, w, fs, "a.txt", "ours\n", "ours")

	require.NoError(t, w.Merge(side.Hash(), &MergeOptions{}))
	got, err := util.ReadFile(fs, "clash")
	require.NoError(t, err)
	require.Equal(t, "file\n", string(got))
	_, err = fs.Stat("clash/inside.txt")
	require.True(t, os.IsNotExist(err))
	idx, err := r.Storer.Index()
	require.NoError(t, err)
	require.Equal(t, []index.Stage{0}, stageNumbers(idx, "clash"))
	require.Empty(t, stageNumbers(idx, "clash/inside.txt"))
}

func TestMergeRepeatedLineConflict(t *testing.T) {
	t.Parallel()
	w, r, fs := newMergeRepo(t)
	commitFile(t, w, fs, "r.txt", "a\na\n", "base")
	base, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("side"),
		Create: true,
		Hash:   base.Hash(),
	}))
	commitFile(t, w, fs, "r.txt", "a\nBBB\na\n", "theirs")
	side, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	commitFile(t, w, fs, "r.txt", "a\nAAA\na\n", "ours")

	err = w.Merge(side.Hash(), &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)
	body, err := util.ReadFile(fs, "r.txt")
	require.NoError(t, err)
	require.Contains(t, string(body), "<<<<<<< HEAD")
	require.Contains(t, string(body), "AAA")
	require.Contains(t, string(body), "BBB")
	require.Contains(t, string(body), ">>>>>>>")
}

func newMergeRepo(t *testing.T) (*Worktree, *Repository, billyFS) {
	t.Helper()
	fs := memfs.New()
	r, err := Init(memory.NewStorage(), WithWorkTree(fs))
	require.NoError(t, err)
	w, err := r.Worktree()
	require.NoError(t, err)
	return w, r, fs
}

// billyFS is the worktree filesystem used by merge tests.
type billyFS interface {
	billy.Basic
	billy.Dir
}

func commitFile(t *testing.T, w *Worktree, fs billyFS, name, content, msg string) {
	t.Helper()
	// memfs satisfies util.WriteFile via the concrete type; use the worktree filesystem.
	require.NoError(t, util.WriteFile(w.Filesystem, name, []byte(content), 0o644))
	_, err := w.Add(name)
	require.NoError(t, err)
	_, err = w.Commit(msg+"\n", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)
	_ = fs
}

func modeOf(idx *index.Index, name string) filemode.FileMode {
	for _, e := range idx.Entries {
		if e.Name == name && e.Stage == 0 {
			return e.Mode
		}
	}
	return 0
}

func stagesFor(idx *index.Index, name string) map[index.Stage]plumbing.Hash {
	out := map[index.Stage]plumbing.Hash{}
	for _, e := range idx.Entries {
		if e.Name == name {
			out[e.Stage] = e.Hash
		}
	}
	return out
}

func stageNumbers(idx *index.Index, name string) []index.Stage {
	var stages []index.Stage
	for _, e := range idx.Entries {
		if e.Name == name {
			stages = append(stages, e.Stage)
		}
	}
	return stages
}

func mustHeadBefore(t *testing.T, parent plumbing.Hash, r *Repository) plumbing.Hash {
	t.Helper()
	_, err := r.CommitObject(parent)
	require.NoError(t, err)
	return parent
}

func TestMergeDeleteModifyOtherSide(t *testing.T) {
	t.Parallel()
	w, r, fs := newMergeRepo(t)
	commitFile(t, w, fs, "f.txt", "base\n", "base")
	base, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("side"),
		Create: true,
		Hash:   base.Hash(),
	}))
	commitFile(t, w, fs, "f.txt", "theirs\n", "theirs")
	side, err := r.Head()
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	require.NoError(t, w.Filesystem.Remove("f.txt"))
	_, err = w.Add("f.txt")
	require.NoError(t, err)
	_, err = w.Commit("delete\n", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	err = w.Merge(side.Hash(), &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)
	body, err := util.ReadFile(w.Filesystem, "f.txt")
	require.NoError(t, err)
	require.Equal(t, "theirs\n", string(body))
	idx, err := r.Storer.Index()
	require.NoError(t, err)
	require.Equal(t, []index.Stage{index.AncestorMode, index.TheirMode}, stageNumbers(idx, "f.txt"))
	require.False(t, bytes.Contains(body, []byte("<<<<<<<")))
}
