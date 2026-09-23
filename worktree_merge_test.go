package git

import (
	"math/rand"
	"os"
	"strings"
	"testing"

	"github.com/go-git/go-billy/v6"
	"github.com/go-git/go-billy/v6/memfs"
	"github.com/go-git/go-billy/v6/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/go-git/go-git/v6/plumbing"
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

	w, err := r.Worktree()
	require.NoError(t, err)

	return &mergeTestRepo{t: t, r: r, w: w, fs: fs}
}

// commit writes the given files (an empty content deletes the file) and
// commits them.
func (m *mergeTestRepo) commit(files map[string]string) plumbing.Hash {
	m.t.Helper()

	for name, content := range files {
		if content == "" {
			_, err := m.w.Remove(name)
			require.NoError(m.t, err)
			continue
		}

		require.NoError(m.t, util.WriteFile(m.fs, name, []byte(content), 0o644))
		_, err := m.w.Add(name)
		require.NoError(m.t, err)
	}

	h, err := m.w.Commit("commit", &CommitOptions{Author: defaultSignature()})
	require.NoError(m.t, err)
	return h
}

func (m *mergeTestRepo) checkout(branch string, create bool) {
	m.t.Helper()

	require.NoError(m.t, m.w.Checkout(&CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName(branch),
		Create: create,
	}))
}

// diverge creates a base commit, a "feature" branch with theirs changes and
// a commit with ours changes on the initial branch, which is left checked out.
func (m *mergeTestRepo) diverge(base, ours, theirs map[string]string) (plumbing.Hash, plumbing.Hash) {
	m.t.Helper()

	m.commit(base)

	initial, err := m.r.Head()
	require.NoError(m.t, err)

	m.checkout("feature", true)
	theirsHash := m.commit(theirs)

	m.checkout(initial.Name().Short(), false)
	oursHash := m.commit(ours)

	return oursHash, theirsHash
}

func (m *mergeTestRepo) read(name string) string {
	m.t.Helper()

	content, err := util.ReadFile(m.fs, name)
	require.NoError(m.t, err)
	return string(content)
}

func (m *mergeTestRepo) stages(name string) []index.Stage {
	m.t.Helper()

	idx, err := m.r.Storer.Index()
	require.NoError(m.t, err)

	var stages []index.Stage
	for _, e := range idx.Entries {
		if e.Name == name {
			stages = append(stages, e.Stage)
		}
	}
	return stages
}

func (m *mergeTestRepo) headCommitParents() []plumbing.Hash {
	m.t.Helper()

	head, err := m.r.Head()
	require.NoError(m.t, err)

	c, err := m.r.CommitObject(head.Hash())
	require.NoError(m.t, err)
	return c.ParentHashes
}

func (m *mergeTestRepo) assertMergeHead(expected plumbing.Hash) {
	m.t.Helper()

	content, err := util.ReadFile(m.fs, ".git/MERGE_HEAD")
	require.NoError(m.t, err)
	assert.Equal(m.t, expected.String(), strings.TrimSpace(string(content)))

	_, err = m.r.Reference("MERGE_HEAD", false)
	assert.Error(m.t, err)
}

func withoutUserConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func TestMergeFastForward(t *testing.T) {
	m := newMergeTestRepo(t)
	m.commit(map[string]string{"a": "a\n"})

	initial, err := m.r.Head()
	require.NoError(t, err)

	m.checkout("feature", true)
	target := m.commit(map[string]string{"b": "b\n"})
	m.checkout(initial.Name().Short(), false)

	require.NoError(t, m.w.Merge(target, &MergeOptions{}))

	head, err := m.r.Head()
	require.NoError(t, err)
	assert.Equal(t, initial.Name(), head.Name())
	assert.Equal(t, target, head.Hash())
	assert.Equal(t, "b\n", m.read("b"))

	status, err := m.w.Status()
	require.NoError(t, err)
	assert.True(t, status.IsClean())
}

func TestMergeAlreadyUpToDate(t *testing.T) {
	m := newMergeTestRepo(t)
	base := m.commit(map[string]string{"a": "a\n"})
	head := m.commit(map[string]string{"a": "b\n"})

	require.NoError(t, m.w.Merge(base, &MergeOptions{}))

	ref, err := m.r.Head()
	require.NoError(t, err)
	assert.Equal(t, head, ref.Hash())
}

func TestMergeCreatesMergeCommit(t *testing.T) {
	withoutUserConfig(t)

	m := newMergeTestRepo(t)
	ours, theirs := m.diverge(
		map[string]string{"a": "a\n", "c": "c\n"},
		map[string]string{"b": "b\n"},
		map[string]string{"c": "", "d/e": "e\n"},
	)

	require.NoError(t, m.w.Merge(theirs, &MergeOptions{}))

	assert.Equal(t, []plumbing.Hash{ours, theirs}, m.headCommitParents())
	assert.Equal(t, "a\n", m.read("a"))
	assert.Equal(t, "b\n", m.read("b"))
	assert.Equal(t, "e\n", m.read("d/e"))
	_, err := m.fs.Lstat("c")
	assert.True(t, os.IsNotExist(err))

	status, err := m.w.Status()
	require.NoError(t, err)
	assert.True(t, status.IsClean(), status.String())

	_, err = m.fs.Lstat(".git/MERGE_HEAD")
	assert.Error(t, err)
}

func TestMergeSameFileNonOverlapping(t *testing.T) {
	m := newMergeTestRepo(t)
	_, theirs := m.diverge(
		map[string]string{"f": "1\n2\n3\n4\n5\n"},
		map[string]string{"f": "one\n2\n3\n4\n5\n"},
		map[string]string{"f": "1\n2\n3\n4\nfive\n"},
	)

	require.NoError(t, m.w.Merge(theirs, &MergeOptions{}))
	assert.Equal(t, "one\n2\n3\n4\nfive\n", m.read("f"))
	assert.Equal(t, []index.Stage{0}, m.stages("f"))
}

func TestMergeContentConflict(t *testing.T) {
	m := newMergeTestRepo(t)
	ours, theirs := m.diverge(
		map[string]string{"f": "x\nx\nx\nx\n", "g": "g\n"},
		map[string]string{"f": "x\ny\nx\nx\n"},
		map[string]string{"f": "x\nz\nx\nx\n", "g": "G\n", "h": "h\n"},
	)

	err := m.w.Merge(theirs, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)

	assert.Equal(t, "x\n<<<<<<< HEAD\ny\n=======\nz\n>>>>>>> "+theirs.String()+"\nx\nx\n", m.read("f"))
	assert.Equal(t, []index.Stage{index.AncestorMode, index.OurMode, index.TheirMode}, m.stages("f"))

	assert.Equal(t, "G\n", m.read("g"))
	assert.Equal(t, "h\n", m.read("h"))
	assert.Equal(t, []index.Stage{0}, m.stages("g"))
	assert.Equal(t, []index.Stage{0}, m.stages("h"))

	m.assertMergeHead(theirs)

	head, err := m.r.Head()
	require.NoError(t, err)
	assert.Equal(t, ours, head.Hash())

	_, err = m.w.Commit("merge", &CommitOptions{Author: defaultSignature()})
	assert.ErrorIs(t, err, ErrUnmergedPaths)

	require.NoError(t, util.WriteFile(m.fs, "f", []byte("x\nyz\nx\nx\n"), 0o644))
	_, err = m.w.Add("f")
	require.NoError(t, err)
	assert.Equal(t, []index.Stage{0}, m.stages("f"))

	_, err = m.w.Commit("merge", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)
	assert.Equal(t, []plumbing.Hash{ours, theirs}, m.headCommitParents())

	_, err = m.fs.Lstat(".git/MERGE_HEAD")
	assert.Error(t, err)

	status, err := m.w.Status()
	require.NoError(t, err)
	assert.True(t, status.IsClean(), status.String())
}

func TestMergeResolveWithBaseContent(t *testing.T) {
	m := newMergeTestRepo(t)
	ours, theirs := m.diverge(
		map[string]string{"f": "base\n"},
		map[string]string{"f": "ours\n"},
		map[string]string{"f": "theirs\n"},
	)

	require.ErrorIs(t, m.w.Merge(theirs, &MergeOptions{}), ErrMergeConflicts)

	require.NoError(t, util.WriteFile(m.fs, "f", []byte("base\n"), 0o644))
	require.NoError(t, m.w.AddWithOptions(&AddOptions{All: true}))
	assert.Equal(t, []index.Stage{0}, m.stages("f"))

	_, err := m.w.Commit("merge", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)
	assert.Equal(t, []plumbing.Hash{ours, theirs}, m.headCommitParents())
}

func TestMergeDeleteModifyConflict(t *testing.T) {
	m := newMergeTestRepo(t)
	_, theirs := m.diverge(
		map[string]string{"f": "f\n", "g": "g\n"},
		map[string]string{"f": "modified\n"},
		map[string]string{"f": "", "g": "G\n"},
	)

	require.ErrorIs(t, m.w.Merge(theirs, &MergeOptions{}), ErrMergeConflicts)

	assert.Equal(t, []index.Stage{index.AncestorMode, index.OurMode}, m.stages("f"))
	assert.Equal(t, "modified\n", m.read("f"))
	assert.Equal(t, "G\n", m.read("g"))
	m.assertMergeHead(theirs)

	_, err := m.w.Remove("f")
	require.NoError(t, err)
	assert.Empty(t, m.stages("f"))
}

func TestMergeAddAddConflict(t *testing.T) {
	m := newMergeTestRepo(t)
	_, theirs := m.diverge(
		map[string]string{"a": "a\n"},
		map[string]string{"n": "same\nours\n"},
		map[string]string{"n": "same\ntheirs\n"},
	)

	require.ErrorIs(t, m.w.Merge(theirs, &MergeOptions{}), ErrMergeConflicts)
	assert.Equal(t, []index.Stage{index.OurMode, index.TheirMode}, m.stages("n"))
	assert.Equal(t, "same\n<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>> "+theirs.String()+"\n", m.read("n"))
}

func TestMergeAddAddIdentical(t *testing.T) {
	m := newMergeTestRepo(t)
	_, theirs := m.diverge(
		map[string]string{"a": "a\n"},
		map[string]string{"n": "same\n", "o": "o\n"},
		map[string]string{"n": "same\n", "t": "t\n"},
	)

	require.NoError(t, m.w.Merge(theirs, &MergeOptions{}))
	assert.Equal(t, "same\n", m.read("n"))
	assert.Equal(t, []index.Stage{0}, m.stages("n"))
}

func TestMergeFileDirectoryConflict(t *testing.T) {
	m := newMergeTestRepo(t)
	_, theirs := m.diverge(
		map[string]string{"a": "a\n"},
		map[string]string{"d": "file\n"},
		map[string]string{"d/f": "nested\n"},
	)

	require.ErrorIs(t, m.w.Merge(theirs, &MergeOptions{}), ErrMergeConflicts)

	assert.Equal(t, []index.Stage{index.OurMode}, m.stages("d"))
	assert.Equal(t, []index.Stage{0}, m.stages("d/f"))
	assert.Equal(t, "nested\n", m.read("d/f"))
	assert.Equal(t, "file\n", m.read("d~HEAD"))
	m.assertMergeHead(theirs)
}

func TestMergeUncommittedChanges(t *testing.T) {
	m := newMergeTestRepo(t)
	_, theirs := m.diverge(
		map[string]string{"a": "a\n"},
		map[string]string{"b": "b\n"},
		map[string]string{"c": "c\n"},
	)

	require.NoError(t, util.WriteFile(m.fs, "a", []byte("dirty\n"), 0o644))
	assert.ErrorIs(t, m.w.Merge(theirs, &MergeOptions{}), ErrUncommittedChanges)
	assert.Equal(t, "dirty\n", m.read("a"))
}

func TestMergeText(t *testing.T) {
	tests := []struct {
		name, base, ours, theirs, expected string
		clean                              bool
	}{
		{
			name: "separate changes", clean: true,
			base: "a\nb\nc\nd\n", ours: "A\nb\nc\nd\n", theirs: "a\nb\nc\nD\n",
			expected: "A\nb\nc\nD\n",
		},
		{
			name: "same change", clean: true,
			base: "a\nb\n", ours: "a\nB\n", theirs: "a\nB\n",
			expected: "a\nB\n",
		},
		{
			name: "insertions at different places", clean: true,
			base: "a\nb\nc\n", ours: "0\na\nb\nc\n", theirs: "a\nb\nc\nd\n",
			expected: "0\na\nb\nc\nd\n",
		},
		{
			name: "adjacent changes", clean: false,
			base: "a\nb\n", ours: "A\nb\n", theirs: "a\nB\n",
			expected: "<<<<<<< ours\nA\nb\n=======\na\nB\n>>>>>>> theirs\n",
		},
		{
			name: "repeated lines", clean: false,
			base: "x\nx\nx\n", ours: "x\ny\nx\n", theirs: "x\nz\nx\n",
			expected: "x\n<<<<<<< ours\ny\n=======\nz\n>>>>>>> theirs\nx\n",
		},
		{
			name: "missing trailing newline", clean: false,
			base: "a", ours: "b", theirs: "c",
			expected: "<<<<<<< ours\nb\n=======\nc\n>>>>>>> theirs\n",
		},
		{
			name: "delete and modify lines", clean: false,
			base: "a\nb\nc\n", ours: "a\nc\n", theirs: "a\nB\nc\n",
			expected: "a\n<<<<<<< ours\n=======\nB\n>>>>>>> theirs\nc\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, clean := mergeText([]byte(tc.base), []byte(tc.ours), []byte(tc.theirs), "ours", "theirs")
			assert.Equal(t, tc.clean, clean)
			assert.Equal(t, tc.expected, string(out))
		})
	}
}

func TestDiffLinesReconstructs(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	gen := func() []string {
		lines := make([]string, rnd.Intn(20))
		for i := range lines {
			lines[i] = string(rune('a'+rnd.Intn(3))) + "\n"
		}
		return lines
	}

	for range 500 {
		a, b := gen(), gen()

		var out []string
		pos := 0
		for _, h := range diffLines(a, b) {
			require.GreaterOrEqual(t, h.baseStart, pos)
			out = append(out, a[pos:h.baseStart]...)
			out = append(out, b[h.sideStart:h.sideEnd]...)
			pos = h.baseEnd
		}
		out = append(out, a[pos:]...)

		require.Equal(t, strings.Join(b, ""), strings.Join(out, ""))
	}
}
