package git

import (
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-billy/v6"
	"github.com/go-git/go-billy/v6/memfs"
	"github.com/go-git/go-billy/v6/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/go-git/go-git/v6/plumbing"
	format "github.com/go-git/go-git/v6/plumbing/format/config"
	"github.com/go-git/go-git/v6/plumbing/format/index"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/storage/memory"
)

type mergeFixture struct {
	t  *testing.T
	r  *Repository
	w  *Worktree
	fs billy.Filesystem
}

func newMergeFixture(t *testing.T) *mergeFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	fs := memfs.New()
	r, err := Init(memory.NewStorage(), WithWorkTree(fs))
	require.NoError(t, err)

	w, err := r.Worktree()
	require.NoError(t, err)

	return &mergeFixture{t: t, r: r, w: w, fs: fs}
}

func (f *mergeFixture) commit(msg string, files map[string]string, removed ...string) plumbing.Hash {
	f.t.Helper()
	for name, content := range files {
		require.NoError(f.t, util.WriteFile(f.fs, name, []byte(content), 0o644))
		_, err := f.w.Add(name)
		require.NoError(f.t, err)
	}
	for _, name := range removed {
		_, err := f.w.Remove(name)
		require.NoError(f.t, err)
	}

	h, err := f.w.Commit(msg, &CommitOptions{Author: &object.Signature{
		Name: "foo", Email: "foo@foo.foo", When: time.Now(),
	}})
	require.NoError(f.t, err)
	return h
}

func (f *mergeFixture) checkout(h plumbing.Hash) {
	f.t.Helper()
	require.NoError(f.t, f.w.Reset(&ResetOptions{Commit: h, Mode: HardReset}))
}

func (f *mergeFixture) read(name string) string {
	f.t.Helper()
	b, err := util.ReadFile(f.fs, name)
	require.NoError(f.t, err)
	return string(b)
}

func (f *mergeFixture) stages(name string) map[index.Stage]plumbing.Hash {
	f.t.Helper()
	idx, err := f.r.Storer.Index()
	require.NoError(f.t, err)

	res := map[index.Stage]plumbing.Hash{}
	for _, e := range idx.Entries {
		if e.Name == name {
			res[e.Stage] = e.Hash
		}
	}
	return res
}

func (f *mergeFixture) blobHash(content string) plumbing.Hash {
	h := plumbing.NewHasher(format.SHA1, plumbing.BlobObject, int64(len(content)))
	_, _ = h.Write([]byte(content))
	return h.Sum()
}

func (f *mergeFixture) head() *object.Commit {
	f.t.Helper()
	ref, err := f.r.Head()
	require.NoError(f.t, err)
	c, err := f.r.CommitObject(ref.Hash())
	require.NoError(f.t, err)
	return c
}

// diverge creates a base commit, a "theirs" commit on top of it and an "ours"
// commit on top of base, leaving HEAD at ours. It returns theirs.
func (f *mergeFixture) diverge(base, ours, theirs map[string]string, oursRm, theirsRm []string) plumbing.Hash {
	f.t.Helper()
	b := f.commit("base", base)
	th := f.commit("theirs", theirs, theirsRm...)
	f.checkout(b)
	f.commit("ours", ours, oursRm...)
	return th
}

func TestMergeFastForward(t *testing.T) {
	f := newMergeFixture(t)
	base := f.commit("base", map[string]string{"a": "a\n"})
	next := f.commit("next", map[string]string{"b": "b\n"})
	f.checkout(base)

	require.NoError(t, f.w.Merge(next, &MergeOptions{}))
	assert.Equal(t, next, f.head().Hash)
	assert.Equal(t, "b\n", f.read("b"))

	require.NoError(t, f.w.Merge(base, &MergeOptions{}))
	assert.Equal(t, next, f.head().Hash)
}

func TestMergeCleanCreatesMergeCommit(t *testing.T) {
	f := newMergeFixture(t)
	theirs := f.diverge(
		map[string]string{"f": "1\n2\n3\n4\n5\n6\n7\n"},
		map[string]string{"f": "one\n2\n3\n4\n5\n6\n7\n", "ours": "o\n"},
		map[string]string{"f": "1\n2\n3\n4\n5\n6\nseven\n", "theirs": "t\n"},
		nil, nil,
	)
	ours := f.head().Hash

	require.NoError(t, f.w.Merge(theirs, &MergeOptions{}))

	head := f.head()
	assert.Equal(t, []plumbing.Hash{ours, theirs}, head.ParentHashes)
	assert.Equal(t, "go-git", head.Author.Name)
	assert.Equal(t, "one\n2\n3\n4\n5\n6\nseven\n", f.read("f"))
	assert.Equal(t, "o\n", f.read("ours"))
	assert.Equal(t, "t\n", f.read("theirs"))

	file, err := head.File("f")
	require.NoError(t, err)
	content, err := file.Contents()
	require.NoError(t, err)
	assert.Equal(t, "one\n2\n3\n4\n5\n6\nseven\n", content)

	status, err := f.w.Status()
	require.NoError(t, err)
	assert.True(t, status.IsClean(), status)
}

func TestMergeConflictAndResolve(t *testing.T) {
	f := newMergeFixture(t)
	theirs := f.diverge(
		map[string]string{"f": "a\nb\nc\n", "g": "1\n2\n3\n4\n5\n"},
		map[string]string{"f": "a\nours\nc\n", "g": "one\n2\n3\n4\n5\n"},
		map[string]string{"f": "a\ntheirs\nc\n", "g": "1\n2\n3\n4\nfive\n"},
		nil, nil,
	)
	ours := f.head().Hash

	err := f.w.Merge(theirs, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)

	assert.Equal(t, "a\n<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>> "+theirs.String()+"\nc\n", f.read("f"))
	assert.Equal(t, "one\n2\n3\n4\nfive\n", f.read("g"))
	assert.Equal(t, theirs.String()+"\n", f.read(".git/MERGE_HEAD"))
	assert.Equal(t, ours, f.head().Hash)

	assert.Equal(t, map[index.Stage]plumbing.Hash{
		1: f.blobHash("a\nb\nc\n"),
		2: f.blobHash("a\nours\nc\n"),
		3: f.blobHash("a\ntheirs\nc\n"),
	}, f.stages("f"))
	assert.Equal(t, map[index.Stage]plumbing.Hash{0: f.blobHash("one\n2\n3\n4\nfive\n")}, f.stages("g"))

	require.ErrorIs(t, f.w.Merge(theirs, &MergeOptions{}), ErrUncommittedChanges)

	require.NoError(t, util.WriteFile(f.fs, "f", []byte("a\nresolved\nc\n"), 0o644))
	_, err = f.w.Add("f")
	require.NoError(t, err)
	assert.Equal(t, map[index.Stage]plumbing.Hash{0: f.blobHash("a\nresolved\nc\n")}, f.stages("f"))

	h, err := f.w.Commit("merge", &CommitOptions{Author: &object.Signature{Name: "foo", Email: "foo@foo"}})
	require.NoError(t, err)

	c, err := f.r.CommitObject(h)
	require.NoError(t, err)
	assert.Equal(t, []plumbing.Hash{ours, theirs}, c.ParentHashes)

	_, err = f.fs.Lstat(".git/MERGE_HEAD")
	assert.Error(t, err)
}

func TestMergeResolveToOursCommits(t *testing.T) {
	f := newMergeFixture(t)
	theirs := f.diverge(
		map[string]string{"f": "a\n"},
		map[string]string{"f": "ours\n"},
		map[string]string{"f": "theirs\n"},
		nil, nil,
	)

	require.ErrorIs(t, f.w.Merge(theirs, nil), ErrMergeConflicts)
	require.NoError(t, util.WriteFile(f.fs, "f", []byte("ours\n"), 0o644))
	_, err := f.w.Add("f")
	require.NoError(t, err)
	assert.Equal(t, map[index.Stage]plumbing.Hash{0: f.blobHash("ours\n")}, f.stages("f"))

	_, err = f.w.Commit("merge", &CommitOptions{Author: &object.Signature{Name: "foo", Email: "foo@foo"}})
	require.NoError(t, err)
	assert.Len(t, f.head().ParentHashes, 2)
}

func TestMergeConflictRepeatedLines(t *testing.T) {
	f := newMergeFixture(t)
	theirs := f.diverge(
		map[string]string{"f": "x\nx\nx\nx\n"},
		map[string]string{"f": "x\ny\nx\nx\n"},
		map[string]string{"f": "x\nz\nx\nx\n"},
		nil, nil,
	)

	require.ErrorIs(t, f.w.Merge(theirs, &MergeOptions{}), ErrMergeConflicts)
	content := f.read("f")
	assert.Contains(t, content, "<<<<<<< HEAD\ny\n=======\nz\n>>>>>>>")
	assert.Len(t, f.stages("f"), 3)
}

func TestMergeDeleteModifyConflict(t *testing.T) {
	f := newMergeFixture(t)
	theirs := f.diverge(
		map[string]string{"f": "a\n", "keep": "k\n"},
		map[string]string{"f": "modified\n"},
		map[string]string{"other": "o\n"},
		nil, []string{"f"},
	)

	require.ErrorIs(t, f.w.Merge(theirs, &MergeOptions{}), ErrMergeConflicts)
	assert.Equal(t, map[index.Stage]plumbing.Hash{
		1: f.blobHash("a\n"),
		2: f.blobHash("modified\n"),
	}, f.stages("f"))
	assert.Equal(t, "modified\n", f.read("f"))
	assert.Equal(t, "o\n", f.read("other"))
}

func TestMergeAddAddConflict(t *testing.T) {
	f := newMergeFixture(t)
	theirs := f.diverge(
		map[string]string{"base": "b\n"},
		map[string]string{"new": "line\nours\n"},
		map[string]string{"new": "line\ntheirs\n"},
		nil, nil,
	)

	require.ErrorIs(t, f.w.Merge(theirs, &MergeOptions{}), ErrMergeConflicts)
	assert.Equal(t, map[index.Stage]plumbing.Hash{
		2: f.blobHash("line\nours\n"),
		3: f.blobHash("line\ntheirs\n"),
	}, f.stages("new"))
	assert.Contains(t, f.read("new"), "<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>>")
}

func TestMergeFileDirectoryConflict(t *testing.T) {
	f := newMergeFixture(t)
	theirs := f.diverge(
		map[string]string{"base": "b\n"},
		map[string]string{"p": "file\n"},
		map[string]string{"p/child": "child\n"},
		nil, nil,
	)

	require.ErrorIs(t, f.w.Merge(theirs, &MergeOptions{}), ErrMergeConflicts)
	assert.Equal(t, map[index.Stage]plumbing.Hash{2: f.blobHash("file\n")}, f.stages("p"))
	assert.Equal(t, "child\n", f.read("p/child"))
	assert.Equal(t, "file\n", f.read("p~HEAD"))
}

func TestMergeUncommittedChanges(t *testing.T) {
	f := newMergeFixture(t)
	theirs := f.diverge(
		map[string]string{"f": "a\n"},
		map[string]string{"o": "o\n"},
		map[string]string{"t": "t\n"},
		nil, nil,
	)

	require.NoError(t, util.WriteFile(f.fs, "f", []byte("dirty\n"), 0o644))
	require.ErrorIs(t, f.w.Merge(theirs, &MergeOptions{}), ErrUncommittedChanges)
}

func TestMergeLines(t *testing.T) {
	tests := []struct {
		name, base, ours, theirs, want string
		conflict                       bool
	}{
		{name: "identical", base: "a\n", ours: "b\n", theirs: "b\n", want: "b\n"},
		{name: "separate", base: "a\nb\nc\n", ours: "A\nb\nc\n", theirs: "a\nb\nC\n", want: "A\nb\nC\n"},
		{name: "insertions", base: "a\nb\n", ours: "0\na\nb\n", theirs: "a\nb\n9\n", want: "0\na\nb\n9\n"},
		{
			name: "overlap", base: "a\nb\nc\n", ours: "a\nX\nc\n", theirs: "a\nY\nc\n",
			want: "a\n<<<<<<< HEAD\nX\n=======\nY\n>>>>>>> T\nc\n", conflict: true,
		},
		{
			name: "no trailing newline", base: "a", ours: "b", theirs: "c",
			want: "<<<<<<< HEAD\nb\n=======\nc\n>>>>>>> T\n", conflict: true,
		},
		{
			name: "adjacent", base: "a\nb\n", ours: "A\nb\n", theirs: "a\nB\n",
			want: "<<<<<<< HEAD\nA\nb\n=======\na\nB\n>>>>>>> T\n", conflict: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, conflict := mergeLines([]byte(tc.base), []byte(tc.ours), []byte(tc.theirs), "HEAD", "T")
			assert.Equal(t, tc.want, string(got))
			assert.Equal(t, tc.conflict, conflict)
		})
	}
}

func TestMatchLines(t *testing.T) {
	a := strings.SplitAfter("a\nb\nc\na\nb\nb\na\n", "\n")
	b := strings.SplitAfter("c\nb\na\nb\na\nc\n", "\n")
	m := matchLines(a, b)

	last := -1
	matched := 0
	for i, j := range m {
		if j < 0 {
			continue
		}
		assert.Greater(t, j, last)
		assert.Equal(t, a[i], b[j])
		last = j
		matched++
	}
	assert.Equal(t, 5, matched)
}
