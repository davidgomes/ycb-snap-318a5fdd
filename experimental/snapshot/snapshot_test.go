package snapshot_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"strconv"
	"sync"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
	"github.com/tetratelabs/wazero/experimental/snapshot"
	"github.com/tetratelabs/wazero/experimental/wazerotest"
	"github.com/tetratelabs/wazero/internal/testing/require"
)

// memoryWasm is a module that exports one page of memory as "memory".
var memoryWasm = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x05, 0x03, 0x01, 0x00, 0x01,
	0x07, 0x0a, 0x01, 0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
}

func newModule(size int) *wazerotest.Module {
	return wazerotest.NewModule(wazerotest.NewMemory(size))
}

func gunzip(t *testing.T, b []byte) []byte {
	r, err := gzip.NewReader(bytes.NewReader(b))
	require.NoError(t, err)
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return out
}

func TestCoordinator_RealModules(t *testing.T) {
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)

	compiled, err := r.CompileModule(ctx, memoryWasm)
	require.NoError(t, err)
	var mods []api.Module
	for i := 0; i < 2; i++ {
		m, err := r.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName("m"+strconv.Itoa(i)))
		require.NoError(t, err)
		mods = append(mods, m)
	}

	c := experimental.NewSnapshotCoordinator()
	mods[0].Memory().WriteByte(10, 1)
	mods[1].Memory().WriteByte(20, 2)
	full, err := c.CaptureSnapshot(mods...)
	require.NoError(t, err)
	require.Equal(t, uint64(1), full.Version())

	mods[0].Memory().WriteByte(10, 3)
	mods[1].Memory().WriteByte(30, 4)
	inc, err := c.CaptureIncremental(full, mods...)
	require.NoError(t, err)
	require.Equal(t, uint64(2), inc.Version())
	require.True(t, len(inc.CompressedData()) < len(full.CompressedData()))
	require.Equal(t, []snapshot.DiffEntry{
		{Offset: 10, OldValue: 1, NewValue: 3},
		{Offset: 30, OldValue: 0, NewValue: 4},
	}, full.Compare(inc))

	require.NoError(t, c.RestoreSnapshot(full, mods...))
	b, _ := mods[0].Memory().ReadByte(10)
	require.Equal(t, byte(1), b)
	b, _ = mods[1].Memory().ReadByte(30)
	require.Equal(t, byte(0), b)

	require.NoError(t, mods[1].Close(ctx))
	_, err = c.CaptureSnapshot(mods...)
	errorContains(t, err, "module closed")
	require.Equal(t, snapshot.CodeModuleClosed, snapshot.ErrorCode(err))
}

func TestCaptureSnapshot(t *testing.T) {
	c := snapshot.NewCoordinator()
	a, b := newModule(1), newModule(2*wazerotest.PageSize)
	a.Memory().WriteByte(0, 0xaa)
	b.Memory().WriteByte(wazerotest.PageSize+1, 0xbb)

	snap, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)
	data := snap.Data()
	require.Equal(t, 2, len(data))
	require.Equal(t, wazerotest.PageSize, len(data[0]))
	require.Equal(t, 2*wazerotest.PageSize, len(data[1]))
	require.Equal(t, append(data[0], data[1]...), gunzip(t, snap.CompressedData()))

	// Captures are independent of later writes and of mutations to returned copies.
	a.Memory().WriteByte(0, 0x11)
	data[0][0] = 0x22
	require.Equal(t, byte(0xaa), snap.Data()[0][0])
	tags := snap.Tags()
	tags["x"] = "y"
	require.Equal(t, 0, len(snap.Tags()))
	snap.SetTag("k", "v")
	require.Equal(t, map[string]string{"k": "v"}, snap.Tags())

	s := snapshot.Summarize(snap)
	require.Equal(t, snapshot.SnapshotSummary{TotalModules: 2, TotalBytes: 3 * wazerotest.PageSize, Version: 1}, s)
}

func TestCaptureSnapshot_Errors(t *testing.T) {
	c := snapshot.NewCoordinator()
	_, err := c.CaptureSnapshot()
	errorContains(t, err, "no modules")

	_, err = c.CaptureSnapshot(newModule(1), nil)
	errorContains(t, err, "module closed")

	closed := newModule(1)
	require.NoError(t, closed.Close(context.Background()))
	_, err = c.CaptureSnapshot(closed)
	errorContains(t, err, "module closed")

	// Failed captures don't consume versions.
	snap, err := c.CaptureSnapshot(newModule(1))
	require.NoError(t, err)
	require.Equal(t, uint64(1), snap.Version())
}

func TestCaptureIncremental(t *testing.T) {
	c := snapshot.NewCoordinator()
	a, b := newModule(1), newModule(1)
	full, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)

	a.Memory().WriteByte(5, 1)
	inc1, err := c.CaptureIncremental(full, a, b)
	require.NoError(t, err)
	b.Memory().Write(100, []byte{1, 2, 3})
	inc2, err := c.CaptureIncremental(inc1, a, b)
	require.NoError(t, err)

	require.Equal(t, uint64(3), inc2.Version())
	require.True(t, len(inc1.CompressedData()) < len(full.CompressedData()))
	require.True(t, len(inc2.CompressedData()) < len(inc1.CompressedData()) ||
		len(inc2.CompressedData()) < len(full.CompressedData()))

	data := inc2.Data()
	require.Equal(t, byte(1), data[0][5])
	require.Equal(t, []byte{1, 2, 3}, data[1][100:103])
	require.Equal(t, 4, len(full.Compare(inc2)))
	require.Equal(t, []snapshot.DiffEntry{
		{Offset: 100, OldValue: 0, NewValue: 1},
		{Offset: 101, OldValue: 0, NewValue: 2},
		{Offset: 102, OldValue: 0, NewValue: 3},
	}, inc1.Compare(inc2))

	require.Equal(t, uint64(1), snapshot.Summarize(inc1).ModifiedBytes)
	require.Equal(t, uint64(3), snapshot.Summarize(inc2).ModifiedBytes)
	require.Equal(t, uint64(2*wazerotest.PageSize), snapshot.Summarize(inc2).TotalBytes)
}

func TestCaptureIncremental_Errors(t *testing.T) {
	c := snapshot.NewCoordinator()
	a := newModule(1)
	_, err := c.CaptureIncremental(nil, a)
	errorContains(t, err, "baseline snapshot is nil")

	full, err := c.CaptureSnapshot(a)
	require.NoError(t, err)
	_, err = c.CaptureIncremental(full, a, newModule(1))
	errorContains(t, err, "module count mismatch")
	_, err = c.CaptureIncremental(full)
	errorContains(t, err, "module count mismatch")
	_, err = c.CaptureIncremental(full, nil)
	errorContains(t, err, "module closed")
}

func TestRestoreSnapshot(t *testing.T) {
	c := snapshot.NewCoordinator()
	a, b := newModule(1), newModule(1)
	a.Memory().WriteByte(0, 'a')
	b.Memory().WriteByte(0, 'b')
	snap, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)

	reset := func() {
		a.Memory().WriteByte(0, 0)
		b.Memory().WriteByte(0, 0)
	}
	first := func(m api.Module) byte {
		v, _ := m.Memory().ReadByte(0)
		return v
	}

	t.Run("identity wins over position", func(t *testing.T) {
		reset()
		require.NoError(t, c.RestoreSnapshot(snap, b, a))
		require.Equal(t, byte('a'), first(a))
		require.Equal(t, byte('b'), first(b))
	})

	t.Run("positional fallback", func(t *testing.T) {
		x, y := newModule(1), newModule(1)
		require.NoError(t, c.RestoreSnapshot(snap, x, y))
		require.Equal(t, byte('a'), first(x))
		require.Equal(t, byte('b'), first(y))
	})

	t.Run("subset by identity", func(t *testing.T) {
		reset()
		require.NoError(t, c.RestoreSnapshot(snap, b))
		require.Equal(t, byte(0), first(a))
		require.Equal(t, byte('b'), first(b))

		x := newModule(1)
		require.NoError(t, c.RestoreSnapshot(snap, x))
		require.Equal(t, byte(0), first(x))
	})

	t.Run("zeroes memory beyond captured size", func(t *testing.T) {
		big := newModule(2 * wazerotest.PageSize)
		big.Memory().WriteByte(wazerotest.PageSize+5, 9)
		require.NoError(t, c.RestoreSnapshot(snap, big, newModule(1)))
		v, _ := big.Memory().ReadByte(wazerotest.PageSize + 5)
		require.Equal(t, byte(0), v)
	})

	t.Run("errors", func(t *testing.T) {
		err := c.RestoreSnapshot(snap, a, b, newModule(1))
		errorContains(t, err, "incompatible module")

		err = c.RestoreSnapshot(snap, newModule(1), wazerotest.NewModule(nil))
		require.Equal(t, "insufficient_memory", snapshot.ErrorCode(err))

		err = c.RestoreSnapshot(nil, a)
		require.Error(t, err)
	})
}

func TestCoordinator_Concurrent(t *testing.T) {
	c := snapshot.NewCoordinator()
	m := newModule(1)
	const n = 50
	versions := make(chan uint64, 2*n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			full, err := c.CaptureSnapshot(m)
			require.NoError(t, err)
			inc, err := c.CaptureIncremental(full, m)
			require.NoError(t, err)
			require.NoError(t, c.RestoreSnapshot(inc, m))
			versions <- full.Version()
			versions <- inc.Version()
		}()
	}
	wg.Wait()
	close(versions)
	seen := map[uint64]bool{}
	for v := range versions {
		require.False(t, seen[v])
		seen[v] = true
	}
	for v := uint64(1); v <= 2*n; v++ {
		require.True(t, seen[v])
	}
}

func TestRegistry(t *testing.T) {
	c1, c2 := snapshot.NewCoordinator(), snapshot.NewCoordinator()
	snapshot.Register("test", c1)
	snapshot.Register("test", c2)
	got, ok := snapshot.Get("test")
	require.True(t, ok)
	require.Equal(t, c2, got)
	snapshot.Unregister("test")
	_, ok = snapshot.Get("test")
	require.False(t, ok)

	ctx := context.Background()
	require.Nil(t, snapshot.GetCoordinator(ctx))
	require.Equal(t, c1, snapshot.GetCoordinator(snapshot.WithCoordinator(ctx, c1)))
}

func TestChain(t *testing.T) {
	ch := snapshot.NewChain()
	require.Equal(t, 0, ch.Len())
	require.Nil(t, ch.Head())

	c := snapshot.NewCoordinator()
	s1, _ := c.CaptureSnapshot(newModule(1))
	s2, _ := c.CaptureSnapshot(newModule(1))
	ch.Push(s1)
	ch.Push(s2)
	require.Equal(t, 2, ch.Len())
	require.Equal(t, s2, ch.Head())

	list := ch.Snapshots()
	require.Equal(t, []snapshot.Snapshot{s1, s2}, list)
	list[0] = nil
	require.Equal(t, s1, ch.Snapshots()[0])
}

func TestMarshalSnapshot(t *testing.T) {
	c := snapshot.NewCoordinator()
	a, b := newModule(1), newModule(1)
	full, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)
	b.Memory().WriteByte(7, 7)
	inc, err := c.CaptureIncremental(full, a, b)
	require.NoError(t, err)
	inc.SetTag("reason", "crash")

	encoded, err := snapshot.MarshalSnapshot(inc)
	require.NoError(t, err)
	decoded, err := snapshot.UnmarshalSnapshot(encoded)
	require.NoError(t, err)

	require.Equal(t, inc.Data(), decoded.Data())
	require.Equal(t, inc.Version(), decoded.Version())
	require.Equal(t, inc.Tags(), decoded.Tags())
	require.Equal(t, uint64(0), snapshot.Summarize(decoded).ModifiedBytes)
	require.Equal(t, bytes.Join(inc.Data(), nil), gunzip(t, decoded.CompressedData()))

	x, y := newModule(1), newModule(1)
	require.NoError(t, c.RestoreSnapshot(decoded, x, y))
	v, _ := y.Memory().ReadByte(7)
	require.Equal(t, byte(7), v)

	_, err = snapshot.MarshalSnapshot(nil)
	require.Error(t, err)
	_, err = snapshot.UnmarshalSnapshot(nil)
	require.Error(t, err)
	_, err = snapshot.UnmarshalSnapshot(encoded[:len(encoded)-1])
	require.Error(t, err)
	corrupt := append([]byte{}, encoded...)
	corrupt[20] ^= 0xff
	_, err = snapshot.UnmarshalSnapshot(corrupt)
	require.Error(t, err)
}

func errorContains(t *testing.T, err error, substr string) {
	t.Helper()
	require.Error(t, err)
	require.Contains(t, err.Error(), substr)
}
