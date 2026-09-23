package git

import (
	"os"
	"testing"
	"time"

	"github.com/go-git/go-billy/v6/memfs"
	"github.com/go-git/go-billy/v6/util"
	"github.com/stretchr/testify/require"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/format/index"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/storage/memory"
)

func TestMergeFastForward(t *testing.T) {
	t.Parallel()
	r, wt := newMergeRepo(t)
	base := commitFiles(t, wt, map[string]string{"a.txt": "base\n"}, nil, "base")
	require.NoError(t, wt.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("feature"),
		Create: true,
	}))
	feature := commitFiles(t, wt, map[string]string{"b.txt": "feature\n"}, nil, "feature")
	require.NoError(t, wt.Checkout(&CheckoutOptions{Branch: plumbing.Master}))

	require.NoError(t, wt.Merge(feature, &MergeOptions{}))

	head, err := r.Head()
	require.NoError(t, err)
	require.Equal(t, feature, head.Hash())
	commit, err := r.CommitObject(head.Hash())
	require.NoError(t, err)
	require.Equal(t, []plumbing.Hash{base}, commit.ParentHashes)
	require.Equal(t, "feature\n", readWorktreeFile(t, wt, "b.txt"))
	_, err = wt.Filesystem.Stat(".git/MERGE_HEAD")
	require.ErrorIs(t, err, os.ErrNotExist)

	status, err := wt.Status()
	require.NoError(t, err)
	require.True(t, status.IsClean())
}

func TestMergeFastForwardNilOptions(t *testing.T) {
	t.Parallel()
	_, wt := newMergeRepo(t)
	commitFiles(t, wt, map[string]string{"a.txt": "base\n"}, nil, "base")
	require.NoError(t, wt.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("feature"),
		Create: true,
	}))
	feature := commitFiles(t, wt, map[string]string{"a.txt": "next\n"}, nil, "feature")
	require.NoError(t, wt.Checkout(&CheckoutOptions{Branch: plumbing.Master}))

	require.NoError(t, wt.Merge(feature, nil))
	require.Equal(t, "next\n", readWorktreeFile(t, wt, "a.txt"))
}

func TestMergeThreeWayWithoutUserConfig(t *testing.T) {
	t.Parallel()
	r, wt := newMergeRepo(t)
	base := commitFiles(t, wt, map[string]string{"f.txt": "1\n2\n3\n"}, nil, "base")
	require.NoError(t, wt.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("ours"),
		Create: true,
	}))
	ours := commitFiles(t, wt, map[string]string{"f.txt": "1\nO\n3\n", "ours.txt": "ours\n"}, nil, "ours")
	require.NoError(t, wt.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	require.NoError(t, wt.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("theirs"),
		Create: true,
		Hash:   base,
	}))
	theirs := commitFiles(t, wt, map[string]string{"f.txt": "1\n2\n3\nT\n", "theirs.txt": "theirs\n"}, nil, "theirs")
	require.NoError(t, wt.Checkout(&CheckoutOptions{Branch: plumbing.NewBranchReferenceName("ours")}))

	require.NoError(t, wt.Merge(theirs, &MergeOptions{}))

	head, err := r.Head()
	require.NoError(t, err)
	commit, err := r.CommitObject(head.Hash())
	require.NoError(t, err)
	require.Equal(t, []plumbing.Hash{ours, theirs}, commit.ParentHashes)
	require.Equal(t, "Merge commit '"+theirs.String()+"' into ours\n", commit.Message)
	require.NotEmpty(t, commit.Author.Name)
	require.NotEmpty(t, commit.Author.Email)
	require.Equal(t, "1\nO\n3\nT\n", readWorktreeFile(t, wt, "f.txt"))
	require.Equal(t, "ours\n", readWorktreeFile(t, wt, "ours.txt"))
	require.Equal(t, "theirs\n", readWorktreeFile(t, wt, "theirs.txt"))
	_, err = wt.Filesystem.Stat(".git/MERGE_HEAD")
	require.ErrorIs(t, err, os.ErrNotExist)

	status, err := wt.Status()
	require.NoError(t, err)
	require.True(t, status.IsClean())
}

func TestMergeContentConflictKeepsOtherFiles(t *testing.T) {
	t.Parallel()
	r, wt := newMergeRepo(t)
	base := commitFiles(t, wt, map[string]string{
		"f.txt":     "a\na\na\n",
		"other.txt": "base\n",
	}, nil, "base")
	require.NoError(t, wt.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("ours"),
		Create: true,
	}))
	ours := commitFiles(t, wt, map[string]string{"f.txt": "a\nb\na\n"}, nil, "ours")
	require.NoError(t, wt.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	require.NoError(t, wt.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("theirs"),
		Create: true,
		Hash:   base,
	}))
	theirs := commitFiles(t, wt, map[string]string{
		"f.txt":     "a\nc\na\n",
		"other.txt": "theirs\n",
	}, nil, "theirs")
	require.NoError(t, wt.Checkout(&CheckoutOptions{Branch: plumbing.NewBranchReferenceName("ours")}))

	err := wt.Merge(theirs, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)

	head, err := r.Head()
	require.NoError(t, err)
	require.Equal(t, ours, head.Hash())
	require.Equal(t, theirs.String()+"\n", readWorktreeFile(t, wt, ".git/MERGE_HEAD"))
	require.Equal(t, "a\n<<<<<<< HEAD\nb\n=======\nc\n>>>>>>> "+theirs.String()+"\na\n", readWorktreeFile(t, wt, "f.txt"))
	require.Equal(t, "theirs\n", readWorktreeFile(t, wt, "other.txt"))

	fStages := indexStages(t, r, "f.txt")
	require.Len(t, fStages, 3)
	baseFile := blobFromCommit(t, r, base, "f.txt")
	oursFile := blobFromCommit(t, r, ours, "f.txt")
	theirsFile := blobFromCommit(t, r, theirs, "f.txt")
	require.Equal(t, baseFile, fStages[index.AncestorMode])
	require.Equal(t, oursFile, fStages[index.OurMode])
	require.Equal(t, theirsFile, fStages[index.TheirMode])
	_, hasStage0 := fStages[0]
	require.False(t, hasStage0)

	other := indexStages(t, r, "other.txt")
	require.Equal(t, map[index.Stage]plumbing.Hash{0: blobFromCommit(t, r, theirs, "other.txt")}, other)

	require.NoError(t, util.WriteFile(wt.Filesystem, "f.txt", []byte("a\nresolved\na\n"), 0o644))
	_, err = wt.Add("f.txt")
	require.NoError(t, err)
	resolved := indexStages(t, r, "f.txt")
	require.Len(t, resolved, 1)
	_, ok := resolved[0]
	require.True(t, ok)

	hash, err := wt.Commit("resolve", &CommitOptions{Author: testSignature()})
	require.NoError(t, err)
	commit, err := r.CommitObject(hash)
	require.NoError(t, err)
	require.Equal(t, []plumbing.Hash{ours, theirs}, commit.ParentHashes)
	_, err = wt.Filesystem.Stat(".git/MERGE_HEAD")
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestMergeDeleteModify(t *testing.T) {
	t.Parallel()
	r, wt := newMergeRepo(t)
	base := commitFiles(t, wt, map[string]string{
		"del.txt": "base\n",
		"mod.txt": "base\n",
	}, nil, "base")
	require.NoError(t, wt.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("ours"),
		Create: true,
	}))
	ours := commitFiles(t, wt, map[string]string{"mod.txt": "oursmod\n"}, []string{"del.txt"}, "ours")
	require.NoError(t, wt.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	require.NoError(t, wt.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("theirs"),
		Create: true,
		Hash:   base,
	}))
	theirs := commitFiles(t, wt, map[string]string{"del.txt": "theirsmod\n"}, []string{"mod.txt"}, "theirs")
	require.NoError(t, wt.Checkout(&CheckoutOptions{Branch: plumbing.NewBranchReferenceName("ours")}))

	err := wt.Merge(theirs, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)

	del := indexStages(t, r, "del.txt")
	require.Equal(t, blobFromCommit(t, r, base, "del.txt"), del[index.AncestorMode])
	_, hasOurs := del[index.OurMode]
	require.False(t, hasOurs)
	require.Equal(t, blobFromCommit(t, r, theirs, "del.txt"), del[index.TheirMode])
	require.Equal(t, "theirsmod\n", readWorktreeFile(t, wt, "del.txt"))

	mod := indexStages(t, r, "mod.txt")
	require.Equal(t, blobFromCommit(t, r, base, "mod.txt"), mod[index.AncestorMode])
	require.Equal(t, blobFromCommit(t, r, ours, "mod.txt"), mod[index.OurMode])
	_, hasTheirs := mod[index.TheirMode]
	require.False(t, hasTheirs)
	require.Equal(t, "oursmod\n", readWorktreeFile(t, wt, "mod.txt"))
	require.Equal(t, theirs.String()+"\n", readWorktreeFile(t, wt, ".git/MERGE_HEAD"))
}

func TestMergeAddAdd(t *testing.T) {
	t.Parallel()
	r, wt := newMergeRepo(t)
	base := commitFiles(t, wt, map[string]string{"b.txt": "base\n"}, nil, "base")
	require.NoError(t, wt.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("ours"),
		Create: true,
	}))
	commitFiles(t, wt, map[string]string{"n.txt": "start\nours\n"}, nil, "ours")
	require.NoError(t, wt.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	require.NoError(t, wt.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("theirs"),
		Create: true,
		Hash:   base,
	}))
	theirs := commitFiles(t, wt, map[string]string{"n.txt": "start\ntheirs\n"}, nil, "theirs")
	require.NoError(t, wt.Checkout(&CheckoutOptions{Branch: plumbing.NewBranchReferenceName("ours")}))

	err := wt.Merge(theirs, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)
	require.Equal(t, "start\n<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>> "+theirs.String()+"\n", readWorktreeFile(t, wt, "n.txt"))

	stages := indexStages(t, r, "n.txt")
	_, hasBase := stages[index.AncestorMode]
	require.False(t, hasBase)
	require.Contains(t, stages, index.OurMode)
	require.Contains(t, stages, index.TheirMode)
}

func TestMergeAddAddIdentical(t *testing.T) {
	t.Parallel()
	_, wt := newMergeRepo(t)
	base := commitFiles(t, wt, map[string]string{"b.txt": "base\n"}, nil, "base")
	require.NoError(t, wt.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("ours"),
		Create: true,
	}))
	commitFiles(t, wt, map[string]string{"n.txt": "same\n"}, nil, "ours")
	require.NoError(t, wt.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	require.NoError(t, wt.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("theirs"),
		Create: true,
		Hash:   base,
	}))
	theirs := commitFiles(t, wt, map[string]string{"n.txt": "same\n"}, nil, "theirs")
	require.NoError(t, wt.Checkout(&CheckoutOptions{Branch: plumbing.NewBranchReferenceName("ours")}))

	require.NoError(t, wt.Merge(theirs, &MergeOptions{}))
	require.Equal(t, "same\n", readWorktreeFile(t, wt, "n.txt"))
}

func TestMergeFileDirectory(t *testing.T) {
	t.Parallel()
	r, wt := newMergeRepo(t)
	base := commitFiles(t, wt, map[string]string{"other.txt": "base\n"}, nil, "base")
	require.NoError(t, wt.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("ours"),
		Create: true,
	}))
	commitFiles(t, wt, map[string]string{"foo": "file-content\n"}, nil, "ours")
	require.NoError(t, wt.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	require.NoError(t, wt.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("theirs"),
		Create: true,
		Hash:   base,
	}))
	theirs := commitFiles(t, wt, map[string]string{"foo/bar.txt": "dir-content\n"}, nil, "theirs")
	require.NoError(t, wt.Checkout(&CheckoutOptions{Branch: plumbing.NewBranchReferenceName("ours")}))

	err := wt.Merge(theirs, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)
	require.Equal(t, theirs.String()+"\n", readWorktreeFile(t, wt, ".git/MERGE_HEAD"))
	require.Equal(t, "dir-content\n", readWorktreeFile(t, wt, "foo/bar.txt"))
	require.Equal(t, "file-content\n", readWorktreeFile(t, wt, "foo~HEAD"))

	stages := indexStages(t, r, "foo~HEAD")
	require.Contains(t, stages, index.OurMode)
	_, hasTheirs := stages[index.TheirMode]
	require.False(t, hasTheirs)
	bar := indexStages(t, r, "foo/bar.txt")
	require.Equal(t, map[index.Stage]plumbing.Hash{0: blobFromCommit(t, r, theirs, "foo/bar.txt")}, bar)
}

func TestMergeUncommittedChanges(t *testing.T) {
	t.Parallel()
	r, wt := newMergeRepo(t)
	base := commitFiles(t, wt, map[string]string{"a.txt": "base\n"}, nil, "base")
	require.NoError(t, wt.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("feature"),
		Create: true,
	}))
	feature := commitFiles(t, wt, map[string]string{"a.txt": "feature\n"}, nil, "feature")
	require.NoError(t, wt.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	require.NoError(t, util.WriteFile(wt.Filesystem, "a.txt", []byte("dirty\n"), 0o644))

	err := wt.Merge(feature, &MergeOptions{})
	require.ErrorIs(t, err, ErrUncommittedChanges)
	head, err := r.Head()
	require.NoError(t, err)
	require.Equal(t, base, head.Hash())
	require.Equal(t, "dirty\n", readWorktreeFile(t, wt, "a.txt"))
}

func TestAddClearsConflictStages(t *testing.T) {
	t.Parallel()
	r, wt := newMergeRepo(t)
	commitFiles(t, wt, map[string]string{"f.txt": "same\n"}, nil, "base")
	hash, err := wt.Add("f.txt")
	require.NoError(t, err)

	idx, err := r.Storer.Index()
	require.NoError(t, err)
	idx.Entries = []*index.Entry{
		{Name: "f.txt", Hash: hash, Mode: filemode.Regular, Stage: index.AncestorMode},
		{Name: "f.txt", Hash: hash, Mode: filemode.Regular, Stage: index.OurMode},
		{Name: "f.txt", Hash: hash, Mode: filemode.Regular, Stage: index.TheirMode},
	}
	require.NoError(t, r.Storer.SetIndex(idx))

	_, err = wt.Add("f.txt")
	require.NoError(t, err)
	stages := indexStages(t, r, "f.txt")
	require.Equal(t, map[index.Stage]plumbing.Hash{0: hash}, stages)
}

func TestCommitMergeSameTreeAsHead(t *testing.T) {
	t.Parallel()
	r, wt := newMergeRepo(t)
	base := commitFiles(t, wt, map[string]string{"f.txt": "base\n"}, nil, "base")
	require.NoError(t, wt.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("ours"),
		Create: true,
	}))
	ours := commitFiles(t, wt, map[string]string{"f.txt": "ours\n"}, nil, "ours")
	require.NoError(t, wt.Checkout(&CheckoutOptions{Branch: plumbing.Master}))
	require.NoError(t, wt.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("theirs"),
		Create: true,
		Hash:   base,
	}))
	theirs := commitFiles(t, wt, map[string]string{"f.txt": "theirs\n"}, nil, "theirs")
	require.NoError(t, wt.Checkout(&CheckoutOptions{Branch: plumbing.NewBranchReferenceName("ours")}))

	err := wt.Merge(theirs, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)
	require.NoError(t, util.WriteFile(wt.Filesystem, "f.txt", []byte("ours\n"), 0o644))
	_, err = wt.Add("f.txt")
	require.NoError(t, err)

	hash, err := wt.Commit("keep ours", &CommitOptions{Author: testSignature()})
	require.NoError(t, err)
	commit, err := r.CommitObject(hash)
	require.NoError(t, err)
	require.Equal(t, []plumbing.Hash{ours, theirs}, commit.ParentHashes)
	require.Equal(t, oursTree(t, r, ours), commit.TreeHash)
}

func oursTree(t *testing.T, r *Repository, hash plumbing.Hash) plumbing.Hash {
	t.Helper()
	c, err := r.CommitObject(hash)
	require.NoError(t, err)
	return c.TreeHash
}

func TestCommitAppendsMergeHead(t *testing.T) {
	t.Parallel()
	r, wt := newMergeRepo(t)
	first := commitFiles(t, wt, map[string]string{"a.txt": "a\n"}, nil, "first")
	second := commitFiles(t, wt, map[string]string{"a.txt": "b\n"}, nil, "second")
	require.NoError(t, wt.Filesystem.MkdirAll(".git", 0o755))
	require.NoError(t, util.WriteFile(wt.Filesystem, ".git/MERGE_HEAD", []byte(first.String()+"\n"), 0o644))
	require.NoError(t, util.WriteFile(wt.Filesystem, "a.txt", []byte("c\n"), 0o644))
	_, err := wt.Add("a.txt")
	require.NoError(t, err)

	hash, err := wt.Commit("finish merge", &CommitOptions{Author: testSignature()})
	require.NoError(t, err)
	commit, err := r.CommitObject(hash)
	require.NoError(t, err)
	require.Equal(t, []plumbing.Hash{second, first}, commit.ParentHashes)
	_, err = wt.Filesystem.Stat(".git/MERGE_HEAD")
	require.ErrorIs(t, err, os.ErrNotExist)
}

func newMergeRepo(t *testing.T) (*Repository, *Worktree) {
	t.Helper()
	r, err := Init(memory.NewStorage(), WithWorkTree(memfs.New()))
	require.NoError(t, err)
	wt, err := r.Worktree()
	require.NoError(t, err)
	return r, wt
}

func testSignature() *object.Signature {
	return &object.Signature{Name: "tester", Email: "tester@example.com", When: time.Unix(0, 0)}
}

func commitFiles(t *testing.T, wt *Worktree, files map[string]string, remove []string, msg string) plumbing.Hash {
	t.Helper()
	for name, content := range files {
		err := util.WriteFile(wt.Filesystem, name, []byte(content), 0o644)
		require.NoError(t, err, "write %s", name)
		_, err = wt.Add(name)
		require.NoError(t, err, "add %s", name)
	}
	for _, name := range remove {
		_, err := wt.Remove(name)
		require.NoError(t, err, "remove %s", name)
	}
	hash, err := wt.Commit(msg, &CommitOptions{Author: testSignature()})
	require.NoError(t, err, "commit %s", msg)
	return hash
}

func readWorktreeFile(t *testing.T, wt *Worktree, name string) string {
	t.Helper()
	b, err := util.ReadFile(wt.Filesystem, name)
	require.NoError(t, err)
	return string(b)
}

func indexStages(t *testing.T, r *Repository, name string) map[index.Stage]plumbing.Hash {
	t.Helper()
	idx, err := r.Storer.Index()
	require.NoError(t, err)
	out := make(map[index.Stage]plumbing.Hash)
	for _, e := range idx.Entries {
		if e.Name == name {
			out[e.Stage] = e.Hash
		}
	}
	return out
}

func blobFromCommit(t *testing.T, r *Repository, commit plumbing.Hash, name string) plumbing.Hash {
	t.Helper()
	c, err := r.CommitObject(commit)
	require.NoError(t, err)
	f, err := c.File(name)
	require.NoError(t, err)
	return f.Hash
}
