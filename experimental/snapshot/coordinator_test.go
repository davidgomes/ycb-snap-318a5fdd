package snapshot

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"sort"
	"sync"
	"testing"

	"github.com/tetratelabs/wazero/experimental/wazerotest"
	"github.com/tetratelabs/wazero/internal/testing/require"
)

func TestCaptureSnapshotErrors(t *testing.T) {
	c := NewCoordinator()
	_, err := c.CaptureSnapshot()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no modules")

	_, err = c.CaptureSnapshot(nil)
	require.Contains(t, err.Error(), "module closed")

	var nilPtr *wazerotest.Module
	_, err = c.CaptureSnapshot(nilPtr)
	require.Contains(t, err.Error(), "module closed")

	closed := newMemModule(8)
	require.NoError(t, closed.Close(context.Background()))
	_, err = c.CaptureSnapshot(closed)
	require.Contains(t, err.Error(), "module closed")

	// A failed capture does not consume a version.
	live := newMemModule(4)
	snap, err := c.CaptureSnapshot(live)
	require.NoError(t, err)
	require.Equal(t, uint64(1), snap.Version())
}

func TestCaptureAndRestore(t *testing.T) {
	c := NewCoordinator()
	a := newMemModule(8)
	b := newMemModule(4)
	require.True(t, a.Memory().Write(0, []byte{1, 2, 3, 4, 5, 6, 7, 8}))
	require.True(t, b.Memory().Write(0, []byte{9, 8, 7, 6}))

	snap, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)
	require.Equal(t, uint64(1), snap.Version())

	data := snap.Data()
	require.Equal(t, 2, len(data))
	require.Equal(t, []byte{1, 2, 3, 4, 5, 6, 7, 8}, data[0])
	require.Equal(t, []byte{9, 8, 7, 6}, data[1])

	// Deep copy: mutating the result does not change the snapshot.
	data[0][0] = 0
	data[0] = []byte{1}
	again := snap.Data()
	require.Equal(t, []byte{1, 2, 3, 4, 5, 6, 7, 8}, again[0])
	again[0][1] = 0
	require.Equal(t, byte(2), snap.Data()[0][1])

	// Later writes to the module are not visible in the snapshot.
	require.True(t, a.Memory().WriteByte(0, 99))
	require.Equal(t, byte(1), snap.Data()[0][0])

	raw := concat(snap.Data())
	require.Equal(t, gzipOf(raw), snap.CompressedData())
	got, err := gunzip(snap.CompressedData())
	require.NoError(t, err)
	require.Equal(t, raw, got)
	mut := snap.CompressedData()
	mut[0] ^= 0xff
	require.Equal(t, gzipOf(raw), snap.CompressedData())

	sum := Summarize(snap)
	require.Equal(t, SnapshotSummary{
		TotalModules:  2,
		TotalBytes:    12,
		ModifiedBytes: 0,
		Version:       1,
	}, sum)

	// Scramble and restore by identity, including swapped order.
	require.True(t, a.Memory().Write(0, []byte{0, 0, 0, 0, 0, 0, 0, 0}))
	require.True(t, b.Memory().Write(0, []byte{0, 0, 0, 0}))
	require.NoError(t, c.RestoreSnapshot(snap, b, a))
	view, ok := a.Memory().Read(0, 8)
	require.True(t, ok)
	require.Equal(t, []byte{1, 2, 3, 4, 5, 6, 7, 8}, view)
	view, ok = b.Memory().Read(0, 4)
	require.True(t, ok)
	require.Equal(t, []byte{9, 8, 7, 6}, view)
}

func TestRestorePositionalAndPartial(t *testing.T) {
	c := NewCoordinator()
	a := newMemModule(4)
	b := newMemModule(4)
	require.True(t, a.Memory().Write(0, []byte{1, 2, 3, 4}))
	require.True(t, b.Memory().Write(0, []byte{5, 6, 7, 8}))
	snap, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)

	// Equal count with unknown modules falls back to position.
	x := newMemModule(4)
	y := newMemModule(6)
	require.True(t, x.Memory().Write(0, []byte{9, 9, 9, 9}))
	require.True(t, y.Memory().Write(0, []byte{8, 8, 8, 8, 7, 7}))
	require.NoError(t, c.RestoreSnapshot(snap, x, y))
	view, ok := x.Memory().Read(0, 4)
	require.True(t, ok)
	require.Equal(t, []byte{1, 2, 3, 4}, view)
	view, ok = y.Memory().Read(0, 6)
	require.True(t, ok)
	require.Equal(t, []byte{5, 6, 7, 8, 7, 7}, view)

	// Fewer modules match by identity only. An unknown module is skipped.
	require.True(t, a.Memory().Write(0, []byte{0, 0, 0, 0}))
	require.True(t, b.Memory().Write(0, []byte{0, 0, 0, 0}))
	stranger := newMemModule(4)
	require.True(t, stranger.Memory().Write(0, []byte{3, 3, 3, 3}))
	require.NoError(t, c.RestoreSnapshot(snap, stranger))
	view, ok = stranger.Memory().Read(0, 4)
	require.True(t, ok)
	require.Equal(t, []byte{3, 3, 3, 3}, view)
	require.NoError(t, c.RestoreSnapshot(snap, b))
	view, ok = a.Memory().Read(0, 4)
	require.True(t, ok)
	require.Equal(t, []byte{0, 0, 0, 0}, view)
	view, ok = b.Memory().Read(0, 4)
	require.True(t, ok)
	require.Equal(t, []byte{5, 6, 7, 8}, view)

	// A captured module keeps its own image even when it shares the call with a
	// replacement in the other position.
	require.True(t, a.Memory().Write(0, []byte{0, 0, 0, 0}))
	require.True(t, b.Memory().Write(0, []byte{0, 0, 0, 0}))
	replacement := newMemModule(4)
	require.NoError(t, c.RestoreSnapshot(snap, b, replacement))
	view, ok = b.Memory().Read(0, 4)
	require.True(t, ok)
	require.Equal(t, []byte{5, 6, 7, 8}, view)
	view, ok = replacement.Memory().Read(0, 4)
	require.True(t, ok)
	require.Equal(t, []byte{1, 2, 3, 4}, view)

	// No match still succeeds.
	other := newMemModule(4)
	require.True(t, other.Memory().Write(0, []byte{4, 4, 4, 4}))
	require.NoError(t, c.RestoreSnapshot(snap, other))
	view, ok = other.Memory().Read(0, 4)
	require.True(t, ok)
	require.Equal(t, []byte{4, 4, 4, 4}, view)
	require.NoError(t, c.RestoreSnapshot(snap))

	extra := newMemModule(4)
	err = c.RestoreSnapshot(snap, a, b, extra)
	require.Error(t, err)
	require.Contains(t, err.Error(), "incompatible module")
}

func TestRestoreInsufficientMemory(t *testing.T) {
	c := NewCoordinator()
	src := newMemModule(4)
	require.True(t, src.Memory().Write(0, []byte{1, 2, 3, 4}))
	snap, err := c.CaptureSnapshot(src)
	require.NoError(t, err)

	small := newMemModule(3)
	require.True(t, small.Memory().Write(0, []byte{9, 9, 9}))
	err = c.RestoreSnapshot(snap, small)
	require.Error(t, err)
	require.Equal(t, "insufficient_memory", ErrorCode(err))
	require.Contains(t, err.Error(), "insufficient_memory")
	view, ok := small.Memory().Read(0, 3)
	require.True(t, ok)
	require.Equal(t, []byte{9, 9, 9}, view)

	emptyMod := &wazerotest.Module{}
	err = c.RestoreSnapshot(snap, emptyMod)
	require.Equal(t, "insufficient_memory", ErrorCode(err))

	// A later module that cannot fit leaves the earlier module unchanged.
	first := newMemModule(4)
	second := newMemModule(1)
	require.True(t, first.Memory().Write(0, []byte{7, 7, 7, 7}))
	two, err := c.CaptureSnapshot(src, src)
	require.NoError(t, err)
	err = c.RestoreSnapshot(two, first, second)
	require.Equal(t, "insufficient_memory", ErrorCode(err))
	view, ok = first.Memory().Read(0, 4)
	require.True(t, ok)
	require.Equal(t, []byte{7, 7, 7, 7}, view)

	require.Equal(t, "", ErrorCode(errNoModules))
	require.Equal(t, "", ErrorCode(nil))

	closed := newMemModule(4)
	require.NoError(t, closed.Close(context.Background()))
	err = c.RestoreSnapshot(snap, closed)
	require.Contains(t, err.Error(), "module closed")
}

func TestIncrementalSnapshot(t *testing.T) {
	c := NewCoordinator()
	m := newMemModule(1024)
	require.True(t, m.Memory().Write(0, []byte{1, 2, 3, 4, 5}))
	base, err := c.CaptureSnapshot(m)
	require.NoError(t, err)

	_, err = c.CaptureIncremental(nil, m)
	require.Contains(t, err.Error(), "baseline snapshot is nil")
	_, err = c.CaptureIncremental(base)
	require.Contains(t, err.Error(), "module count mismatch")
	_, err = c.CaptureIncremental(base, m, m)
	require.Contains(t, err.Error(), "module count mismatch")
	_, err = c.CaptureIncremental(base, nil)
	require.Contains(t, err.Error(), "module closed")

	require.True(t, m.Memory().WriteByte(1, 9))
	require.True(t, m.Memory().WriteByte(4, 8))
	inc, err := c.CaptureIncremental(base, m)
	require.NoError(t, err)
	require.Equal(t, uint64(2), inc.Version())
	require.Equal(t, 1024, len(inc.Data()[0]))
	require.Equal(t, byte(1), inc.Data()[0][0])
	require.Equal(t, byte(9), inc.Data()[0][1])
	require.Equal(t, byte(3), inc.Data()[0][2])
	require.Equal(t, byte(8), inc.Data()[0][4])
	require.True(t, len(inc.CompressedData()) < len(base.CompressedData()))
	_, err = gunzip(inc.CompressedData())
	require.NoError(t, err)

	sum := Summarize(inc)
	require.Equal(t, 1, sum.TotalModules)
	require.Equal(t, uint64(1024), sum.TotalBytes)
	require.Equal(t, uint64(2), sum.ModifiedBytes)
	require.Equal(t, uint64(2), sum.Version)
	require.Equal(t, len(base.Compare(inc)), int(sum.ModifiedBytes))

	diffs := base.Compare(inc)
	require.Equal(t, []DiffEntry{
		{Offset: 1, OldValue: 2, NewValue: 9},
		{Offset: 4, OldValue: 5, NewValue: 8},
	}, diffs)
	// Swapped direction exchanges old and new.
	back := inc.Compare(base)
	require.Equal(t, byte(9), back[0].OldValue)
	require.Equal(t, byte(2), back[0].NewValue)

	// Chained incremental reconstructs the full image, not only the last delta.
	require.True(t, m.Memory().WriteByte(0, 7))
	inc2, err := c.CaptureIncremental(inc, m)
	require.NoError(t, err)
	require.Equal(t, uint64(3), inc2.Version())
	require.Equal(t, byte(7), inc2.Data()[0][0])
	require.Equal(t, byte(9), inc2.Data()[0][1])
	require.Equal(t, byte(8), inc2.Data()[0][4])
	require.Equal(t, uint64(1), Summarize(inc2).ModifiedBytes)
	require.True(t, len(inc2.CompressedData()) < len(inc.CompressedData()))

	// Restoring the first incremental drops the later edit.
	require.NoError(t, c.RestoreSnapshot(inc, m))
	b0, ok := m.Memory().ReadByte(0)
	require.True(t, ok)
	require.Equal(t, byte(1), b0)
	b1, ok := m.Memory().ReadByte(1)
	require.True(t, ok)
	require.Equal(t, byte(9), b1)

	// Versions stay contiguous when another full capture follows.
	full, err := c.CaptureSnapshot(m)
	require.NoError(t, err)
	require.Equal(t, uint64(4), full.Version())
	require.Equal(t, uint64(0), Summarize(full).ModifiedBytes)
}

func TestCompareGroupsModules(t *testing.T) {
	c := NewCoordinator()
	a := newMemModule(6)
	b := newMemModule(3)
	require.True(t, a.Memory().Write(0, []byte{1, 2, 3, 4, 5, 6}))
	require.True(t, b.Memory().Write(0, []byte{7, 8, 9}))
	before, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)
	require.True(t, a.Memory().WriteByte(5, 60))
	require.True(t, a.Memory().WriteByte(1, 20))
	require.True(t, b.Memory().WriteByte(0, 70))
	after, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)

	require.Equal(t, []DiffEntry{
		{Offset: 1, OldValue: 2, NewValue: 20},
		{Offset: 5, OldValue: 6, NewValue: 60},
		{Offset: 0, OldValue: 7, NewValue: 70},
	}, before.Compare(after))
	require.Zero(t, len(before.Compare(before)))
}

func TestTagsAreCopied(t *testing.T) {
	c := NewCoordinator()
	m := newMemModule(2)
	snap, err := c.CaptureSnapshot(m)
	require.NoError(t, err)
	require.Equal(t, 0, len(snap.Tags()))

	snap.SetTag("region", "heap")
	snap.SetTag("region", "stack")
	snap.SetTag("owner", "test")
	got := snap.Tags()
	got["region"] = "mutated"
	got["extra"] = "nope"
	require.Equal(t, map[string]string{"region": "stack", "owner": "test"}, snap.Tags())
}

func TestEmptyMemoryModule(t *testing.T) {
	c := NewCoordinator()
	bare := &wazerotest.Module{}
	snap, err := c.CaptureSnapshot(bare)
	require.NoError(t, err)
	require.Equal(t, []byte{}, snap.Data()[0])
	require.Equal(t, uint64(0), Summarize(snap).TotalBytes)

	// Empty image restores onto a module with no memory and onto a larger one.
	require.NoError(t, c.RestoreSnapshot(snap, bare))
	other := newMemModule(2)
	require.True(t, other.Memory().Write(0, []byte{4, 5}))
	require.NoError(t, c.RestoreSnapshot(snap, other))
	view, ok := other.Memory().Read(0, 2)
	require.True(t, ok)
	require.Equal(t, []byte{4, 5}, view)

	inc, err := c.CaptureIncremental(snap, bare)
	require.NoError(t, err)
	require.Equal(t, uint64(0), Summarize(inc).ModifiedBytes)
	require.True(t, len(inc.CompressedData()) < len(snap.CompressedData()) || len(snap.CompressedData()) == 0)
	require.True(t, len(inc.CompressedData()) < len(snap.CompressedData()))
}

func TestPageSizedIncrementalIsSmaller(t *testing.T) {
	c := NewCoordinator()
	m := wazerotest.NewModule(wazerotest.NewMemory(1))
	base, err := c.CaptureSnapshot(m)
	require.NoError(t, err)
	require.Equal(t, uint32(65536), m.Memory().Size())

	require.True(t, m.Memory().WriteByte(100, 0x5a))
	inc, err := c.CaptureIncremental(base, m)
	require.NoError(t, err)
	require.Equal(t, byte(0x5a), inc.Data()[0][100])
	require.Equal(t, 65536, len(inc.Data()[0]))
	require.True(t, len(inc.CompressedData()) < len(base.CompressedData()))
	plain, err := gunzip(inc.CompressedData())
	require.NoError(t, err)
	require.True(t, len(plain) > 0)

	require.True(t, m.Memory().WriteByte(200, 0x11))
	inc2, err := c.CaptureIncremental(inc, m)
	require.NoError(t, err)
	require.Equal(t, byte(0x5a), inc2.Data()[0][100])
	require.Equal(t, byte(0x11), inc2.Data()[0][200])
	require.True(t, len(inc2.CompressedData()) < len(inc.CompressedData()))
	_, err = gunzip(inc2.CompressedData())
	require.NoError(t, err)
	require.Equal(t, uint64(1), Summarize(inc2).ModifiedBytes)
}

func TestVersionsAreGapFreeUnderConcurrency(t *testing.T) {
	c := NewCoordinator()
	m := newMemModule(8)
	base, err := c.CaptureSnapshot(m)
	require.NoError(t, err)

	const n = 40
	var wg sync.WaitGroup
	versions := make([]uint64, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var snap Snapshot
			if i%2 == 0 {
				snap, errs[i] = c.CaptureSnapshot(m)
			} else {
				snap, errs[i] = c.CaptureIncremental(base, m)
			}
			if snap != nil {
				versions[i] = snap.Version()
			}
		}(i)
	}
	wg.Wait()
	for i := range errs {
		require.NoError(t, errs[i])
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	require.Equal(t, uint64(2), versions[0])
	for i := range versions {
		require.Equal(t, uint64(i+2), versions[i])
	}

	other := NewCoordinator()
	snap, err := other.CaptureSnapshot(m)
	require.NoError(t, err)
	require.Equal(t, uint64(1), snap.Version())
}

func TestRegistryAndContext(t *testing.T) {
	name := t.Name()
	defer Unregister(name)

	_, ok := Get(name)
	require.False(t, ok)

	first := NewCoordinator()
	second := NewCoordinator()
	Register(name, first)
	got, ok := Get(name)
	require.True(t, ok)
	require.Equal(t, first, got)

	Register(name, second)
	got, ok = Get(name)
	require.True(t, ok)
	require.Equal(t, second, got)

	Unregister(name)
	_, ok = Get(name)
	require.False(t, ok)
	Unregister(name)

	require.Nil(t, GetCoordinator(context.Background()))
	ctx := WithCoordinator(context.Background(), first)
	require.Equal(t, first, GetCoordinator(ctx))
	require.Equal(t, first, GetCoordinator(context.WithValue(ctx, struct{}{}, "child")))
}

func TestRegistryConcurrent(t *testing.T) {
	const n = 32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "concurrent-" + string(rune('a'+i))
			c := NewCoordinator()
			Register(name, c)
			got, ok := Get(name)
			require.True(t, ok)
			require.Equal(t, c, got)
			Unregister(name)
		}(i)
	}
	wg.Wait()
}

func TestChain(t *testing.T) {
	chain := NewChain()
	require.Equal(t, 0, chain.Len())
	require.Nil(t, chain.Head())
	require.Equal(t, 0, len(chain.Snapshots()))

	c := NewCoordinator()
	m := newMemModule(1)
	s1, err := c.CaptureSnapshot(m)
	require.NoError(t, err)
	require.True(t, m.Memory().WriteByte(0, 1))
	s2, err := c.CaptureSnapshot(m)
	require.NoError(t, err)

	chain.Push(s1)
	chain.Push(s2)
	require.Equal(t, 2, chain.Len())
	require.Equal(t, s2, chain.Head())
	snaps := chain.Snapshots()
	require.Equal(t, []Snapshot{s1, s2}, snaps)
	snaps[0] = nil
	require.Equal(t, s1, chain.Snapshots()[0])
}

func TestMarshalRoundTrip(t *testing.T) {
	c := NewCoordinator()
	a := newMemModule(4)
	b := newMemModule(2)
	require.True(t, a.Memory().Write(0, []byte{1, 2, 3, 4}))
	require.True(t, b.Memory().Write(0, []byte{0, 255}))
	base, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)
	base.SetTag("k", "v")
	base.SetTag("empty", "")
	base.SetTag("uni", "héllo")

	require.True(t, a.Memory().WriteByte(2, 9))
	inc, err := c.CaptureIncremental(base, a, b)
	require.NoError(t, err)
	inc.SetTag("gen", "2")

	blob, err := MarshalSnapshot(inc)
	require.NoError(t, err)
	again, err := MarshalSnapshot(inc)
	require.NoError(t, err)
	require.Equal(t, blob, again)

	decoded, err := UnmarshalSnapshot(blob)
	require.NoError(t, err)
	require.Equal(t, inc.Version(), decoded.Version())
	require.Equal(t, inc.Data(), decoded.Data())
	require.Equal(t, inc.Tags(), decoded.Tags())
	require.Equal(t, uint64(0), Summarize(decoded).ModifiedBytes)
	require.Equal(t, gzipOf(concat(decoded.Data())), decoded.CompressedData())
	plain, err := gunzip(decoded.CompressedData())
	require.NoError(t, err)
	require.Equal(t, concat(decoded.Data()), plain)

	// Decoded snapshots have no module identity, so a short restore is a no-op
	// and an equal count restores in order.
	require.True(t, a.Memory().Write(0, []byte{0, 0, 0, 0}))
	require.NoError(t, c.RestoreSnapshot(decoded, a))
	view, ok := a.Memory().Read(0, 4)
	require.True(t, ok)
	require.Equal(t, []byte{0, 0, 0, 0}, view)

	freshA := newMemModule(4)
	freshB := newMemModule(2)
	require.NoError(t, c.RestoreSnapshot(decoded, freshA, freshB))
	view, ok = freshA.Memory().Read(0, 4)
	require.True(t, ok)
	require.Equal(t, inc.Data()[0], view)
	view, ok = freshB.Memory().Read(0, 2)
	require.True(t, ok)
	require.Equal(t, inc.Data()[1], view)

	_, err = MarshalSnapshot(nil)
	require.Error(t, err)
	_, err = UnmarshalSnapshot(nil)
	require.Error(t, err)
	_, err = UnmarshalSnapshot([]byte("nope"))
	require.Error(t, err)
	truncated := blob[:len(blob)-1]
	_, err = UnmarshalSnapshot(truncated)
	require.Error(t, err)
	_, err = UnmarshalSnapshot(append(blob, 0))
	require.Error(t, err)
}

func TestNilSnapshotRestore(t *testing.T) {
	c := NewCoordinator()
	err := c.RestoreSnapshot(nil)
	require.Error(t, err)
	require.Zero(t, Summarize(nil).Version)
}

func newMemModule(n int) *wazerotest.Module {
	return &wazerotest.Module{ExportMemory: &wazerotest.Memory{Bytes: make([]byte, n)}}
}

func gzipOf(p []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(p)
	_ = w.Close()
	return buf.Bytes()
}

func gunzip(p []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(p))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}
