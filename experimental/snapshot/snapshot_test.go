package snapshot

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental/wazerotest"
	"github.com/tetratelabs/wazero/internal/testing/require"
)

func newModule(size int) *wazerotest.Module {
	return wazerotest.NewModule(&wazerotest.Memory{Bytes: make([]byte, size)})
}

func TestCaptureSnapshot(t *testing.T) {
	c := NewCoordinator()
	m1, m2 := newModule(65536), newModule(65536)
	m1.Memory().WriteByte(10, 1)
	m2.Memory().WriteByte(20, 2)

	s, err := c.CaptureSnapshot(m1, m2)
	require.NoError(t, err)
	require.Equal(t, uint64(1), s.Version())

	data := s.Data()
	require.Equal(t, 2, len(data))
	require.Equal(t, byte(1), data[0][10])
	require.Equal(t, byte(2), data[1][20])

	data[0][10] = 99
	require.Equal(t, byte(1), s.Data()[0][10])

	r, err := gzip.NewReader(bytes.NewReader(s.CompressedData()))
	require.NoError(t, err)
	raw, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, bytes.Join(s.Data(), nil), raw)

	s.SetTag("k", "v")
	tags := s.Tags()
	tags["k"] = "x"
	require.Equal(t, "v", s.Tags()["k"])
}

func TestCaptureErrors(t *testing.T) {
	c := NewCoordinator()
	_, err := c.CaptureSnapshot()
	errorContains(t, err, "no modules")

	_, err = c.CaptureSnapshot(nil)
	errorContains(t, err, "module closed")

	closed := newModule(16)
	require.NoError(t, closed.Close(context.Background()))
	_, err = c.CaptureSnapshot(closed)
	errorContains(t, err, "module closed")

	_, err = c.CaptureIncremental(nil, newModule(16))
	errorContains(t, err, "baseline snapshot is nil")

	m := newModule(16)
	s, err := c.CaptureSnapshot(m)
	require.NoError(t, err)
	require.Equal(t, uint64(1), s.Version())
	_, err = c.CaptureIncremental(s, m, newModule(16))
	errorContains(t, err, "module count mismatch")
}

func TestCaptureIncremental(t *testing.T) {
	c := NewCoordinator()
	m1, m2 := newModule(65536), newModule(65536)
	for i := 0; i < 65536; i += 7 {
		m1.Memory().WriteByte(uint32(i), byte(i))
	}
	base, err := c.CaptureSnapshot(m1, m2)
	require.NoError(t, err)

	m1.Memory().WriteByte(3, 0xff)
	m2.Memory().WriteByte(100, 0xaa)
	m2.Memory().WriteByte(5, 0xbb)
	inc, err := c.CaptureIncremental(base, m1, m2)
	require.NoError(t, err)
	require.Equal(t, uint64(2), inc.Version())
	require.True(t, len(inc.CompressedData()) < len(base.CompressedData()))

	data := inc.Data()
	require.Equal(t, byte(0xff), data[0][3])
	require.Equal(t, byte(0xaa), data[1][100])

	diff := base.Compare(inc)
	require.Equal(t, []DiffEntry{
		{Offset: 3, OldValue: 0, NewValue: 0xff},
		{Offset: 5, OldValue: 0, NewValue: 0xbb},
		{Offset: 100, OldValue: 0, NewValue: 0xaa},
	}, diff)

	sum := Summarize(inc)
	require.Equal(t, SnapshotSummary{TotalModules: 2, TotalBytes: 131072, ModifiedBytes: 3, Version: 2}, sum)
	require.Equal(t, uint64(0), Summarize(base).ModifiedBytes)

	m1.Memory().WriteByte(4, 1)
	inc2, err := c.CaptureIncremental(inc, m1, m2)
	require.NoError(t, err)
	require.Equal(t, uint64(3), inc2.Version())
	require.Equal(t, uint64(1), Summarize(inc2).ModifiedBytes)
	require.Equal(t, byte(0xff), inc2.Data()[0][3])
}

func TestRestoreSnapshot(t *testing.T) {
	c := NewCoordinator()
	m1, m2 := newModule(64), newModule(64)
	m1.Memory().WriteByte(0, 1)
	m2.Memory().WriteByte(0, 2)
	s, err := c.CaptureSnapshot(m1, m2)
	require.NoError(t, err)

	m1.Memory().WriteByte(0, 9)
	m2.Memory().WriteByte(0, 9)

	// Identity match, fewer modules.
	require.NoError(t, c.RestoreSnapshot(s, m2))
	v, _ := m2.Memory().ReadByte(0)
	require.Equal(t, byte(2), v)
	v, _ = m1.Memory().ReadByte(0)
	require.Equal(t, byte(9), v)

	// Unmatched modules are skipped.
	other := newModule(64)
	require.NoError(t, c.RestoreSnapshot(s, other))
	v, _ = other.Memory().ReadByte(0)
	require.Equal(t, byte(0), v)

	// Positional fallback.
	n1, n2 := newModule(64), newModule(64)
	require.NoError(t, c.RestoreSnapshot(s, n2, n1))
	v, _ = n2.Memory().ReadByte(0)
	require.Equal(t, byte(1), v)

	err = c.RestoreSnapshot(s, m1, m2, newModule(64))
	errorContains(t, err, "incompatible module")

	err = c.RestoreSnapshot(s, newModule(8), newModule(8))
	require.Equal(t, "insufficient_memory", ErrorCode(err))
}

func TestMarshal(t *testing.T) {
	c := NewCoordinator()
	m := newModule(128)
	m.Memory().WriteByte(7, 7)
	base, err := c.CaptureSnapshot(m)
	require.NoError(t, err)
	m.Memory().WriteByte(8, 8)
	inc, err := c.CaptureIncremental(base, m)
	require.NoError(t, err)
	inc.SetTag("a", "b")

	b, err := MarshalSnapshot(inc)
	require.NoError(t, err)
	got, err := UnmarshalSnapshot(b)
	require.NoError(t, err)
	require.Equal(t, inc.Data(), got.Data())
	require.Equal(t, inc.Version(), got.Version())
	require.Equal(t, map[string]string{"a": "b"}, got.Tags())
	require.Equal(t, uint64(0), Summarize(got).ModifiedBytes)

	_, err = MarshalSnapshot(nil)
	require.Error(t, err)
	_, err = UnmarshalSnapshot([]byte("junk"))
	require.Error(t, err)
	_, err = UnmarshalSnapshot(b[:len(b)-1])
	require.Error(t, err)
}

func TestConcurrentVersions(t *testing.T) {
	c := NewCoordinator()
	m := newModule(64)
	base, err := c.CaptureSnapshot(m)
	require.NoError(t, err)

	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[uint64]bool{1: true}
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var s Snapshot
			var err error
			if i%2 == 0 {
				s, err = c.CaptureSnapshot(m)
			} else {
				s, err = c.CaptureIncremental(base, m)
			}
			require.NoError(t, err)
			_ = c.RestoreSnapshot(s, m)
			mu.Lock()
			seen[s.Version()] = true
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	for v := uint64(1); v <= 51; v++ {
		require.True(t, seen[v])
	}
}

func TestRegistryAndContext(t *testing.T) {
	c1, c2 := NewCoordinator(), NewCoordinator()
	Register("x", c1)
	Register("x", c2)
	got, ok := Get("x")
	require.True(t, ok)
	require.Equal(t, c2, got)
	Unregister("x")
	_, ok = Get("x")
	require.False(t, ok)

	require.Nil(t, GetCoordinator(context.Background()))
	require.Equal(t, c1, GetCoordinator(WithCoordinator(context.Background(), c1)))
}

func TestChain(t *testing.T) {
	ch := NewChain()
	require.Nil(t, ch.Head())
	require.Equal(t, 0, ch.Len())
	c := NewCoordinator()
	var mods []api.Module
	mods = append(mods, newModule(8))
	s1, _ := c.CaptureSnapshot(mods...)
	s2, _ := c.CaptureSnapshot(mods...)
	ch.Push(s1)
	ch.Push(s2)
	require.Equal(t, 2, ch.Len())
	require.Equal(t, s2, ch.Head())
	snaps := ch.Snapshots()
	snaps[0] = nil
	require.Equal(t, s1, ch.Snapshots()[0])
}

func TestErrorCode(t *testing.T) {
	require.Equal(t, "", ErrorCode(nil))
	_, err := NewCoordinator().CaptureSnapshot()
	require.True(t, strings.Contains(err.Error(), "no modules"))
	require.Equal(t, CodeNoModules, ErrorCode(err))
}

func errorContains(t *testing.T, err error, substr string) {
	t.Helper()
	require.Error(t, err)
	require.Contains(t, err.Error(), substr)
}
