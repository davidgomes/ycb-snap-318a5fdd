package snapshot_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"sort"
	"sync"
	"testing"

	"github.com/tetratelabs/wazero/experimental/snapshot"
	"github.com/tetratelabs/wazero/experimental/wazerotest"
	"github.com/tetratelabs/wazero/internal/testing/require"
)

func newMemModule(n int) (*wazerotest.Module, *wazerotest.Memory) {
	mem := &wazerotest.Memory{Bytes: make([]byte, n)}
	return &wazerotest.Module{ExportMemory: mem}, mem
}

func gunzip(t *testing.T, data []byte) []byte {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return out
}

func concat(parts [][]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestCaptureSnapshotErrors(t *testing.T) {
	c := snapshot.NewCoordinator()
	_, err := c.CaptureSnapshot()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no modules")
	require.Equal(t, "", snapshot.ErrorCode(err))

	_, err = c.CaptureSnapshot(nil)
	require.Contains(t, err.Error(), "module closed")

	var typedNil *wazerotest.Module
	_, err = c.CaptureSnapshot(typedNil)
	require.Contains(t, err.Error(), "module closed")

	mod, _ := newMemModule(4)
	require.NoError(t, mod.Close(context.Background()))
	_, err = c.CaptureSnapshot(mod)
	require.Contains(t, err.Error(), "module closed")

	live, _ := newMemModule(4)
	_, err = c.CaptureSnapshot(live, nil)
	require.Contains(t, err.Error(), "module closed")

	snap, err := c.CaptureSnapshot(live)
	require.NoError(t, err)
	require.Equal(t, uint64(1), snap.Version())
}

func TestCaptureAndRestore(t *testing.T) {
	c := snapshot.NewCoordinator()
	a, am := newMemModule(4)
	b, bm := newMemModule(4)
	copy(am.Bytes, []byte{1, 2, 3, 4})
	copy(bm.Bytes, []byte{5, 6, 7, 8})

	snap, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)
	require.Equal(t, uint64(1), snap.Version())

	data := snap.Data()
	require.Equal(t, []byte{1, 2, 3, 4}, data[0])
	require.Equal(t, []byte{5, 6, 7, 8}, data[1])
	data[0][0] = 0xFF
	require.Equal(t, byte(1), snap.Data()[0][0])
	require.Equal(t, byte(1), am.Bytes[0])

	plain := gunzip(t, snap.CompressedData())
	require.Equal(t, concat(snap.Data()), plain)
	// Compressed bytes are stable.
	require.Equal(t, snap.CompressedData(), snap.CompressedData())

	copy(am.Bytes, []byte{9, 9, 9, 9})
	copy(bm.Bytes, []byte{8, 8, 8, 8})

	// Identity match in reverse order.
	require.NoError(t, c.RestoreSnapshot(snap, b, a))
	require.Equal(t, []byte{1, 2, 3, 4}, am.Bytes)
	require.Equal(t, []byte{5, 6, 7, 8}, bm.Bytes)

	copy(am.Bytes, []byte{9, 9, 9, 9})
	copy(bm.Bytes, []byte{8, 8, 8, 8})
	cOnly, cm := newMemModule(4)
	// Fewer modules: identity only. cOnly does not match.
	require.NoError(t, c.RestoreSnapshot(snap, cOnly))
	require.Equal(t, []byte{0, 0, 0, 0}, cm.Bytes)
	require.Equal(t, []byte{9, 9, 9, 9}, am.Bytes)

	// Fewer modules: only B is restored.
	require.NoError(t, c.RestoreSnapshot(snap, b))
	require.Equal(t, []byte{9, 9, 9, 9}, am.Bytes)
	require.Equal(t, []byte{5, 6, 7, 8}, bm.Bytes)

	// No modules is a complete miss.
	copy(bm.Bytes, []byte{8, 8, 8, 8})
	require.NoError(t, c.RestoreSnapshot(snap))
	require.Equal(t, []byte{8, 8, 8, 8}, bm.Bytes)

	// Positional fallback when the instances are new.
	p1, p1m := newMemModule(4)
	p2, p2m := newMemModule(4)
	require.NoError(t, c.RestoreSnapshot(snap, p1, p2))
	require.Equal(t, []byte{1, 2, 3, 4}, p1m.Bytes)
	require.Equal(t, []byte{5, 6, 7, 8}, p2m.Bytes)

	// Mixed identity: not every module matches, so the whole restore is positional.
	copy(am.Bytes, []byte{9, 9, 9, 9})
	copy(bm.Bytes, []byte{8, 8, 8, 8})
	other, om := newMemModule(4)
	require.NoError(t, c.RestoreSnapshot(snap, a, other))
	require.Equal(t, []byte{1, 2, 3, 4}, am.Bytes)
	require.Equal(t, []byte{5, 6, 7, 8}, om.Bytes)
	require.Equal(t, []byte{8, 8, 8, 8}, bm.Bytes)
}

func TestRestoreErrors(t *testing.T) {
	c := snapshot.NewCoordinator()
	a, am := newMemModule(4)
	b, _ := newMemModule(4)
	copy(am.Bytes, []byte{1, 2, 3, 4})
	snap, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)

	copy(am.Bytes, []byte{7, 7, 7, 7})
	err = c.RestoreSnapshot(snap, a, b, a)
	require.Contains(t, err.Error(), "incompatible module")
	require.Equal(t, []byte{7, 7, 7, 7}, am.Bytes)

	small, sm := newMemModule(2)
	copy(sm.Bytes, []byte{3, 3})
	wide, wm := newMemModule(4)
	copy(wm.Bytes, []byte{6, 6, 6, 6})
	err = c.RestoreSnapshot(snap, wide, small)
	require.Equal(t, "insufficient_memory", snapshot.ErrorCode(err))
	require.Contains(t, err.Error(), "insufficient_memory")
	require.Equal(t, "insufficient_memory", snapshot.ErrorCode(fmt.Errorf("wrap: %w", err)))
	// Neither target is written when a later module cannot hold the image.
	require.Equal(t, []byte{6, 6, 6, 6}, wm.Bytes)
	require.Equal(t, []byte{3, 3}, sm.Bytes)

	err = c.RestoreSnapshot(snap, nil, b)
	require.Contains(t, err.Error(), "module closed")

	require.NoError(t, a.Close(context.Background()))
	err = c.RestoreSnapshot(snap, a, b)
	require.Contains(t, err.Error(), "module closed")

	empty, _ := newMemModule(0)
	full, _ := newMemModule(4)
	copy(full.Memory().(*wazerotest.Memory).Bytes, []byte{1, 1, 1, 1})
	one, err := c.CaptureSnapshot(full)
	require.NoError(t, err)
	err = c.RestoreSnapshot(one, empty)
	require.Equal(t, "insufficient_memory", snapshot.ErrorCode(err))

	noMem := &wazerotest.Module{}
	err = c.RestoreSnapshot(one, noMem)
	require.Equal(t, "insufficient_memory", snapshot.ErrorCode(err))
}

func TestNilMemoryCapture(t *testing.T) {
	c := snapshot.NewCoordinator()
	mod := &wazerotest.Module{}
	snap, err := c.CaptureSnapshot(mod)
	require.NoError(t, err)
	data := snap.Data()
	require.Equal(t, 1, len(data))
	require.Equal(t, 0, len(data[0]))

	other, om := newMemModule(4)
	copy(om.Bytes, []byte{4, 4, 4, 4})
	require.NoError(t, c.RestoreSnapshot(snap, other))
	require.Equal(t, []byte{4, 4, 4, 4}, om.Bytes)
}

func TestIncremental(t *testing.T) {
	c := snapshot.NewCoordinator()
	mod, mem := newMemModule(8)
	copy(mem.Bytes, []byte{1, 2, 3, 4, 5, 6, 7, 8})
	base, err := c.CaptureSnapshot(mod)
	require.NoError(t, err)

	_, err = c.CaptureIncremental(nil, mod)
	require.Contains(t, err.Error(), "baseline snapshot is nil")
	_, err = c.CaptureIncremental(base)
	require.Contains(t, err.Error(), "module count mismatch")
	_, err = c.CaptureIncremental(base, mod, mod)
	require.Contains(t, err.Error(), "module count mismatch")
	_, err = c.CaptureIncremental(base, nil)
	require.Contains(t, err.Error(), "module closed")

	mem.Bytes[1] = 0xAB
	mem.Bytes[6] = 0xCD
	inc, err := c.CaptureIncremental(base, mod)
	require.NoError(t, err)
	require.Equal(t, uint64(2), inc.Version())
	require.Equal(t, []byte{1, 0xAB, 3, 4, 5, 6, 0xCD, 8}, inc.Data()[0])

	sum := snapshot.Summarize(inc)
	require.Equal(t, 1, sum.TotalModules)
	require.Equal(t, uint64(8), sum.TotalBytes)
	require.Equal(t, uint64(2), sum.ModifiedBytes)
	require.Equal(t, inc.Version(), sum.Version)

	baseSum := snapshot.Summarize(base)
	require.Equal(t, uint64(0), baseSum.ModifiedBytes)
	require.Equal(t, uint64(8), baseSum.TotalBytes)

	diff := base.Compare(inc)
	require.Equal(t, []snapshot.DiffEntry{
		{Offset: 1, OldValue: 2, NewValue: 0xAB},
		{Offset: 6, OldValue: 7, NewValue: 0xCD},
	}, diff)
	require.Equal(t, 0, len(inc.Compare(inc)))

	// A second incremental is relative to the reconstructed baseline, not the root.
	mem.Bytes[6] = 0xEE
	inc2, err := c.CaptureIncremental(inc, mod)
	require.NoError(t, err)
	require.Equal(t, uint64(3), inc2.Version())
	require.Equal(t, uint64(1), snapshot.Summarize(inc2).ModifiedBytes)
	require.Equal(t, []byte{1, 0xAB, 3, 4, 5, 6, 0xEE, 8}, inc2.Data()[0])

	mem.Bytes[0] = 0
	require.NoError(t, c.RestoreSnapshot(inc2, mod))
	require.Equal(t, []byte{1, 0xAB, 3, 4, 5, 6, 0xEE, 8}, mem.Bytes)

	again, err := c.CaptureSnapshot(mod)
	require.NoError(t, err)
	require.Equal(t, uint64(4), again.Version())
}

func TestIncrementalCompression(t *testing.T) {
	c := snapshot.NewCoordinator()
	mod := wazerotest.NewModule(wazerotest.NewMemory(1))
	mem := mod.Memory().(*wazerotest.Memory)
	base, err := c.CaptureSnapshot(mod)
	require.NoError(t, err)

	mem.Bytes[50] = 0x11
	mem.Bytes[1000] = 0x22
	inc, err := c.CaptureIncremental(base, mod)
	require.NoError(t, err)
	require.True(t, len(inc.CompressedData()) < len(base.CompressedData()))
	plain := gunzip(t, inc.CompressedData())
	require.True(t, bytes.Contains(plain, []byte{0x11}) || bytes.Contains(plain, []byte{0x22}))

	mem.Bytes[2000] = 0x33
	inc2, err := c.CaptureIncremental(inc, mod)
	require.NoError(t, err)
	require.True(t, len(inc2.CompressedData()) < len(inc.CompressedData()))
	_ = gunzip(t, inc2.CompressedData())
	require.Equal(t, mem.Bytes, inc2.Data()[0])

	mem.Bytes[3000] = 0x44
	inc3, err := c.CaptureIncremental(inc2, mod)
	require.NoError(t, err)
	require.True(t, len(inc3.CompressedData()) < len(inc2.CompressedData()))
	_ = gunzip(t, inc3.CompressedData())

	diff := base.Compare(inc)
	require.Equal(t, []snapshot.DiffEntry{
		{Offset: 50, OldValue: 0, NewValue: 0x11},
		{Offset: 1000, OldValue: 0, NewValue: 0x22},
	}, diff)
}

func TestMultiModuleCompare(t *testing.T) {
	c := snapshot.NewCoordinator()
	a, am := newMemModule(4)
	b, bm := newMemModule(4)
	before, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)
	am.Bytes[2] = 9
	bm.Bytes[0] = 4
	bm.Bytes[3] = 1
	after, err := c.CaptureIncremental(before, a, b)
	require.NoError(t, err)
	require.Equal(t, []snapshot.DiffEntry{
		{Offset: 2, OldValue: 0, NewValue: 9},
		{Offset: 0, OldValue: 0, NewValue: 4},
		{Offset: 3, OldValue: 0, NewValue: 1},
	}, before.Compare(after))
	require.Equal(t, uint64(3), snapshot.Summarize(after).ModifiedBytes)
	require.Equal(t, 2, snapshot.Summarize(after).TotalModules)
}

func TestGrowthAndShrink(t *testing.T) {
	c := snapshot.NewCoordinator()
	mod, mem := newMemModule(4)
	copy(mem.Bytes, []byte{1, 2, 3, 4})
	base, err := c.CaptureSnapshot(mod)
	require.NoError(t, err)
	mem.Bytes = append(mem.Bytes, 0xAB)
	inc, err := c.CaptureIncremental(base, mod)
	require.NoError(t, err)
	require.Equal(t, uint64(1), snapshot.Summarize(inc).ModifiedBytes)
	require.Equal(t, snapshot.DiffEntry{Offset: 4, OldValue: 0, NewValue: 0xAB}, base.Compare(inc)[0])

	mem.Bytes = mem.Bytes[:3]
	shrunk, err := c.CaptureIncremental(inc, mod)
	require.NoError(t, err)
	// The dropped tail is two bytes (0x04 was already replaced by growth... inc image is 5 bytes).
	require.Equal(t, uint64(2), snapshot.Summarize(shrunk).ModifiedBytes)
	require.Equal(t, 3, len(shrunk.Data()[0]))
}

func TestTagsImmutableCopies(t *testing.T) {
	c := snapshot.NewCoordinator()
	mod, _ := newMemModule(2)
	snap, err := c.CaptureSnapshot(mod)
	require.NoError(t, err)
	snap.SetTag("region", "heap")
	snap.SetTag("region", "stack")
	snap.SetTag("gen", "1")
	first := snap.Tags()
	first["region"] = "mutated"
	first["extra"] = "nope"
	got := snap.Tags()
	require.Equal(t, "stack", got["region"])
	require.Equal(t, "1", got["gen"])
	_, ok := got["extra"]
	require.False(t, ok)
}

func TestVersionsAcrossCoordinators(t *testing.T) {
	mod, _ := newMemModule(2)
	c1 := snapshot.NewCoordinator()
	c2 := snapshot.NewCoordinator()
	s1, err := c1.CaptureSnapshot(mod)
	require.NoError(t, err)
	s2, err := c2.CaptureSnapshot(mod)
	require.NoError(t, err)
	require.Equal(t, uint64(1), s1.Version())
	require.Equal(t, uint64(1), s2.Version())
}

func TestConcurrentCoordinator(t *testing.T) {
	c := snapshot.NewCoordinator()
	mod, _ := newMemModule(8)
	base, err := c.CaptureSnapshot(mod)
	require.NoError(t, err)

	const n = 16
	versions := make([]uint64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var snap snapshot.Snapshot
			var err error
			if i%2 == 0 {
				snap, err = c.CaptureSnapshot(mod)
			} else {
				snap, err = c.CaptureIncremental(base, mod)
			}
			if err != nil {
				t.Error(err)
				return
			}
			versions[i] = snap.Version()
		}(i)
	}
	wg.Wait()
	got := append([]uint64(nil), versions...)
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	for i, v := range got {
		require.Equal(t, uint64(i+2), v)
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	c := snapshot.NewCoordinator()
	a, am := newMemModule(4)
	b, bm := newMemModule(3)
	copy(am.Bytes, []byte{1, 2, 3, 4})
	copy(bm.Bytes, []byte{5, 6, 7})
	base, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)
	am.Bytes[0] = 9
	inc, err := c.CaptureIncremental(base, a, b)
	require.NoError(t, err)
	inc.SetTag("z", "last")
	inc.SetTag("a", "first")

	_, err = snapshot.MarshalSnapshot(nil)
	require.Error(t, err)
	_, err = snapshot.UnmarshalSnapshot(nil)
	require.Error(t, err)
	_, err = snapshot.UnmarshalSnapshot([]byte("nope"))
	require.Error(t, err)

	blob, err := snapshot.MarshalSnapshot(inc)
	require.NoError(t, err)
	blob = append(blob, 0xFF)
	_, err = snapshot.UnmarshalSnapshot(blob)
	require.Error(t, err)
	blob = blob[:len(blob)-1]

	decoded, err := snapshot.UnmarshalSnapshot(blob)
	require.NoError(t, err)
	require.Equal(t, inc.Version(), decoded.Version())
	require.Equal(t, inc.Data(), decoded.Data())
	require.Equal(t, inc.Tags(), decoded.Tags())
	require.Equal(t, uint64(0), snapshot.Summarize(decoded).ModifiedBytes)
	require.Equal(t, concat(decoded.Data()), gunzip(t, decoded.CompressedData()))
	require.Equal(t, 0, len(inc.Compare(decoded)))

	// Unmarshal drops module identity, so an equal-count restore is positional.
	copy(am.Bytes, []byte{0, 0, 0, 0})
	copy(bm.Bytes, []byte{0, 0, 0})
	require.NoError(t, c.RestoreSnapshot(decoded, a, b))
	require.Equal(t, []byte{9, 2, 3, 4}, am.Bytes)
	require.Equal(t, []byte{5, 6, 7}, bm.Bytes)

	decoded.SetTag("a", "changed")
	require.Equal(t, "changed", decoded.Tags()["a"])
	require.Equal(t, "first", inc.Tags()["a"])

	fresh := snapshot.NewCoordinator()
	next, err := fresh.CaptureIncremental(decoded, a, b)
	require.NoError(t, err)
	require.Equal(t, uint64(1), next.Version())
}

func TestChain(t *testing.T) {
	ch := snapshot.NewChain()
	require.Equal(t, 0, ch.Len())
	require.Nil(t, ch.Head())
	require.Equal(t, 0, len(ch.Snapshots()))

	c := snapshot.NewCoordinator()
	mod, _ := newMemModule(1)
	s1, err := c.CaptureSnapshot(mod)
	require.NoError(t, err)
	s2, err := c.CaptureIncremental(s1, mod)
	require.NoError(t, err)
	ch.Push(s1)
	ch.Push(s2)
	require.Equal(t, 2, ch.Len())
	require.Equal(t, s2.Version(), ch.Head().Version())
	got := ch.Snapshots()
	require.Equal(t, s1.Version(), got[0].Version())
	require.Equal(t, s2.Version(), got[1].Version())
	got[0] = nil
	require.Equal(t, s1.Version(), ch.Snapshots()[0].Version())

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch.Push(s1)
			_ = ch.Head()
			_ = ch.Len()
			_ = ch.Snapshots()
		}()
	}
	wg.Wait()
	require.Equal(t, 10, ch.Len())
}

func TestRegistryAndContext(t *testing.T) {
	name := "snapshot-registry-test"
	t.Cleanup(func() { snapshot.Unregister(name) })

	_, ok := snapshot.Get(name)
	require.False(t, ok)

	c1 := snapshot.NewCoordinator()
	c2 := snapshot.NewCoordinator()
	snapshot.Register(name, c1)
	got, ok := snapshot.Get(name)
	require.True(t, ok)
	require.Equal(t, c1, got)
	snapshot.Register(name, c2)
	got, ok = snapshot.Get(name)
	require.True(t, ok)
	require.Equal(t, c2, got)
	snapshot.Unregister(name)
	_, ok = snapshot.Get(name)
	require.False(t, ok)
	snapshot.Unregister(name)

	require.Nil(t, snapshot.GetCoordinator(context.Background()))
	ctx := snapshot.WithCoordinator(context.Background(), c1)
	require.Equal(t, c1, snapshot.GetCoordinator(ctx))

	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			n := fmt.Sprintf("reg-%d", i)
			c := snapshot.NewCoordinator()
			snapshot.Register(n, c)
			got, ok := snapshot.Get(n)
			if !ok || got != c {
				t.Errorf("registry get %s", n)
			}
			snapshot.Unregister(n)
		}(i)
	}
	wg.Wait()
}

func TestClosedModuleDoesNotConsumeVersion(t *testing.T) {
	c := snapshot.NewCoordinator()
	_, err := c.CaptureSnapshot()
	require.Error(t, err)
	mod, _ := newMemModule(1)
	require.NoError(t, mod.Close(context.Background()))
	_, err = c.CaptureSnapshot(mod)
	require.Error(t, err)
	live, _ := newMemModule(1)
	snap, err := c.CaptureSnapshot(live)
	require.NoError(t, err)
	require.Equal(t, uint64(1), snap.Version())
}

func TestLargerTargetKeepsTail(t *testing.T) {
	c := snapshot.NewCoordinator()
	src, sm := newMemModule(2)
	copy(sm.Bytes, []byte{1, 2})
	snap, err := c.CaptureSnapshot(src)
	require.NoError(t, err)
	dst, dm := newMemModule(4)
	copy(dm.Bytes, []byte{9, 9, 9, 9})
	require.NoError(t, c.RestoreSnapshot(snap, dst))
	require.Equal(t, []byte{1, 2, 9, 9}, dm.Bytes)
}

func TestConcurrentSnapshotMethods(t *testing.T) {
	c := snapshot.NewCoordinator()
	mod, mem := newMemModule(32)
	mem.Bytes[3] = 7
	base, err := c.CaptureSnapshot(mod)
	require.NoError(t, err)
	mem.Bytes[3] = 8
	inc, err := c.CaptureIncremental(base, mod)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			inc.SetTag("k", "v")
			_ = inc.Tags()
			_ = inc.Data()
			_ = inc.CompressedData()
			_ = inc.Compare(base)
			_ = snapshot.Summarize(inc)
			if i%2 == 0 {
				_ = inc.Version()
			}
		}(i)
	}
	wg.Wait()
	require.Equal(t, "v", inc.Tags()["k"])
}
