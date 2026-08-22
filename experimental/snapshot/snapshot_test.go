package snapshot_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"sync"
	"testing"

	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
	"github.com/tetratelabs/wazero/experimental/snapshot"
	"github.com/tetratelabs/wazero/experimental/wazerotest"
	"github.com/tetratelabs/wazero/internal/testing/require"
)

func newMod(contents []byte) *wazerotest.Module {
	return wazerotest.NewModule(&wazerotest.Memory{Bytes: append([]byte{}, contents...)})
}

func TestCaptureSnapshot_Errors(t *testing.T) {
	c := snapshot.NewCoordinator()

	_, err := c.CaptureSnapshot()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no modules")

	_, err = c.CaptureSnapshot(nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "module closed")

	closed := newMod([]byte{1})
	require.NoError(t, closed.Close(context.Background()))
	_, err = c.CaptureSnapshot(closed)
	require.Error(t, err)
	require.Contains(t, err.Error(), "module closed")
}

func TestCaptureIncremental_Errors(t *testing.T) {
	c := snapshot.NewCoordinator()
	mod := newMod([]byte{1, 2, 3})
	base, err := c.CaptureSnapshot(mod)
	require.NoError(t, err)

	_, err = c.CaptureIncremental(nil, mod)
	require.Error(t, err)
	require.Contains(t, err.Error(), "baseline snapshot is nil")

	_, err = c.CaptureIncremental(base)
	require.Error(t, err)
	require.Contains(t, err.Error(), "module count mismatch")

	_, err = c.CaptureIncremental(base, mod, newMod([]byte{4}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "module count mismatch")
}

func TestCaptureAndVersions(t *testing.T) {
	c := snapshot.NewCoordinator()
	a := newMod([]byte{1, 2, 3, 4})
	b := newMod([]byte{9, 8, 7, 6})

	full, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)
	require.Equal(t, uint64(1), full.Version())
	require.Equal(t, [][]byte{{1, 2, 3, 4}, {9, 8, 7, 6}}, full.Data())

	require.True(t, a.Memory().WriteByte(1, 20))
	inc, err := c.CaptureIncremental(full, a, b)
	require.NoError(t, err)
	require.Equal(t, uint64(2), inc.Version())
	require.Equal(t, [][]byte{{1, 20, 3, 4}, {9, 8, 7, 6}}, inc.Data())

	full2, err := c.CaptureSnapshot(b)
	require.NoError(t, err)
	require.Equal(t, uint64(3), full2.Version())

	inc2, err := c.CaptureIncremental(inc, a, b)
	require.NoError(t, err)
	require.Equal(t, uint64(4), inc2.Version())
	require.Equal(t, [][]byte{{1, 20, 3, 4}, {9, 8, 7, 6}}, inc2.Data())
}

func TestDataAndTagsAreDeepCopies(t *testing.T) {
	c := snapshot.NewCoordinator()
	mod := newMod([]byte{1, 2, 3})
	snap, err := c.CaptureSnapshot(mod)
	require.NoError(t, err)

	snap.SetTag("env", "test")
	data1 := snap.Data()
	data1[0][0] = 99
	tags1 := snap.Tags()
	tags1["env"] = "mutated"
	tags1["extra"] = "x"

	require.Equal(t, [][]byte{{1, 2, 3}}, snap.Data())
	require.Equal(t, map[string]string{"env": "test"}, snap.Tags())
}

func TestCompressedData_FullMatchesGzipConcat(t *testing.T) {
	c := snapshot.NewCoordinator()
	a := newMod([]byte{1, 2, 3})
	b := newMod([]byte{4, 5})
	snap, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)

	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	for _, d := range snap.Data() {
		_, err = w.Write(d)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	require.Equal(t, buf.Bytes(), snap.CompressedData())

	r, err := gzip.NewReader(bytes.NewReader(snap.CompressedData()))
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, []byte{1, 2, 3, 4, 5}, got)
}

func TestCompressedData_IncrementalSmaller(t *testing.T) {
	c := snapshot.NewCoordinator()
	mod := wazerotest.NewModule(wazerotest.NewMemory(wazerotest.PageSize))
	require.True(t, mod.Memory().WriteByte(0, 1))

	base, err := c.CaptureSnapshot(mod)
	require.NoError(t, err)
	require.True(t, mod.Memory().WriteByte(10, 7))

	inc, err := c.CaptureIncremental(base, mod)
	require.NoError(t, err)
	require.True(t, len(inc.CompressedData()) < len(base.CompressedData()))
	require.Equal(t, byte(7), inc.Data()[0][10])

	require.True(t, mod.Memory().WriteByte(11, 8))
	inc2, err := c.CaptureIncremental(inc, mod)
	require.NoError(t, err)
	require.True(t, len(inc2.CompressedData()) < len(inc.CompressedData()))
	require.Equal(t, byte(7), inc2.Data()[0][10])
	require.Equal(t, byte(8), inc2.Data()[0][11])
}

func TestCompare(t *testing.T) {
	c := snapshot.NewCoordinator()
	a := newMod([]byte{1, 2, 3, 4})
	b := newMod([]byte{5, 6, 7})
	left, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)

	require.True(t, a.Memory().WriteByte(0, 9))
	require.True(t, a.Memory().WriteByte(3, 8))
	require.True(t, b.Memory().WriteByte(1, 0))
	right, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)

	diffs := left.Compare(right)
	require.Equal(t, []snapshot.DiffEntry{
		{Offset: 0, OldValue: 1, NewValue: 9},
		{Offset: 3, OldValue: 4, NewValue: 8},
		{Offset: 1, OldValue: 6, NewValue: 0},
	}, diffs)
	require.Equal(t, 0, len(left.Compare(left)))
}

func TestRestore_IdentityAndPositional(t *testing.T) {
	c := snapshot.NewCoordinator()
	a := newMod([]byte{1, 1, 1})
	b := newMod([]byte{2, 2, 2})
	snap, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)

	require.True(t, a.Memory().WriteByte(0, 9))
	require.True(t, b.Memory().WriteByte(0, 8))

	// Identity: restore only B.
	require.NoError(t, c.RestoreSnapshot(snap, b))
	got, ok := b.Memory().ReadByte(0)
	require.True(t, ok)
	require.Equal(t, byte(2), got)
	got, ok = a.Memory().ReadByte(0)
	require.True(t, ok)
	require.Equal(t, byte(9), got)

	// Fewer modules, no identity match: skip, no error.
	other := newMod([]byte{7, 7, 7})
	require.NoError(t, c.RestoreSnapshot(snap, other))
	got, ok = other.Memory().ReadByte(0)
	require.True(t, ok)
	require.Equal(t, byte(7), got)

	// Equal count, different instances: positional.
	a2 := newMod([]byte{0, 0, 0})
	b2 := newMod([]byte{0, 0, 0})
	require.NoError(t, c.RestoreSnapshot(snap, a2, b2))
	require.Equal(t, byte(1), mustRead(t, a2, 0))
	require.Equal(t, byte(2), mustRead(t, b2, 0))

	// Reordered originals: identity wins over position.
	require.True(t, a.Memory().WriteByte(0, 3))
	require.True(t, b.Memory().WriteByte(0, 4))
	require.NoError(t, c.RestoreSnapshot(snap, b, a))
	require.Equal(t, byte(1), mustRead(t, a, 0))
	require.Equal(t, byte(2), mustRead(t, b, 0))
}

func TestRestore_IncompatibleAndInsufficient(t *testing.T) {
	c := snapshot.NewCoordinator()
	mod := newMod([]byte{1, 2, 3, 4})
	snap, err := c.CaptureSnapshot(mod)
	require.NoError(t, err)

	err = c.RestoreSnapshot(snap, newMod([]byte{0}), newMod([]byte{0}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "incompatible module")

	small := newMod([]byte{0, 0})
	err = c.RestoreSnapshot(snap, small)
	require.Error(t, err)
	require.Equal(t, "insufficient_memory", snapshot.ErrorCode(err))
}

func TestRegistryAndContext(t *testing.T) {
	name := t.Name()
	defer snapshot.Unregister(name)

	_, ok := snapshot.Get(name)
	require.False(t, ok)

	c1 := snapshot.NewCoordinator()
	c2 := snapshot.NewCoordinator()
	snapshot.Register(name, c1)
	snapshot.Register(name, c2)
	got, ok := snapshot.Get(name)
	require.True(t, ok)
	require.Same(t, c2, got)

	snapshot.Unregister(name)
	_, ok = snapshot.Get(name)
	require.False(t, ok)

	ctx := snapshot.WithCoordinator(context.Background(), c1)
	require.Same(t, c1, snapshot.GetCoordinator(ctx))
	require.Nil(t, snapshot.GetCoordinator(context.Background()))
}

func TestSummarizeAndChain(t *testing.T) {
	c := snapshot.NewCoordinator()
	mod := newMod([]byte{1, 2, 3, 4})
	full, err := c.CaptureSnapshot(mod)
	require.NoError(t, err)
	require.Equal(t, snapshot.SnapshotSummary{
		TotalModules:  1,
		TotalBytes:    4,
		ModifiedBytes: 0,
		Version:       1,
	}, snapshot.Summarize(full))

	require.True(t, mod.Memory().WriteByte(1, 9))
	require.True(t, mod.Memory().WriteByte(2, 8))
	inc, err := c.CaptureIncremental(full, mod)
	require.NoError(t, err)
	require.Equal(t, snapshot.SnapshotSummary{
		TotalModules:  1,
		TotalBytes:    4,
		ModifiedBytes: 2,
		Version:       2,
	}, snapshot.Summarize(inc))

	chain := snapshot.NewChain()
	require.Equal(t, 0, chain.Len())
	require.Nil(t, chain.Head())
	chain.Push(full)
	chain.Push(inc)
	require.Equal(t, 2, chain.Len())
	require.True(t, chain.Head() == inc)
	snaps := chain.Snapshots()
	require.Equal(t, 2, len(snaps))
	require.True(t, snaps[0] == full)
	require.True(t, snaps[1] == inc)
	snaps[0] = nil
	require.True(t, chain.Snapshots()[0] == full)
}

func TestMarshalRoundTrip(t *testing.T) {
	c := snapshot.NewCoordinator()
	a := newMod([]byte{1, 2, 3})
	b := newMod([]byte{4, 5, 6})
	snap, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)
	snap.SetTag("k", "v")

	require.True(t, a.Memory().WriteByte(0, 99))
	inc, err := c.CaptureIncremental(snap, a, b)
	require.NoError(t, err)
	inc.SetTag("from", "inc")

	for _, in := range []snapshot.Snapshot{snap, inc} {
		encoded, err := snapshot.MarshalSnapshot(in)
		require.NoError(t, err)
		out, err := snapshot.UnmarshalSnapshot(encoded)
		require.NoError(t, err)
		require.Equal(t, in.Version(), out.Version())
		require.Equal(t, in.Data(), out.Data())
		require.Equal(t, in.Tags(), out.Tags())
		require.Equal(t, uint64(0), snapshot.Summarize(out).ModifiedBytes)
	}

	_, err = snapshot.MarshalSnapshot(nil)
	require.Error(t, err)
	_, err = snapshot.UnmarshalSnapshot(nil)
	require.Error(t, err)
	_, err = snapshot.UnmarshalSnapshot([]byte("nope"))
	require.Error(t, err)
}

func TestNewSnapshotCoordinator(t *testing.T) {
	c := experimental.NewSnapshotCoordinator()
	mod := newMod([]byte{42})
	snap, err := c.CaptureSnapshot(mod)
	require.NoError(t, err)
	require.Equal(t, uint64(1), snap.Version())
}

func TestCoordinatorConcurrent(t *testing.T) {
	c := snapshot.NewCoordinator()
	const n = 32
	var wg sync.WaitGroup
	versions := make(chan uint64, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			mod := newMod([]byte{byte(i)})
			snap, err := c.CaptureSnapshot(mod)
			require.NoError(t, err)
			versions <- snap.Version()
		}(i)
	}
	wg.Wait()
	close(versions)

	seen := map[uint64]struct{}{}
	for v := range versions {
		_, ok := seen[v]
		require.False(t, ok)
		seen[v] = struct{}{}
	}
	require.Equal(t, n, len(seen))
	for i := uint64(1); i <= uint64(n); i++ {
		_, ok := seen[i]
		require.True(t, ok)
	}
}

func TestNilMemoryModule(t *testing.T) {
	c := snapshot.NewCoordinator()
	mod := wazerotest.NewModule(nil)
	snap, err := c.CaptureSnapshot(mod)
	require.NoError(t, err)
	require.Equal(t, [][]byte{{}}, snap.Data())
	require.NoError(t, c.RestoreSnapshot(snap, mod))
}

func mustRead(t *testing.T, mod api.Module, offset uint32) byte {
	t.Helper()
	v, ok := mod.Memory().ReadByte(offset)
	require.True(t, ok)
	return v
}
