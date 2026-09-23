package git

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/go-git/go-billy/v6/memfs"
	"github.com/go-git/go-billy/v6/util"
	"github.com/stretchr/testify/require"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/format/index"
	"github.com/go-git/go-git/v6/storage/memory"
)

func commitFile(t *testing.T, w *Worktree, name, content, msg string) plumbing.Hash {
	t.Helper()
	require.NoError(t, util.WriteFile(w.Filesystem, name, []byte(content), 0o644))
	_, err := w.Add(name)
	require.NoError(t, err)
	hash, err := w.Commit(msg, &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)
	return hash
}

func TestMergeFastForward(t *testing.T) {
	fs := memfs.New()
	r, err := Init(memory.NewStorage(), WithWorkTree(fs))
	require.NoError(t, err)
	w, err := r.Worktree()
	require.NoError(t, err)

	commitFile(t, w, "a.txt", "base\n", "base")
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: "refs/heads/feature", Create: true}))
	feature := commitFile(t, w, "a.txt", "feature\n", "feature")
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: "refs/heads/master"}))

	require.NoError(t, w.Merge(feature, &MergeOptions{}))
	head, err := r.Head()
	require.NoError(t, err)
	require.Equal(t, feature, head.Hash())
	got, err := util.ReadFile(fs, "a.txt")
	require.NoError(t, err)
	require.Equal(t, "feature\n", string(got))
}

func TestMergeCleanAndConflict(t *testing.T) {
	fs := memfs.New()
	r, err := Init(memory.NewStorage(), WithWorkTree(fs))
	require.NoError(t, err)
	w, err := r.Worktree()
	require.NoError(t, err)

	base := commitFile(t, w, "a.txt", "a\nb\nc\n", "base")
	_ = base
	require.NoError(t, util.WriteFile(fs, "same.txt", []byte("x\nx\n"), 0o644))
	_, err = w.Add("same.txt")
	require.NoError(t, err)
	_, err = w.Commit("same", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: "refs/heads/feature", Create: true}))
	// non-overlapping edit plus a conflicting edit on same.txt with repeated lines
	require.NoError(t, util.WriteFile(fs, "a.txt", []byte("a\nb\nC\n"), 0o644))
	require.NoError(t, util.WriteFile(fs, "same.txt", []byte("x\nY\nx\n"), 0o644))
	require.NoError(t, util.WriteFile(fs, "only-theirs.txt", []byte("t\n"), 0o644))
	_, err = w.Add("a.txt")
	require.NoError(t, err)
	_, err = w.Add("same.txt")
	require.NoError(t, err)
	_, err = w.Add("only-theirs.txt")
	require.NoError(t, err)
	feature, err := w.Commit("feature", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: "refs/heads/master"}))
	require.NoError(t, util.WriteFile(fs, "a.txt", []byte("A\nb\nc\n"), 0o644))
	require.NoError(t, util.WriteFile(fs, "same.txt", []byte("x\nZ\nx\n"), 0o644))
	_, err = w.Add("a.txt")
	require.NoError(t, err)
	_, err = w.Add("same.txt")
	require.NoError(t, err)
	ours, err := w.Commit("ours", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	err = w.Merge(feature, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)

	merged, err := util.ReadFile(fs, "a.txt")
	require.NoError(t, err)
	require.Equal(t, "A\nb\nC\n", string(merged))

	added, err := util.ReadFile(fs, "only-theirs.txt")
	require.NoError(t, err)
	require.Equal(t, "t\n", string(added))

	conflict, err := util.ReadFile(fs, "same.txt")
	require.NoError(t, err)
	text := string(conflict)
	require.Contains(t, text, "<<<<<<< HEAD")
	require.Contains(t, text, "=======")
	require.Contains(t, text, ">>>>>>> "+feature.String())

	raw, err := util.ReadFile(fs, mergeHeadFile)
	require.NoError(t, err)
	require.Equal(t, feature.String()+"\n", string(raw))

	idx, err := r.Storer.Index()
	require.NoError(t, err)
	stages := map[index.Stage]bool{}
	for _, e := range idx.Entries {
		if e.Name == "same.txt" {
			stages[e.Stage] = true
			require.NotEqual(t, index.Stage(0), e.Stage)
		}
	}
	require.Equal(t, map[index.Stage]bool{1: true, 2: true, 3: true}, stages)

	// resolving and committing consumes MERGE_HEAD as the second parent
	require.NoError(t, util.WriteFile(fs, "same.txt", []byte("x\nresolved\nx\n"), 0o644))
	_, err = w.Add("same.txt")
	require.NoError(t, err)
	idx, err = r.Storer.Index()
	require.NoError(t, err)
	var staged int
	for _, e := range idx.Entries {
		if e.Name == "same.txt" {
			staged++
			require.Equal(t, index.Stage(0), e.Stage)
		}
	}
	require.Equal(t, 1, staged)

	commitHash, err := w.Commit("merge", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)
	commit, err := r.CommitObject(commitHash)
	require.NoError(t, err)
	require.Equal(t, []plumbing.Hash{ours, feature}, commit.ParentHashes)
	_, err = fs.Open(mergeHeadFile)
	require.Error(t, err)
	require.True(t, os.IsNotExist(err))
}

func TestMergeNoUserConfig(t *testing.T) {
	fs := memfs.New()
	r, err := Init(memory.NewStorage(), WithWorkTree(fs))
	require.NoError(t, err)
	w, err := r.Worktree()
	require.NoError(t, err)

	commitFile(t, w, "a.txt", "a\nb\n", "base")
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: "refs/heads/feature", Create: true}))
	feature := commitFile(t, w, "b.txt", "b\n", "feature")
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: "refs/heads/master"}))
	ours := commitFile(t, w, "c.txt", "c\n", "ours")

	require.NoError(t, w.Merge(feature, &MergeOptions{}))
	head, err := r.Head()
	require.NoError(t, err)
	commit, err := r.CommitObject(head.Hash())
	require.NoError(t, err)
	require.Equal(t, []plumbing.Hash{ours, feature}, commit.ParentHashes)
	_, err = fs.Open(mergeHeadFile)
	require.Error(t, err)
	require.True(t, os.IsNotExist(err))
}

func TestMergeDeleteModifyAndAddAdd(t *testing.T) {
	fs := memfs.New()
	r, err := Init(memory.NewStorage(), WithWorkTree(fs))
	require.NoError(t, err)
	w, err := r.Worktree()
	require.NoError(t, err)

	commitFile(t, w, "gone.txt", "base\n", "base")
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: "refs/heads/feature", Create: true}))
	require.NoError(t, util.WriteFile(fs, "gone.txt", []byte("modified\n"), 0o644))
	require.NoError(t, util.WriteFile(fs, "new.txt", []byte("theirs\n"), 0o644))
	_, err = w.Add("gone.txt")
	require.NoError(t, err)
	_, err = w.Add("new.txt")
	require.NoError(t, err)
	feature, err := w.Commit("feature", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: "refs/heads/master"}))
	_, err = w.Remove("gone.txt")
	require.NoError(t, err)
	require.NoError(t, util.WriteFile(fs, "new.txt", []byte("ours\n"), 0o644))
	_, err = w.Add("new.txt")
	require.NoError(t, err)
	_, err = w.Commit("ours", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	err = w.Merge(feature, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)

	idx, err := r.Storer.Index()
	require.NoError(t, err)
	gone := map[index.Stage]bool{}
	add := map[index.Stage]bool{}
	for _, e := range idx.Entries {
		switch e.Name {
		case "gone.txt":
			gone[e.Stage] = true
		case "new.txt":
			add[e.Stage] = true
		}
	}
	require.Equal(t, map[index.Stage]bool{index.AncestorMode: true, index.TheirMode: true}, gone)
	require.Equal(t, map[index.Stage]bool{index.OurMode: true, index.TheirMode: true}, add)
	body, err := util.ReadFile(fs, "new.txt")
	require.NoError(t, err)
	require.Contains(t, string(body), "<<<<<<< HEAD")
}

func TestMergeFileDirectory(t *testing.T) {
	fs := memfs.New()
	r, err := Init(memory.NewStorage(), WithWorkTree(fs))
	require.NoError(t, err)
	w, err := r.Worktree()
	require.NoError(t, err)

	commitFile(t, w, "keep.txt", "k\n", "keep")
	commitFile(t, w, "sub/f", "base\n", "base")
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: "refs/heads/feature", Create: true}))
	_, err = w.Remove("sub/f")
	require.NoError(t, err)
	require.NoError(t, util.WriteFile(fs, "sub/f/nested", []byte("dir\n"), 0o644))
	_, err = w.Add("sub/f/nested")
	require.NoError(t, err)
	feature, err := w.Commit("feature", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: "refs/heads/master"}))
	require.NoError(t, util.WriteFile(fs, "sub/f", []byte("file\n"), 0o644))
	_, err = w.Add("sub/f")
	require.NoError(t, err)
	_, err = w.Commit("ours", &CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	err = w.Merge(feature, &MergeOptions{})
	require.ErrorIs(t, err, ErrMergeConflicts)
	idx, err := r.Storer.Index()
	require.NoError(t, err)
	var saw bool
	for _, e := range idx.Entries {
		if e.Name == "sub/f" && e.Stage == index.OurMode {
			saw = true
			require.Equal(t, filemode.Regular, e.Mode)
		}
		if e.Name == "sub/f" && e.Stage == index.TheirMode {
			t.Fatalf("deleting side should not have a blob stage")
		}
	}
	require.True(t, saw)
	_, err = fs.Stat(mergeHeadFile)
	require.NoError(t, err)
	_ = feature
}

func TestMergeDirty(t *testing.T) {
	fs := memfs.New()
	r, err := Init(memory.NewStorage(), WithWorkTree(fs))
	require.NoError(t, err)
	w, err := r.Worktree()
	require.NoError(t, err)
	commitFile(t, w, "a.txt", "a\n", "base")
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: "refs/heads/feature", Create: true}))
	feature := commitFile(t, w, "a.txt", "b\n", "feature")
	require.NoError(t, w.Checkout(&CheckoutOptions{Branch: "refs/heads/master"}))
	require.NoError(t, util.WriteFile(fs, "a.txt", []byte("dirty\n"), 0o644))
	err = w.Merge(feature, &MergeOptions{})
	require.ErrorIs(t, err, ErrUncommittedChanges)
}

func TestMergeTextRepeatedLines(t *testing.T) {
	base := []byte("x\nx\n")
	ours := []byte("x\nY\nx\n")
	theirs := []byte("x\nx\nZ\n")
	got, clean := mergeText(base, ours, theirs)
	if !clean {
		t.Fatalf("expected clean merge, got conflict")
	}
	if string(got) != "x\nY\nx\nZ\n" {
		t.Fatalf("got %q", got)
	}
	_, clean = mergeText(base, ours, []byte("x\nZ\nx\n"))
	if clean {
		t.Fatal("expected conflict on overlapping repeated lines")
	}
}

func TestReadMergeHeadPlain(t *testing.T) {
	fs := memfs.New()
	r, err := Init(memory.NewStorage(), WithWorkTree(fs))
	require.NoError(t, err)
	w, err := r.Worktree()
	require.NoError(t, err)
	h := plumbing.NewHash("0123456789abcdef0123456789abcdef01234567")
	require.NoError(t, w.writeMergeHead(h))
	f, err := fs.Open(mergeHeadFile)
	require.NoError(t, err)
	b, err := io.ReadAll(f)
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(string(b), "\n"))
	got, ok, err := w.readMergeHead()
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, h, got)
}
