package git

import (
	"fmt"
	"os"
	"testing"

	"github.com/go-git/go-billy/v6"
	"github.com/go-git/go-billy/v6/memfs"
	"github.com/go-git/go-billy/v6/osfs"
	"github.com/go-git/go-billy/v6/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/go-git/go-git/v6/plumbing"
	format "github.com/go-git/go-git/v6/plumbing/format/config"
	"github.com/go-git/go-git/v6/plumbing/format/index"
	"github.com/go-git/go-git/v6/storage/memory"
)

type mergeTestRepo struct {
	t  *testing.T
	r  *Repository
	w  *Worktree
	fs billy.Filesystem
}

func newMergeTestRepo(t *testing.T) *mergeTestRepo {
	t.Helper()

	fs := memfs.New()
	r, err := Init(memory.NewStorage(), WithWorkTree(fs))
	require.NoError(t, err)

	return newMergeTestRepoFrom(t, r)
}

func newMergeTestRepoFrom(t *testing.T, r *Repository) *mergeTestRepo {
	t.Helper()

	w, err := r.Worktree()
	require.NoError(t, err)

	return &mergeTestRepo{t: t, r: r, w: w, fs: w.Filesystem}
}

type mergeTestChange struct {
	write  map[string]string
	remove []string
}

// commit applies change to the worktree and commits it.
func (m *mergeTestRepo) commit(msg string, change mergeTestChange) plumbing.Hash {
	m.t.Helper()

	for _, name := range change.remove {
		_, err := m.w.Remove(name)
		require.NoError(m.t, err)
	}

	for name, content := range change.write {
		require.NoError(m.t, util.WriteFile(m.fs, name, []byte(content), 0o644))
		_, err := m.w.Add(name)
		require.NoError(m.t, err)
	}

	h, err := m.w.Commit(msg, &CommitOptions{Author: defaultSignature()})
	require.NoError(m.t, err)

	return h
}

// diverge commits base, then commits theirs on a new branch and ours on top
// of base in the current branch, which is left checked out.
func (m *mergeTestRepo) diverge(base map[string]string, ours, theirs mergeTestChange) (oursHash, theirsHash plumbing.Hash) {
	m.t.Helper()

	m.commit("base", mergeTestChange{write: base})

	head, err := m.r.Head()
	require.NoError(m.t, err)

	require.NoError(m.t, m.w.Checkout(&CheckoutOptions{Branch: "refs/heads/theirs", Create: true}))
	theirsHash = m.commit("theirs", theirs)

	require.NoError(m.t, m.w.Checkout(&CheckoutOptions{Branch: head.Name()}))
	oursHash = m.commit("ours", ours)

	return oursHash, theirsHash
}

func (m *mergeTestRepo) head() plumbing.Hash {
	m.t.Helper()

	head, err := m.r.Head()
	require.NoError(m.t, err)

	return head.Hash()
}

func (m *mergeTestRepo) read(name string) string {
	m.t.Helper()

	data, err := util.ReadFile(m.fs, name)
	require.NoError(m.t, err)

	return string(data)
}

func (m *mergeTestRepo) blob(content string) plumbing.Hash {
	h := plumbing.NewHasher(format.SHA1, plumbing.BlobObject, int64(len(content)))
	_, err := h.Write([]byte(content))
	require.NoError(m.t, err)

	return h.Sum()
}

// stages returns the hash of each index entry of name, keyed by stage.
func (m *mergeTestRepo) stages(name string) map[index.Stage]plumbing.Hash {
	m.t.Helper()

	idx, err := m.r.Storer.Index()
	require.NoError(m.t, err)

	stages := make(map[index.Stage]plumbing.Hash)
	for _, e := range idx.Entries {
		if e.Name == name {
			stages[e.Stage] = e.Hash
		}
	}

	return stages
}

func (m *mergeTestRepo) assertClean() {
	m.t.Helper()

	status, err := m.w.Status()
	require.NoError(m.t, err)
	assert.True(m.t, status.IsClean(), status.String())

	_, err = m.fs.Stat(mergeHeadPath)
	assert.ErrorIs(m.t, err, os.ErrNotExist)
}

func (m *mergeTestRepo) assertMergeHead(h plumbing.Hash) {
	m.t.Helper()

	assert.Equal(m.t, h.String()+"\n", m.read(".git/MERGE_HEAD"))
}

func TestWorktreeMergeFastForward(t *testing.T) {
	t.Parallel()
	m := newMergeTestRepo(t)

	base := m.commit("base", mergeTestChange{write: map[string]string{"a": "a\n"}})
	head, err := m.r.Head()
	require.NoError(t, err)

	require.NoError(t, m.w.Checkout(&CheckoutOptions{Branch: "refs/heads/feature", Create: true}))
	feature := m.commit("feature", mergeTestChange{write: map[string]string{"a": "a2\n", "b": "b\n"}})
	require.NoError(t, m.w.Checkout(&CheckoutOptions{Branch: head.Name()}))
	require.Equal(t, base, m.head())

	require.NoError(t, m.w.Merge(feature, &MergeOptions{}))

	assert.Equal(t, feature, m.head())
	assert.Equal(t, "a2\n", m.read("a"))
	assert.Equal(t, "b\n", m.read("b"))
	m.assertClean()
}

func TestWorktreeMergeAlreadyUpToDate(t *testing.T) {
	t.Parallel()
	m := newMergeTestRepo(t)

	base := m.commit("base", mergeTestChange{write: map[string]string{"a": "a\n"}})
	head := m.commit("second", mergeTestChange{write: map[string]string{"a": "a2\n"}})

	require.NoError(t, m.w.Merge(base, &MergeOptions{}))
	require.NoError(t, m.w.Merge(head, nil))

	assert.Equal(t, head, m.head())
	assert.Equal(t, "a2\n", m.read("a"))
	m.assertClean()
}

func TestWorktreeMergeUnsupportedStrategy(t *testing.T) {
	t.Parallel()
	m := newMergeTestRepo(t)

	base := m.commit("base", mergeTestChange{write: map[string]string{"a": "a\n"}})

	err := m.w.Merge(base, &MergeOptions{Strategy: MergeStrategy(10)})
	assert.ErrorIs(t, err, ErrUnsupportedMergeStrategy)
}

func TestWorktreeMergeCreatesMergeCommit(t *testing.T) {
	t.Parallel()
	m := newMergeTestRepo(t)

	ours, theirs := m.diverge(
		map[string]string{"a": "a\n", "b": "b\n", "dir/c": "c\n"},
		mergeTestChange{write: map[string]string{"a": "ours\n"}},
		mergeTestChange{
			write:  map[string]string{"b": "theirs\n", "dir/new": "new\n"},
			remove: []string{"dir/c"},
		},
	)

	require.NoError(t, m.w.Merge(theirs, &MergeOptions{}))

	commit, err := m.r.CommitObject(m.head())
	require.NoError(t, err)
	assert.Equal(t, []plumbing.Hash{ours, theirs}, commit.ParentHashes)
	assert.Equal(t, fmt.Sprintf("Merge commit '%s'\n", theirs), commit.Message)

	assert.Equal(t, "ours\n", m.read("a"))
	assert.Equal(t, "theirs\n", m.read("b"))
	assert.Equal(t, "new\n", m.read("dir/new"))
	_, err = m.fs.Stat("dir/c")
	assert.ErrorIs(t, err, os.ErrNotExist)

	tree, err := commit.Tree()
	require.NoError(t, err)
	for name, content := range map[string]string{"a": "ours\n", "b": "theirs\n", "dir/new": "new\n"} {
		f, err := tree.File(name)
		require.NoError(t, err)
		got, err := f.Contents()
		require.NoError(t, err)
		assert.Equal(t, content, got, name)
	}

	m.assertClean()
}

func TestWorktreeMergeSameFileNonOverlapping(t *testing.T) {
	t.Parallel()
	m := newMergeTestRepo(t)

	ours, theirs := m.diverge(
		map[string]string{"f": "1\n2\n3\n4\n5\n"},
		mergeTestChange{write: map[string]string{"f": "one\n2\n3\n4\n5\n"}},
		mergeTestChange{write: map[string]string{"f": "1\n2\n3\n4\nfive\nsix\n"}},
	)

	require.NoError(t, m.w.Merge(theirs, &MergeOptions{}))

	commit, err := m.r.CommitObject(m.head())
	require.NoError(t, err)
	assert.Equal(t, []plumbing.Hash{ours, theirs}, commit.ParentHashes)
	assert.Equal(t, "one\n2\n3\n4\nfive\nsix\n", m.read("f"))
	m.assertClean()
}

func TestWorktreeMergeContentConflict(t *testing.T) {
	t.Parallel()
	m := newMergeTestRepo(t)

	ours, theirs := m.diverge(
		map[string]string{"f": "a\nb\nc\n", "g": "g\n"},
		mergeTestChange{write: map[string]string{"f": "a\nours\nc\n"}},
		mergeTestChange{write: map[string]string{"f": "a\ntheirs\nc\n", "g": "g2\n", "h": "h\n"}},
	)

	err := m.w.Merge(theirs, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)

	assert.Equal(t, ours, m.head())
	assert.Equal(t,
		"a\n<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>> "+theirs.String()+"\nc\n",
		m.read("f"))
	assert.Equal(t, map[index.Stage]plumbing.Hash{
		index.AncestorMode: m.blob("a\nb\nc\n"),
		index.OurMode:      m.blob("a\nours\nc\n"),
		index.TheirMode:    m.blob("a\ntheirs\nc\n"),
	}, m.stages("f"))

	assert.Equal(t, "g2\n", m.read("g"))
	assert.Equal(t, map[index.Stage]plumbing.Hash{0: m.blob("g2\n")}, m.stages("g"))
	assert.Equal(t, "h\n", m.read("h"))
	assert.Equal(t, map[index.Stage]plumbing.Hash{0: m.blob("h\n")}, m.stages("h"))

	m.assertMergeHead(theirs)
}

func TestWorktreeMergeConflictRepeatedLines(t *testing.T) {
	t.Parallel()
	m := newMergeTestRepo(t)

	_, theirs := m.diverge(
		map[string]string{"f": "x\nx\nx\nx\n"},
		mergeTestChange{write: map[string]string{"f": "x\nours\nx\nx\n"}},
		mergeTestChange{write: map[string]string{"f": "x\ntheirs\nx\nx\n"}},
	)

	err := m.w.Merge(theirs, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)

	assert.Equal(t,
		"x\n<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>> "+theirs.String()+"\nx\nx\n",
		m.read("f"))
	assert.Len(t, m.stages("f"), 3)
}

func TestWorktreeMergeDeleteModifyConflict(t *testing.T) {
	t.Parallel()

	t.Run("deleted by theirs", func(t *testing.T) {
		t.Parallel()
		m := newMergeTestRepo(t)

		_, theirs := m.diverge(
			map[string]string{"f": "f\n", "other": "o\n"},
			mergeTestChange{write: map[string]string{"f": "ours\n"}},
			mergeTestChange{remove: []string{"f"}},
		)

		require.ErrorIs(t, m.w.Merge(theirs, &MergeOptions{}), ErrMergeConflicts)

		assert.Equal(t, "ours\n", m.read("f"))
		assert.Equal(t, map[index.Stage]plumbing.Hash{
			index.AncestorMode: m.blob("f\n"),
			index.OurMode:      m.blob("ours\n"),
		}, m.stages("f"))
		m.assertMergeHead(theirs)
	})

	t.Run("deleted by ours", func(t *testing.T) {
		t.Parallel()
		m := newMergeTestRepo(t)

		_, theirs := m.diverge(
			map[string]string{"f": "f\n", "other": "o\n"},
			mergeTestChange{remove: []string{"f"}},
			mergeTestChange{write: map[string]string{"f": "theirs\n"}},
		)

		require.ErrorIs(t, m.w.Merge(theirs, &MergeOptions{}), ErrMergeConflicts)

		assert.Equal(t, "theirs\n", m.read("f"))
		assert.Equal(t, map[index.Stage]plumbing.Hash{
			index.AncestorMode: m.blob("f\n"),
			index.TheirMode:    m.blob("theirs\n"),
		}, m.stages("f"))
		m.assertMergeHead(theirs)
	})
}

func TestWorktreeMergeAddAdd(t *testing.T) {
	t.Parallel()

	t.Run("different content conflicts", func(t *testing.T) {
		t.Parallel()
		m := newMergeTestRepo(t)

		_, theirs := m.diverge(
			map[string]string{"a": "a\n"},
			mergeTestChange{write: map[string]string{"n": "ours\n"}},
			mergeTestChange{write: map[string]string{"n": "theirs\n"}},
		)

		require.ErrorIs(t, m.w.Merge(theirs, &MergeOptions{}), ErrMergeConflicts)

		assert.Equal(t,
			"<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>> "+theirs.String()+"\n",
			m.read("n"))
		assert.Equal(t, map[index.Stage]plumbing.Hash{
			index.OurMode:   m.blob("ours\n"),
			index.TheirMode: m.blob("theirs\n"),
		}, m.stages("n"))
	})

	t.Run("same content merges", func(t *testing.T) {
		t.Parallel()
		m := newMergeTestRepo(t)

		_, theirs := m.diverge(
			map[string]string{"a": "a\n"},
			mergeTestChange{write: map[string]string{"n": "same\n", "o": "o\n"}},
			mergeTestChange{write: map[string]string{"n": "same\n", "t": "t\n"}},
		)

		require.NoError(t, m.w.Merge(theirs, &MergeOptions{}))
		assert.Equal(t, "same\n", m.read("n"))
		m.assertClean()
	})
}

func TestWorktreeMergeDirectoryFileConflict(t *testing.T) {
	t.Parallel()
	m := newMergeTestRepo(t)

	_, theirs := m.diverge(
		map[string]string{"a": "a\n"},
		mergeTestChange{write: map[string]string{"p/x": "x\n"}},
		mergeTestChange{write: map[string]string{"p": "file\n"}},
	)

	require.ErrorIs(t, m.w.Merge(theirs, &MergeOptions{}), ErrMergeConflicts)

	assert.Equal(t, map[index.Stage]plumbing.Hash{index.TheirMode: m.blob("file\n")}, m.stages("p"))
	assert.Equal(t, map[index.Stage]plumbing.Hash{0: m.blob("x\n")}, m.stages("p/x"))
	assert.Equal(t, "x\n", m.read("p/x"))
	assert.Equal(t, "file\n", m.read("p~"+theirs.String()))
	m.assertMergeHead(theirs)
}

func TestWorktreeMergeFileDirectoryConflict(t *testing.T) {
	t.Parallel()
	m := newMergeTestRepo(t)

	ours, theirs := m.diverge(
		map[string]string{"a": "a\n"},
		mergeTestChange{write: map[string]string{"p": "file\n"}},
		mergeTestChange{write: map[string]string{"p/x": "x\n"}},
	)

	require.ErrorIs(t, m.w.Merge(theirs, &MergeOptions{}), ErrMergeConflicts)

	assert.Equal(t, ours, m.head())
	assert.Equal(t, map[index.Stage]plumbing.Hash{index.OurMode: m.blob("file\n")}, m.stages("p"))
	assert.Equal(t, map[index.Stage]plumbing.Hash{0: m.blob("x\n")}, m.stages("p/x"))
	assert.Equal(t, "x\n", m.read("p/x"))
	assert.Equal(t, "file\n", m.read("p~HEAD"))
	assert.Empty(t, m.stages("p~HEAD"))
	m.assertMergeHead(theirs)

	// Keeping the directory resolves the conflict.
	require.NoError(t, m.fs.Remove("p~HEAD"))
	_, err := m.w.Add("p")
	require.NoError(t, err)
	assert.Empty(t, m.stages("p"))

	h, err := m.w.Commit("merge", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)
	commit, err := m.r.CommitObject(h)
	require.NoError(t, err)
	assert.Equal(t, []plumbing.Hash{ours, theirs}, commit.ParentHashes)
	m.assertClean()
}

func TestWorktreeMergeUncommittedChanges(t *testing.T) {
	t.Parallel()

	for name, dirty := range map[string]func(m *mergeTestRepo){
		"modified": func(m *mergeTestRepo) {
			require.NoError(t, util.WriteFile(m.fs, "a", []byte("dirty\n"), 0o644))
		},
		"staged": func(m *mergeTestRepo) {
			require.NoError(t, util.WriteFile(m.fs, "a", []byte("dirty\n"), 0o644))
			_, err := m.w.Add("a")
			require.NoError(t, err)
		},
		"untracked": func(m *mergeTestRepo) {
			require.NoError(t, util.WriteFile(m.fs, "untracked", []byte("u\n"), 0o644))
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m := newMergeTestRepo(t)

			ours, theirs := m.diverge(
				map[string]string{"a": "a\n"},
				mergeTestChange{write: map[string]string{"b": "b\n"}},
				mergeTestChange{write: map[string]string{"c": "c\n"}},
			)
			dirty(m)

			assert.ErrorIs(t, m.w.Merge(theirs, &MergeOptions{}), ErrUncommittedChanges)
			assert.Equal(t, ours, m.head())
			_, err := m.fs.Stat("c")
			assert.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestWorktreeMergeUnresolvedConflicts(t *testing.T) {
	t.Parallel()
	m := newMergeTestRepo(t)

	_, theirs := m.diverge(
		map[string]string{"f": "f\n"},
		mergeTestChange{write: map[string]string{"f": "ours\n"}},
		mergeTestChange{write: map[string]string{"f": "theirs\n"}},
	)

	require.ErrorIs(t, m.w.Merge(theirs, &MergeOptions{}), ErrMergeConflicts)
	assert.ErrorIs(t, m.w.Merge(theirs, &MergeOptions{}), ErrUncommittedChanges)
}

func TestWorktreeMergeAfterAbort(t *testing.T) {
	t.Parallel()
	m := newMergeTestRepo(t)

	ours, theirs := m.diverge(
		map[string]string{"f": "f\n"},
		mergeTestChange{write: map[string]string{"f": "ours\n"}},
		mergeTestChange{write: map[string]string{"f": "theirs\n"}},
	)

	require.ErrorIs(t, m.w.Merge(theirs, &MergeOptions{}), ErrMergeConflicts)
	require.NoError(t, m.w.Reset(&ResetOptions{Mode: HardReset}))
	assert.Equal(t, "ours\n", m.read("f"))
	assert.Equal(t, map[index.Stage]plumbing.Hash{0: m.blob("ours\n")}, m.stages("f"))

	require.ErrorIs(t, m.w.Merge(theirs, &MergeOptions{}), ErrMergeConflicts)
	assert.Equal(t, ours, m.head())
	m.assertMergeHead(theirs)
}

func TestWorktreeMergeCommitResolution(t *testing.T) {
	t.Parallel()
	m := newMergeTestRepo(t)

	ours, theirs := m.diverge(
		map[string]string{"f": "f\n", "g": "g\n"},
		mergeTestChange{write: map[string]string{"f": "ours\n"}},
		mergeTestChange{write: map[string]string{"f": "theirs\n", "g": "g2\n"}},
	)

	require.ErrorIs(t, m.w.Merge(theirs, &MergeOptions{}), ErrMergeConflicts)

	require.NoError(t, util.WriteFile(m.fs, "f", []byte("resolved\n"), 0o644))
	_, err := m.w.Add("f")
	require.NoError(t, err)
	assert.Equal(t, map[index.Stage]plumbing.Hash{0: m.blob("resolved\n")}, m.stages("f"))

	h, err := m.w.Commit("merge", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	commit, err := m.r.CommitObject(h)
	require.NoError(t, err)
	assert.Equal(t, []plumbing.Hash{ours, theirs}, commit.ParentHashes)
	assert.Equal(t, h, m.head())

	tree, err := commit.Tree()
	require.NoError(t, err)
	for name, content := range map[string]string{"f": "resolved\n", "g": "g2\n"} {
		f, err := tree.File(name)
		require.NoError(t, err)
		got, err := f.Contents()
		require.NoError(t, err)
		assert.Equal(t, content, got, name)
	}

	m.assertClean()
}

func TestWorktreeMergeCommitKeepingOurs(t *testing.T) {
	t.Parallel()
	m := newMergeTestRepo(t)

	ours, theirs := m.diverge(
		map[string]string{"f": "f\n"},
		mergeTestChange{write: map[string]string{"f": "ours\n"}},
		mergeTestChange{remove: []string{"f"}},
	)

	require.ErrorIs(t, m.w.Merge(theirs, &MergeOptions{}), ErrMergeConflicts)

	_, err := m.w.Add("f")
	require.NoError(t, err)

	h, err := m.w.Commit("merge", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	commit, err := m.r.CommitObject(h)
	require.NoError(t, err)
	assert.Equal(t, []plumbing.Hash{ours, theirs}, commit.ParentHashes)
	m.assertClean()
}

func TestWorktreeMergeAddDeletedConflict(t *testing.T) {
	t.Parallel()
	m := newMergeTestRepo(t)

	ours, theirs := m.diverge(
		map[string]string{"f": "f\n", "g": "g\n"},
		mergeTestChange{write: map[string]string{"f": "ours\n"}},
		mergeTestChange{remove: []string{"f"}},
	)

	require.ErrorIs(t, m.w.Merge(theirs, &MergeOptions{}), ErrMergeConflicts)

	require.NoError(t, m.fs.Remove("f"))
	_, err := m.w.Add("f")
	require.NoError(t, err)
	assert.Empty(t, m.stages("f"))

	h, err := m.w.Commit("merge", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	commit, err := m.r.CommitObject(h)
	require.NoError(t, err)
	assert.Equal(t, []plumbing.Hash{ours, theirs}, commit.ParentHashes)
	tree, err := commit.Tree()
	require.NoError(t, err)
	_, err = tree.File("f")
	assert.Error(t, err)
	m.assertClean()
}

func TestWorktreeMergeOnDisk(t *testing.T) {
	t.Parallel()

	r, err := PlainInit(t.TempDir(), false)
	require.NoError(t, err)
	m := newMergeTestRepoFrom(t, r)

	ours, theirs := m.diverge(
		map[string]string{"f": "f\n", "g": "g\n"},
		mergeTestChange{write: map[string]string{"f": "ours\n"}},
		mergeTestChange{write: map[string]string{"f": "theirs\n", "g": "g2\n"}},
	)

	require.ErrorIs(t, m.w.Merge(theirs, &MergeOptions{}), ErrMergeConflicts)

	dotgit := osfs.New(m.fs.Root())
	data, err := util.ReadFile(dotgit, ".git/MERGE_HEAD")
	require.NoError(t, err)
	assert.Equal(t, theirs.String()+"\n", string(data))

	idx, err := m.r.Storer.Index()
	require.NoError(t, err)
	entries := make([]string, 0, len(idx.Entries))
	for _, e := range idx.Entries {
		entries = append(entries, fmt.Sprintf("%s %d", e.Name, e.Stage))
	}
	assert.Equal(t, []string{"f 1", "f 2", "f 3", "g 0"}, entries)

	require.NoError(t, util.WriteFile(m.fs, "f", []byte("resolved\n"), 0o644))
	_, err = m.w.Add("f")
	require.NoError(t, err)
	h, err := m.w.Commit("merge", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	commit, err := m.r.CommitObject(h)
	require.NoError(t, err)
	assert.Equal(t, []plumbing.Hash{ours, theirs}, commit.ParentHashes)
	m.assertClean()
}

func TestWorktreeCommitWithGitFile(t *testing.T) {
	t.Parallel()

	fs := osfs.New(t.TempDir())
	r, err := Init(memory.NewStorage(), WithWorkTree(fs))
	require.NoError(t, err)
	require.NoError(t, util.WriteFile(fs, GitDirName, []byte("gitdir: ../repo.git\n"), 0o644))

	m := newMergeTestRepoFrom(t, r)
	h := m.commit("commit", mergeTestChange{write: map[string]string{"a": "a\n"}})
	assert.Equal(t, h, m.head())
}

func TestWorktreeMergeWithoutUserConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)

	m := newMergeTestRepo(t)
	ours, theirs := m.diverge(
		map[string]string{"a": "a\n"},
		mergeTestChange{write: map[string]string{"b": "b\n"}},
		mergeTestChange{write: map[string]string{"c": "c\n"}},
	)

	require.NoError(t, m.w.Merge(theirs, &MergeOptions{}))

	commit, err := m.r.CommitObject(m.head())
	require.NoError(t, err)
	assert.Equal(t, []plumbing.Hash{ours, theirs}, commit.ParentHashes)
	assert.Equal(t, defaultMergeAuthorName, commit.Author.Name)
	assert.Equal(t, defaultMergeAuthorEmail, commit.Author.Email)
	assert.Equal(t, commit.Author, commit.Committer)
}

func TestMergeLines(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name               string
		base, ours, theirs string
		want               string
		clean              bool
	}{
		{
			name: "separate changes", clean: true,
			base: "1\n2\n3\n4\n", ours: "one\n2\n3\n4\n", theirs: "1\n2\n3\nfour\n",
			want: "one\n2\n3\nfour\n",
		},
		{
			name: "same change", clean: true,
			base: "1\n2\n3\n", ours: "1\ntwo\n3\n", theirs: "1\ntwo\n3\n",
			want: "1\ntwo\n3\n",
		},
		{
			name: "insertions at both ends", clean: true,
			base: "1\n2\n", ours: "0\n1\n2\n", theirs: "1\n2\n3\n",
			want: "0\n1\n2\n3\n",
		},
		{
			name: "adjacent changes conflict",
			base: "1\n2\n3\n", ours: "one\n2\n3\n", theirs: "1\ntwo\n3\n",
			want: "<<<<<<< ours\none\n2\n=======\n1\ntwo\n>>>>>>> theirs\n3\n",
		},
		{
			name: "insertions at the same point conflict",
			base: "1\n2\n", ours: "1\nours\n2\n", theirs: "1\ntheirs\n2\n",
			want: "1\n<<<<<<< ours\nours\n=======\ntheirs\n>>>>>>> theirs\n2\n",
		},
		{
			name: "common lines are left out of the conflict",
			base: "1\n", ours: "a\nours\nz\n", theirs: "a\ntheirs\nz\n",
			want: "a\n<<<<<<< ours\nours\n=======\ntheirs\n>>>>>>> theirs\nz\n",
		},
		{
			name: "missing trailing newline",
			base: "1\n2", ours: "1\nours", theirs: "1\ntheirs",
			want: "1\n<<<<<<< ours\nours\n=======\ntheirs\n>>>>>>> theirs\n",
		},
		{
			name: "deletion against modification",
			base: "1\n2\n3\n", ours: "1\n3\n", theirs: "1\ntwo\n3\n",
			want: "1\n<<<<<<< ours\n=======\ntwo\n>>>>>>> theirs\n3\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, clean := mergeLines(tc.base, tc.ours, tc.theirs, "ours", "theirs")
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.clean, clean)
		})
	}
}
