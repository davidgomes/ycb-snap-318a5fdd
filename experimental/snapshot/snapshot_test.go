package snapshot_test

import (
	"bytes"
	"fmt"
	"sync"
	"testing"

	"github.com/tetratelabs/wazero/experimental/snapshot"
	"github.com/tetratelabs/wazero/internal/testing/require"
)

// fakeSnapshot is a Snapshot implemented outside the package.
type fakeSnapshot struct {
	data    [][]byte
	version uint64
}

func (f *fakeSnapshot) Data() [][]byte {
	data := make([][]byte, len(f.data))
	for i, d := range f.data {
		data[i] = bytes.Clone(d)
	}
	return data
}
func (f *fakeSnapshot) CompressedData() []byte                         { return nil }
func (f *fakeSnapshot) Version() uint64                                { return f.version }
func (f *fakeSnapshot) Tags() map[string]string                        { return map[string]string{} }
func (f *fakeSnapshot) SetTag(string, string)                          {}
func (f *fakeSnapshot) Compare(snapshot.Snapshot) []snapshot.DiffEntry { return nil }

func TestSnapshot_immutable(t *testing.T) {
	r := newRuntime(t)
	a := instantiate(t, r, "a", 1)
	require.True(t, a.Memory().WriteString(0, "original"))

	c := snapshot.NewCoordinator()
	full, err := c.CaptureSnapshot(a)
	require.NoError(t, err)
	require.True(t, a.Memory().WriteString(0, "modified"))
	inc, err := c.CaptureIncremental(full, a)
	require.NoError(t, err)

	for _, snap := range []snapshot.Snapshot{full, inc} {
		data := snap.Data()
		compressed := snap.CompressedData()
		snap.SetTag("k", "v")

		require.True(t, a.Memory().WriteString(0, "later"))
		got := snap.Data()
		got[0][0] = 'X'
		got[0] = nil
		tags := snap.Tags()
		tags["k"] = "changed"
		tags["added"] = "x"
		snap.CompressedData()[0]++

		require.Equal(t, data, snap.Data())
		require.Equal(t, compressed, snap.CompressedData())
		require.Equal(t, map[string]string{"k": "v"}, snap.Tags())
	}
	require.Equal(t, []byte("original"), full.Data()[0][:8])
	require.Equal(t, []byte("modified"), inc.Data()[0][:8])
}

func TestSnapshot_Tags(t *testing.T) {
	r := newRuntime(t)
	c := snapshot.NewCoordinator()
	snap, err := c.CaptureSnapshot(instantiate(t, r, "a", 0))
	require.NoError(t, err)
	require.Equal(t, map[string]string{}, snap.Tags())

	snap.SetTag("env", "dev")
	snap.SetTag("step", "1")
	snap.SetTag("step", "2")
	require.Equal(t, map[string]string{"env": "dev", "step": "2"}, snap.Tags())

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			snap.SetTag(fmt.Sprint(i), "x")
		}()
		go func() {
			defer wg.Done()
			_ = snap.Tags()
		}()
	}
	wg.Wait()
	require.Equal(t, 12, len(snap.Tags()))
}

func TestSnapshot_Compare(t *testing.T) {
	r := newRuntime(t)
	a, b := instantiate(t, r, "a", 1), instantiate(t, r, "b", 1)

	c := snapshot.NewCoordinator()
	before, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)
	require.Nil(t, before.Compare(before))
	require.Nil(t, before.Compare(nil))

	require.True(t, b.Memory().WriteByte(900, 3))
	require.True(t, b.Memory().WriteByte(5, 4))
	require.True(t, a.Memory().WriteByte(pageSize-1, 1))
	require.True(t, a.Memory().WriteString(300, "hi"))
	after, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)
	inc, err := c.CaptureIncremental(before, a, b)
	require.NoError(t, err)

	expected := []snapshot.DiffEntry{
		{Offset: 300, NewValue: 'h'},
		{Offset: 301, NewValue: 'i'},
		{Offset: pageSize - 1, NewValue: 1},
		{Offset: 5, NewValue: 4},
		{Offset: 900, NewValue: 3},
	}
	require.Equal(t, expected, before.Compare(after))
	require.Equal(t, expected, before.Compare(inc))
	require.Nil(t, after.Compare(inc))
	require.Equal(t, uint64(len(expected)), snapshot.Summarize(inc).ModifiedBytes)

	reversed := after.Compare(before)
	for i, e := range expected {
		require.Equal(t, snapshot.DiffEntry{Offset: e.Offset, OldValue: e.NewValue, NewValue: e.OldValue}, reversed[i])
	}

	t.Run("different module counts", func(t *testing.T) {
		onlyB, err := c.CaptureSnapshot(b)
		require.NoError(t, err)
		require.Equal(t, []snapshot.DiffEntry{
			// a compared to b
			{Offset: 5, NewValue: 4},
			{Offset: 300, OldValue: 'h'},
			{Offset: 301, OldValue: 'i'},
			{Offset: 900, NewValue: 3},
			{Offset: pageSize - 1, OldValue: 1},
			// b compared to nothing
			{Offset: 5, OldValue: 4},
			{Offset: 900, OldValue: 3},
		}, after.Compare(onlyB))
	})

	t.Run("external snapshot", func(t *testing.T) {
		other := &fakeSnapshot{data: [][]byte{{0, 7}}}
		require.Equal(t, []snapshot.DiffEntry{
			{Offset: 1, NewValue: 7},
			{Offset: 300, OldValue: 'h'},
			{Offset: 301, OldValue: 'i'},
			{Offset: pageSize - 1, OldValue: 1},
			{Offset: 5, OldValue: 4},
			{Offset: 900, OldValue: 3},
		}, after.Compare(other))
	})
}
