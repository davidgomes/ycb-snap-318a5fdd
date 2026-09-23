package git

import (
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/go-git/go-billy/v6"
	"github.com/go-git/go-billy/v6/memfs"
	"github.com/go-git/go-billy/v6/util"
	"github.com/stretchr/testify/require"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/format/index"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/storage/memory"
)

func TestMergeTextDuplicateLines(t *testing.T) {
	t.Parallel()

	got, conflict := mergeText("a\na\na\n", "a\nX\na\na\n", "a\na\nY\na\n", "theirs")
	require.False(t, conflict)
	require.Equal(t, "a\nX\na\nY\na\n", got)

	got, conflict = mergeText("a\na\n", "a\nX\na\n", "a\nY\na\n", "deadbeef")
	require.True(t, conflict)
	require.Equal(t, "a\n<<<<<<< HEAD\nX\n=======\nY\n>>>>>>> deadbeef\na\n", got)

	got, conflict = mergeText("a\nb\nc\n", "a\nOURS\nb\nc\n", "a\nb\nc\nTHEIRS\n", "side")
	require.False(t, conflict)
	require.Equal(t, "a\nOURS\nb\nc\nTHEIRS\n", got)

	got, conflict = mergeText("k\n", "k\nours\n", "k\ntheirs\n", "side")
	require.True(t, conflict)
	require.Equal(t, "k\n<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>> side\n", got)
}

func TestMergeFastForward(t *testing.T) {
	t.Parallel()
	r, w, fs := newMergeRepo(t)

	base := commitFiles(t, w, fs, "base", map[string]string{"f.txt": "a\n"})
	require.NoError(t, w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("side"),
		Create: true,
		Hash:   base,
	}))
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	target := commitFiles(t, w, fs, "second", map[string]string{"f.txt": "a\nb\n"})

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.NewBranchReferenceName("side")}))
	require.NoError(t, w.Merge(target, &MergeOptions{}))

	head, err := r.Head()
	require.NoError(t, err)
	require.Equal(t, target, head.Hash())
	commit, err := r.CommitObject(head.Hash())
	require.NoError(t, err)
	require.Equal(t, []plumbing.Hash{base}, commit.ParentHashes)
	require.Equal(t, "a\nb\n", readWorktree(t, fs, "f.txt"))
}

func TestMergeCleanThreeWayWithoutConfig(t *testing.T) {
	t.Parallel()
	r, w, fs := newMergeRepo(t)

	commitFiles(t, w, fs, "base", map[string]string{
		"both.txt": "a\nb\nc\n",
		"only.txt": "x\n",
		"dup.txt":  "a\na\na\n",
	})
	require.NoError(t, w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("ours"),
		Create: true,
	}))
	commitFiles(t, w, fs, "ours", map[string]string{
		"both.txt": "a\nOURS\nb\nc\n",
		"only.txt": "x\nours\n",
		"dup.txt":  "a\nX\na\na\n",
	})
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	target := commitFiles(t, w, fs, "theirs", map[string]string{
		"both.txt": "a\nb\nc\nTHEIRS\n",
		"dup.txt":  "a\na\nY\na\n",
	})

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.NewBranchReferenceName("ours")}))
	require.NoError(t, w.Merge(target, &MergeOptions{}))

	require.Equal(t, "a\nOURS\nb\nc\nTHEIRS\n", readWorktree(t, fs, "both.txt"))
	require.Equal(t, "x\nours\n", readWorktree(t, fs, "only.txt"))
	require.Equal(t, "a\nX\na\nY\na\n", readWorktree(t, fs, "dup.txt"))

	head, err := r.Head()
	require.NoError(t, err)
	commit, err := r.CommitObject(head.Hash())
	require.NoError(t, err)
	require.Len(t, commit.ParentHashes, 2)
	require.Equal(t, target, commit.ParentHashes[1])
	_, err = fs.Stat(".git/MERGE_HEAD")
	require.ErrorIs(t, err, os.ErrNotExist)

	both, err := commit.File("both.txt")
	require.NoError(t, err)
	bothContent, err := both.Contents()
	require.NoError(t, err)
	require.Equal(t, "a\nOURS\nb\nc\nTHEIRS\n", bothContent)
	dupFile, err := commit.File("dup.txt")
	require.NoError(t, err)
	dupContent, err := dupFile.Contents()
	require.NoError(t, err)
	require.Equal(t, "a\nX\na\nY\na\n", dupContent)
}

func TestMergeConflictsContentAndCleanNeighbor(t *testing.T) {
	t.Parallel()
	r, w, fs := newMergeRepo(t)

	commitFiles(t, w, fs, "base", map[string]string{
		"clash.txt": "base\n",
		"ok.txt":    "a\nb\n",
		"dup.txt":   "a\na\n",
	})
	require.NoError(t, w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("ours"),
		Create: true,
	}))
	commitFiles(t, w, fs, "ours", map[string]string{
		"clash.txt": "ours\n",
		"ok.txt":    "a\nOURS\nb\n",
		"dup.txt":   "a\nX\na\n",
	})
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	target := commitFiles(t, w, fs, "theirs", map[string]string{
		"clash.txt": "theirs\n",
		"ok.txt":    "a\nb\nTHEIRS\n",
		"dup.txt":   "a\nY\na\n",
	})
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.NewBranchReferenceName("ours")}))

	err := w.Merge(target, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)

	clash := readWorktree(t, fs, "clash.txt")
	require.Contains(t, clash, "<<<<<<< HEAD\n")
	require.Contains(t, clash, "ours\n")
	require.Contains(t, clash, "=======\n")
	require.Contains(t, clash, "theirs\n")
	require.Contains(t, clash, ">>>>>>> "+target.String()+"\n")
	require.Equal(t, "a\nOURS\nb\nTHEIRS\n", readWorktree(t, fs, "ok.txt"))

	dup := readWorktree(t, fs, "dup.txt")
	require.Contains(t, dup, "<<<<<<< HEAD\n")
	require.Contains(t, dup, "X\n")
	require.Contains(t, dup, "Y\n")

	require.Equal(t, target.String()+"\n", readWorktree(t, fs, ".git/MERGE_HEAD"))

	idx, err := r.Storer.Index()
	require.NoError(t, err)
	requireStages(t, idx, "clash.txt", map[index.Stage]bool{1: true, 2: true, 3: true})
	requireStages(t, idx, "dup.txt", map[index.Stage]bool{1: true, 2: true, 3: true})
	requireStages(t, idx, "ok.txt", map[index.Stage]bool{0: true})

	_, err = w.Add("clash.txt")
	require.NoError(t, err)
	_, err = w.Add("dup.txt")
	require.NoError(t, err)
	idx, err = r.Storer.Index()
	require.NoError(t, err)
	requireStages(t, idx, "clash.txt", map[index.Stage]bool{0: true})
	requireStages(t, idx, "dup.txt", map[index.Stage]bool{0: true})

	hash, err := w.Commit("resolve", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)
	commit, err := r.CommitObject(hash)
	require.NoError(t, err)
	require.Len(t, commit.ParentHashes, 2)
	require.Equal(t, target, commit.ParentHashes[1])
	_, err = fs.Stat(".git/MERGE_HEAD")
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestMergeDeleteModifyAndAddAdd(t *testing.T) {
	t.Parallel()
	r, w, fs := newMergeRepo(t)

	commitFiles(t, w, fs, "base", map[string]string{"f.txt": "line\n"})
	require.NoError(t, w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("ours"),
		Create: true,
	}))
	commitFiles(t, w, fs, "ours", map[string]string{"f.txt": "line\nmod\n", "new.txt": "aaa\n"})
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	require.NoError(t, util.RemoveAll(fs, "f.txt"))
	_, err := w.Add("f.txt")
	require.NoError(t, err)
	target := commitFiles(t, w, fs, "theirs", map[string]string{"new.txt": "bbb\n"})
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.NewBranchReferenceName("ours")}))

	err = w.Merge(target, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)

	require.Equal(t, "line\nmod\n", readWorktree(t, fs, "f.txt"))
	added := readWorktree(t, fs, "new.txt")
	require.Contains(t, added, "<<<<<<< HEAD\n")
	require.Contains(t, added, "aaa\n")
	require.Contains(t, added, "bbb\n")

	idx, err := r.Storer.Index()
	require.NoError(t, err)
	requireStages(t, idx, "f.txt", map[index.Stage]bool{1: true, 2: true})
	requireStages(t, idx, "new.txt", map[index.Stage]bool{2: true, 3: true})
}

func TestMergeFileDirectoryClash(t *testing.T) {
	t.Parallel()
	r, w, fs := newMergeRepo(t)

	commitFiles(t, w, fs, "base", map[string]string{"keep.txt": "a\nb\nc\n"})
	require.NoError(t, w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("ours"),
		Create: true,
	}))
	commitFiles(t, w, fs, "ours", map[string]string{"sub": "file-was-dir\n", "keep.txt": "a\nOURS\nb\nc\n"})
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	require.NoError(t, fs.MkdirAll("sub", 0o755))
	target := commitFiles(t, w, fs, "theirs", map[string]string{
		"sub/file.txt": "child\n",
		"keep.txt":     "a\nb\nc\nTHEIRS\n",
	})
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.NewBranchReferenceName("ours")}))

	err := w.Merge(target, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)
	require.Equal(t, target.String()+"\n", readWorktree(t, fs, ".git/MERGE_HEAD"))
	require.Equal(t, "a\nOURS\nb\nc\nTHEIRS\n", readWorktree(t, fs, "keep.txt"))

	idx, err := r.Storer.Index()
	require.NoError(t, err)
	requireStages(t, idx, "sub", map[index.Stage]bool{2: true})
}

func TestMergeDirectoryVersusFile(t *testing.T) {
	t.Parallel()
	r, w, fs := newMergeRepo(t)

	commitFiles(t, w, fs, "base", map[string]string{"stay.txt": "stay\n"})
	require.NoError(t, w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("ours"),
		Create: true,
	}))
	require.NoError(t, fs.MkdirAll("sub", 0o755))
	commitFiles(t, w, fs, "ours", map[string]string{"sub/a.txt": "child\n"})
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	target := commitFiles(t, w, fs, "theirs", map[string]string{"sub": "i-am-a-file\n"})
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.NewBranchReferenceName("ours")}))

	err := w.Merge(target, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)
	idx, err := r.Storer.Index()
	require.NoError(t, err)
	requireStages(t, idx, "sub", map[index.Stage]bool{3: true})
	require.Equal(t, "child\n", readWorktree(t, fs, "sub/a.txt"))
}

func TestMergeDeletedFileBecomesDirectory(t *testing.T) {
	t.Parallel()
	r, w, fs := newMergeRepo(t)

	commitFiles(t, w, fs, "base", map[string]string{"path": "base\n", "stay.txt": "stay\n"})
	require.NoError(t, w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("ours"),
		Create: true,
	}))
	require.NoError(t, util.RemoveAll(fs, "path"))
	_, err := w.Add("path")
	require.NoError(t, err)
	_, err = w.Commit("delete", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	require.NoError(t, util.RemoveAll(fs, "path"))
	_, err = w.Add("path")
	require.NoError(t, err)
	require.NoError(t, fs.MkdirAll("path", 0o755))
	target := commitFiles(t, w, fs, "theirs", map[string]string{"path/child": "x\n"})
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.NewBranchReferenceName("ours")}))

	require.NoError(t, w.Merge(target, &MergeOptions{}))
	require.Equal(t, "x\n", readWorktree(t, fs, "path/child"))
	head, err := r.Head()
	require.NoError(t, err)
	require.NotEqual(t, target, head.Hash())
}

func TestMergeUncommittedChanges(t *testing.T) {
	t.Parallel()
	_, w, fs := newMergeRepo(t)

	commitFiles(t, w, fs, "base", map[string]string{"f.txt": "a\n"})
	require.NoError(t, w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("side"),
		Create: true,
	}))
	target := commitFiles(t, w, fs, "side", map[string]string{"f.txt": "b\n"})
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	require.NoError(t, util.WriteFile(fs, "f.txt", []byte("dirty\n"), 0o644))

	err := w.Merge(target, &MergeOptions{})
	require.ErrorIs(t, err, ErrUncommittedChanges)
}

func TestMergeAlreadyUpToDate(t *testing.T) {
	t.Parallel()
	r, w, fs := newMergeRepo(t)
	head := commitFiles(t, w, fs, "base", map[string]string{"f.txt": "a\n"})
	require.NoError(t, w.Merge(head, &MergeOptions{}))
	got, err := r.Head()
	require.NoError(t, err)
	require.Equal(t, head, got.Hash())
}

func newMergeRepo(t *testing.T) (*Repository, *Worktree, billy.Filesystem) {
	t.Helper()
	fs := memfs.New()
	r, err := Init(memory.NewStorage(), WithWorkTree(fs))
	require.NoError(t, err)
	w, err := r.Worktree()
	require.NoError(t, err)
	return r, w, fs
}

func commitFiles(t *testing.T, w *Worktree, fs billy.Filesystem, msg string, files map[string]string) plumbing.Hash {
	t.Helper()
	for name, content := range files {
		require.NoError(t, util.WriteFile(fs, name, []byte(content), 0o644))
		_, err := w.Add(name)
		require.NoError(t, err)
	}
	hash, err := w.Commit(msg, &CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@example.com", When: time.Unix(1_700_000_000, 0)},
	})
	require.NoError(t, err)
	return hash
}

func readWorktree(t *testing.T, fs billy.Filesystem, name string) string {
	t.Helper()
	f, err := fs.Open(name)
	require.NoError(t, err)
	defer f.Close()
	b, err := io.ReadAll(f)
	require.NoError(t, err)
	return string(b)
}

func requireStages(t *testing.T, idx *index.Index, name string, want map[index.Stage]bool) {
	t.Helper()
	got := map[index.Stage]bool{}
	for _, e := range idx.Entries {
		if e.Name == name {
			got[e.Stage] = true
		}
	}
	require.Equal(t, want, got)
}

func TestMergeDeleteModifyOtherSide(t *testing.T) {
	t.Parallel()
	// Ours deletes, theirs modifies: stage 1 and stage 3, no stage 2.
	r, w, fs := newMergeRepo(t)
	commitFiles(t, w, fs, "base", map[string]string{"f.txt": "line\n", "stay.txt": "stay\n"})
	require.NoError(t, w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("ours"),
		Create: true,
	}))
	require.NoError(t, util.RemoveAll(fs, "f.txt"))
	_, err := w.Add("f.txt")
	require.NoError(t, err)
	_, err = w.Commit("ours", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	target := commitFiles(t, w, fs, "theirs", map[string]string{"f.txt": "line\nmod\n"})
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: plumbing.NewBranchReferenceName("ours")}))

	err = w.Merge(target, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)
	require.Equal(t, "line\nmod\n", readWorktree(t, fs, "f.txt"))
	idx, err := r.Storer.Index()
	require.NoError(t, err)
	requireStages(t, idx, "f.txt", map[index.Stage]bool{1: true, 3: true})
	require.False(t, errors.Is(err, ErrUncommittedChanges))
}
