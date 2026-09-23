package snapshot_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental/snapshot"
	"github.com/tetratelabs/wazero/experimental/wazerotest"
	"github.com/tetratelabs/wazero/internal/testing/require"
)

func moduleWith(mem []byte) *wazerotest.Module {
	if mem == nil {
		return wazerotest.NewModule(nil)
	}
	return wazerotest.NewModule(&wazerotest.Memory{Bytes: append([]byte(nil), mem...)})
}

func memBytes(m api.Module) []byte {
	mem := m.Memory()
	if mem == nil {
		return nil
	}
	buf, ok := mem.Read(0, mem.Size())
	if !ok {
		return nil
	}
	return append([]byte(nil), buf...)
}

func gzipOf(parts ...[]byte) []byte {
	var all []byte
	for _, p := range parts {
		all = append(all, p...)
	}
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(all)
	_ = w.Close()
	return buf.Bytes()
}

func gunzip(t *testing.T, compressed []byte) []byte {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(compressed))
	require.NoError(t, err)
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	return out
}

func TestCaptureSnapshotErrors(t *testing.T) {
	c := snapshot.NewCoordinator()
	_, err := c.CaptureSnapshot()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no modules")

	_, err = c.CaptureSnapshot(nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "module closed")

	var closed *wazerotest.Module
	_, err = c.CaptureSnapshot(closed)
	require.Error(t, err)
	require.Contains(t, err.Error(), "module closed")

	m := moduleWith([]byte{1, 2, 3})
	require.NoError(t, m.Close(context.Background()))
	_, err = c.CaptureSnapshot(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "module closed")

	// Failed captures do not consume a version.
	live := moduleWith([]byte{9})
	snap, err := c.CaptureSnapshot(live)
	require.NoError(t, err)
	require.Equal(t, uint64(1), snap.Version())
}

func TestCaptureAndRestore(t *testing.T) {
	c := snapshot.NewCoordinator()
	a := moduleWith([]byte{1, 2, 3, 4})
	b := moduleWith([]byte{5, 6})
	snap, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)
	require.Equal(t, uint64(1), snap.Version())

	got := snap.Data()
	require.Equal(t, []byte{1, 2, 3, 4}, got[0])
	require.Equal(t, []byte{5, 6}, got[1])

	// Data is a deep copy and does not alias module memory.
	got[0][0] = 0xFF
	require.Equal(t, byte(1), snap.Data()[0][0])
	b0, _ := a.Memory().ReadByte(0)
	require.Equal(t, byte(1), b0)

	require.Equal(t, gzipOf([]byte{1, 2, 3, 4}, []byte{5, 6}), snap.CompressedData())
	require.Equal(t, gunzip(t, snap.CompressedData()), []byte{1, 2, 3, 4, 5, 6})

	// Mutating module memory after capture does not change the snapshot.
	require.True(t, a.Memory().WriteByte(0, 9))
	require.Equal(t, byte(1), snap.Data()[0][0])

	// Identity restore, including a swapped argument order.
	require.NoError(t, c.RestoreSnapshot(snap, b, a))
	require.Equal(t, []byte{1, 2, 3, 4}, memBytes(a))
	require.Equal(t, []byte{5, 6}, memBytes(b))

	// Positional restore into different modules of the same count.
	a2 := moduleWith([]byte{0, 0, 0, 0})
	b2 := moduleWith([]byte{0, 0})
	require.NoError(t, c.RestoreSnapshot(snap, a2, b2))
	require.Equal(t, []byte{1, 2, 3, 4}, memBytes(a2))
	require.Equal(t, []byte{5, 6}, memBytes(b2))

	// Fewer modules match by identity only. Unknown modules are skipped.
	unknown := moduleWith([]byte{7, 7})
	require.True(t, a.Memory().WriteByte(1, 8))
	require.NoError(t, c.RestoreSnapshot(snap, unknown))
	require.Equal(t, []byte{7, 7}, memBytes(unknown))
	require.Equal(t, byte(8), func() byte { v, _ := a.Memory().ReadByte(1); return v }())

	require.NoError(t, c.RestoreSnapshot(snap, b))
	require.Equal(t, []byte{5, 6}, memBytes(b))
	// a was not included and stays dirty.
	v, _ := a.Memory().ReadByte(1)
	require.Equal(t, byte(8), v)

	// No modules at all is a successful no-op.
	require.NoError(t, c.RestoreSnapshot(snap))

	extra := moduleWith([]byte{1})
	err = c.RestoreSnapshot(snap, a, b, extra)
	require.Error(t, err)
	require.Contains(t, err.Error(), "incompatible module")

	small := moduleWith([]byte{0, 0})
	err = c.RestoreSnapshot(snap, small, b)
	require.Error(t, err)
	require.Equal(t, "insufficient_memory", snapshot.ErrorCode(err))
	// Failure is atomic: b is unchanged by the failed restore.
	require.Equal(t, []byte{5, 6}, memBytes(b))

	err = c.RestoreSnapshot(snap, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "module closed")
}

func TestRestoreLargerTargetKeepsTail(t *testing.T) {
	c := snapshot.NewCoordinator()
	src := moduleWith([]byte{1, 2, 3, 4})
	snap, err := c.CaptureSnapshot(src)
	require.NoError(t, err)
	dst := moduleWith([]byte{9, 9, 9, 9, 9, 9})
	require.NoError(t, c.RestoreSnapshot(snap, dst))
	require.Equal(t, []byte{1, 2, 3, 4, 9, 9}, memBytes(dst))
}

func TestIncremental(t *testing.T) {
	c := snapshot.NewCoordinator()
	buf := make([]byte, 4096)
	for i := range buf {
		buf[i] = byte(i*31 + 7)
	}
	m := moduleWith(buf)
	full, err := c.CaptureSnapshot(m)
	require.NoError(t, err)

	require.True(t, m.Memory().WriteByte(10, buf[10]+1))
	inc, err := c.CaptureIncremental(full, m)
	require.NoError(t, err)
	require.Equal(t, uint64(2), inc.Version())
	require.True(t, len(inc.CompressedData()) < len(full.CompressedData()))
	_ = gunzip(t, inc.CompressedData())

	got := inc.Data()[0]
	require.Equal(t, len(buf), len(got))
	require.Equal(t, byte(buf[10]+1), got[10])
	require.Equal(t, buf[11], got[11])

	sum := snapshot.Summarize(inc)
	require.Equal(t, 1, sum.TotalModules)
	require.Equal(t, uint64(len(buf)), sum.TotalBytes)
	require.Equal(t, uint64(1), sum.ModifiedBytes)
	require.Equal(t, inc.Version(), sum.Version)
	require.Equal(t, uint64(0), snapshot.Summarize(full).ModifiedBytes)

	// Chained incremental reconstructs the latest memory and shrinks again.
	require.True(t, m.Memory().WriteByte(20, buf[20]+3))
	inc2, err := c.CaptureIncremental(inc, m)
	require.NoError(t, err)
	require.Equal(t, uint64(3), inc2.Version())
	require.True(t, len(inc2.CompressedData()) < len(inc.CompressedData()))
	want := append([]byte(nil), buf...)
	want[10] = buf[10] + 1
	want[20] = buf[20] + 3
	require.Equal(t, want, inc2.Data()[0])
	require.Equal(t, uint64(1), snapshot.Summarize(inc2).ModifiedBytes)

	// Another full capture continues the version sequence.
	full2, err := c.CaptureSnapshot(m)
	require.NoError(t, err)
	require.Equal(t, uint64(4), full2.Version())

	_, err = c.CaptureIncremental(nil, m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "baseline snapshot is nil")

	other := moduleWith([]byte{1})
	_, err = c.CaptureIncremental(full, m, other)
	require.Error(t, err)
	require.Contains(t, err.Error(), "module count mismatch")

	// Failed incremental does not consume a version.
	next, err := c.CaptureSnapshot(m)
	require.NoError(t, err)
	require.Equal(t, uint64(5), next.Version())
}

func TestCompare(t *testing.T) {
	c := snapshot.NewCoordinator()
	m0 := moduleWith([]byte{0, 0, 0, 0})
	m1 := moduleWith([]byte{5, 5})
	before, err := c.CaptureSnapshot(m0, m1)
	require.NoError(t, err)
	require.True(t, m0.Memory().WriteByte(3, 9))
	require.True(t, m0.Memory().WriteByte(1, 2))
	require.True(t, m1.Memory().WriteByte(0, 8))
	after, err := c.CaptureSnapshot(m0, m1)
	require.NoError(t, err)

	diffs := before.Compare(after)
	require.Equal(t, []snapshot.DiffEntry{
		{Offset: 1, OldValue: 0, NewValue: 2},
		{Offset: 3, OldValue: 0, NewValue: 9},
		{Offset: 0, OldValue: 5, NewValue: 8},
	}, diffs)
	require.Equal(t, 0, len(after.Compare(after)))
}

func TestTagsCopies(t *testing.T) {
	c := snapshot.NewCoordinator()
	m := moduleWith([]byte{1})
	snap, err := c.CaptureSnapshot(m)
	require.NoError(t, err)
	require.Equal(t, 0, len(snap.Tags()))

	snap.SetTag("region", "heap")
	snap.SetTag("gen", "a")
	tags := snap.Tags()
	tags["region"] = "mutated"
	tags["extra"] = "nope"
	require.Equal(t, "heap", snap.Tags()["region"])
	_, ok := snap.Tags()["extra"]
	require.False(t, ok)

	again := snap.Tags()
	require.Equal(t, "heap", again["region"])
	require.Equal(t, "a", again["gen"])
}

func TestRegistryAndContext(t *testing.T) {
	c1 := snapshot.NewCoordinator()
	c2 := snapshot.NewCoordinator()
	name := "test-registry-coord"
	t.Cleanup(func() { snapshot.Unregister(name) })

	snapshot.Register(name, c1)
	got, ok := snapshot.Get(name)
	require.True(t, ok)
	require.Same(t, c1, got)

	snapshot.Register(name, c2)
	got, ok = snapshot.Get(name)
	require.True(t, ok)
	require.Same(t, c2, got)

	snapshot.Unregister(name)
	_, ok = snapshot.Get(name)
	require.False(t, ok)
	snapshot.Unregister(name)

	ctx := context.Background()
	require.Nil(t, snapshot.GetCoordinator(ctx))
	require.Equal(t, ctx, snapshot.WithCoordinator(ctx, nil))
	ctx = snapshot.WithCoordinator(ctx, c1)
	require.Equal(t, c1, snapshot.GetCoordinator(ctx))
}

func TestChain(t *testing.T) {
	ch := snapshot.NewChain()
	require.Equal(t, 0, ch.Len())
	require.Nil(t, ch.Head())
	require.Equal(t, 0, len(ch.Snapshots()))

	c := snapshot.NewCoordinator()
	m := moduleWith([]byte{1, 2})
	s1, err := c.CaptureSnapshot(m)
	require.NoError(t, err)
	require.True(t, m.Memory().WriteByte(0, 3))
	s2, err := c.CaptureSnapshot(m)
	require.NoError(t, err)
	ch.Push(s1)
	ch.Push(s2)
	require.Equal(t, 2, ch.Len())
	require.Same(t, s2, ch.Head())
	snaps := ch.Snapshots()
	require.Equal(t, 2, len(snaps))
	require.Same(t, s1, snaps[0])
	require.Same(t, s2, snaps[1])
	snaps[0] = nil
	require.Equal(t, s1, ch.Snapshots()[0])
}

func TestMarshalRoundTrip(t *testing.T) {
	c := snapshot.NewCoordinator()
	m0 := moduleWith([]byte{1, 2, 3})
	m1 := moduleWith([]byte{4, 5})
	snap, err := c.CaptureSnapshot(m0, m1)
	require.NoError(t, err)
	snap.SetTag("b", "2")
	snap.SetTag("a", "1")

	blob, err := snapshot.MarshalSnapshot(snap)
	require.NoError(t, err)
	decoded, err := snapshot.UnmarshalSnapshot(blob)
	require.NoError(t, err)
	require.Equal(t, snap.Version(), decoded.Version())
	require.Equal(t, snap.Data(), decoded.Data())
	require.Equal(t, "1", decoded.Tags()["a"])
	require.Equal(t, "2", decoded.Tags()["b"])
	require.Equal(t, 0, len(snap.Compare(decoded)))
	require.Equal(t, uint64(0), snapshot.Summarize(decoded).ModifiedBytes)
	require.Equal(t, gzipOf([]byte{1, 2, 3}, []byte{4, 5}), decoded.CompressedData())

	// Decoded snapshots have no capture identity, so restore is positional.
	dst0 := moduleWith([]byte{0, 0, 0})
	dst1 := moduleWith([]byte{0, 0})
	require.NoError(t, c.RestoreSnapshot(decoded, dst0, dst1))
	require.Equal(t, []byte{1, 2, 3}, memBytes(dst0))
	require.Equal(t, []byte{4, 5}, memBytes(dst1))

	// Incremental marshal comes back as a full snapshot of reconstructed memory.
	require.True(t, m0.Memory().WriteByte(0, 9))
	inc, err := c.CaptureIncremental(snap, m0, m1)
	require.NoError(t, err)
	blob, err = snapshot.MarshalSnapshot(inc)
	require.NoError(t, err)
	decoded, err = snapshot.UnmarshalSnapshot(blob)
	require.NoError(t, err)
	require.Equal(t, byte(9), decoded.Data()[0][0])
	require.Equal(t, uint64(0), snapshot.Summarize(decoded).ModifiedBytes)
	require.Equal(t, gzipOf(decoded.Data()[0], decoded.Data()[1]), decoded.CompressedData())

	_, err = snapshot.MarshalSnapshot(nil)
	require.Error(t, err)
	_, err = snapshot.UnmarshalSnapshot(nil)
	require.Error(t, err)
	_, err = snapshot.UnmarshalSnapshot([]byte("nope"))
	require.Error(t, err)
	_, err = snapshot.UnmarshalSnapshot(blob[:len(blob)-1])
	require.Error(t, err)
	_, err = snapshot.UnmarshalSnapshot(append(append([]byte{}, blob...), 0xFF))
	require.Error(t, err)
}

func TestConcurrentCoordinatorAndRegistry(t *testing.T) {
	c := snapshot.NewCoordinator()
	mod := moduleWith(bytes.Repeat([]byte{1}, 64))
	const n = 32
	var wg sync.WaitGroup
	versions := make([]uint64, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := c.CaptureSnapshot(mod)
			if err != nil {
				t.Errorf("capture: %v", err)
				return
			}
			_ = s.Data()
			_ = s.CompressedData()
			versions[i] = s.Version()
		}(i)
	}
	wg.Wait()
	seen := map[uint64]bool{}
	for _, v := range versions {
		if v == 0 || seen[v] {
			t.Fatalf("version %d", v)
		}
		seen[v] = true
	}
	for i := uint64(1); i <= n; i++ {
		require.True(t, seen[i])
	}

	snap := func() snapshot.Snapshot {
		s, err := c.CaptureSnapshot(mod)
		require.NoError(t, err)
		return s
	}()
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			snap.SetTag(fmt.Sprintf("k%d", i), "v")
			_ = snap.Tags()
		}(i)
	}
	wg.Wait()
	require.Equal(t, 20, len(snap.Tags()))

	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("conc-%d", i)
			coord := snapshot.NewCoordinator()
			snapshot.Register(name, coord)
			got, ok := snapshot.Get(name)
			if !ok || got != coord {
				t.Errorf("registry %s", name)
			}
			snapshot.Unregister(name)
		}(i)
	}
	wg.Wait()
}

func TestNilMemoryAndClosedIncremental(t *testing.T) {
	c := snapshot.NewCoordinator()
	empty := moduleWith(nil)
	snap, err := c.CaptureSnapshot(empty)
	require.NoError(t, err)
	require.Equal(t, 1, len(snap.Data()))
	require.Equal(t, 0, len(snap.Data()[0]))

	withMem := moduleWith([]byte{1, 2, 3, 4})
	full, err := c.CaptureSnapshot(withMem)
	require.NoError(t, err)
	err = c.RestoreSnapshot(full, empty)
	require.Error(t, err)
	require.Equal(t, "insufficient_memory", snapshot.ErrorCode(err))
	require.Equal(t, "", snapshot.ErrorCode(errNoModules()))

	require.NoError(t, withMem.Close(context.Background()))
	_, err = c.CaptureIncremental(full, withMem)
	require.Error(t, err)
	require.Contains(t, err.Error(), "module closed")
}

func errNoModules() error {
	c := snapshot.NewCoordinator()
	_, err := c.CaptureSnapshot()
	return err
}

func TestIncompressibleIncrementalStillSmaller(t *testing.T) {
	c := snapshot.NewCoordinator()
	// Zero-filled memory gzips smaller than a sparse delta. The incremental
	// result must still be strictly smaller than the baseline.
	m := moduleWith(make([]byte, 64))
	full, err := c.CaptureSnapshot(m)
	require.NoError(t, err)
	for i := 0; i < 64; i++ {
		require.True(t, m.Memory().WriteByte(uint32(i), 0xFF))
	}
	inc, err := c.CaptureIncremental(full, m)
	require.NoError(t, err)
	require.True(t, len(inc.CompressedData()) < len(full.CompressedData()))
	require.Equal(t, bytes.Repeat([]byte{0xFF}, 64), inc.Data()[0])
	require.Equal(t, uint64(64), snapshot.Summarize(inc).ModifiedBytes)
	_ = gunzip(t, inc.CompressedData())
}
