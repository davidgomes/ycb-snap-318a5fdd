package snapshot_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental/snapshot"
	"github.com/tetratelabs/wazero/internal/testing/binaryencoding"
	"github.com/tetratelabs/wazero/internal/testing/require"
	"github.com/tetratelabs/wazero/internal/wasm"
)

const pageSize = 65536

var testCtx = context.Background()

func newRuntime(t *testing.T) wazero.Runtime {
	r := wazero.NewRuntime(testCtx)
	t.Cleanup(func() { require.NoError(t, r.Close(testCtx)) })
	return r
}

// instantiate returns a module named name with a memory of the given pages,
// or without memory if pages is negative.
func instantiate(t *testing.T, r wazero.Runtime, name string, pages int) api.Module {
	t.Helper()
	m := &wasm.Module{}
	if pages >= 0 {
		m.MemorySection = &wasm.Memory{Min: uint32(pages), Max: 8, IsMaxEncoded: true}
		m.ExportSection = []wasm.Export{{Type: wasm.ExternTypeMemory, Name: "memory"}}
	}
	mod, err := r.InstantiateWithConfig(testCtx, binaryencoding.EncodeModule(m), wazero.NewModuleConfig().WithName(name))
	require.NoError(t, err)
	return mod
}

func memoryOf(mod api.Module) []byte {
	buf, _ := mod.Memory().Read(0, mod.Memory().Size())
	return bytes.Clone(buf)
}

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(b))
	require.NoError(t, err)
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return out
}

func TestCoordinator_CaptureSnapshot(t *testing.T) {
	r := newRuntime(t)
	a, b := instantiate(t, r, "a", 1), instantiate(t, r, "b", 2)
	require.True(t, a.Memory().WriteString(10, "hello"))
	require.True(t, b.Memory().WriteString(70000, "world"))

	c := snapshot.NewCoordinator()
	snap, err := c.CaptureSnapshot(b, a)
	require.NoError(t, err)

	require.Equal(t, uint64(1), snap.Version())
	require.Equal(t, [][]byte{memoryOf(b), memoryOf(a)}, snap.Data())
	require.Equal(t, append(memoryOf(b), memoryOf(a)...), gunzip(t, snap.CompressedData()))
	require.Equal(t, map[string]string{}, snap.Tags())
	require.Equal(t, snapshot.SnapshotSummary{TotalModules: 2, TotalBytes: 3 * pageSize, Version: 1}, snapshot.Summarize(snap))

	snap, err = c.CaptureSnapshot(a)
	require.NoError(t, err)
	require.Equal(t, uint64(2), snap.Version())
}

func TestCoordinator_CaptureSnapshot_withoutMemory(t *testing.T) {
	r := newRuntime(t)
	a, none := instantiate(t, r, "a", 1), instantiate(t, r, "none", -1)

	c := snapshot.NewCoordinator()
	snap, err := c.CaptureSnapshot(a, none)
	require.NoError(t, err)
	require.Equal(t, [][]byte{memoryOf(a), {}}, snap.Data())

	inc, err := c.CaptureIncremental(snap, a, none)
	require.NoError(t, err)
	require.Equal(t, [][]byte{memoryOf(a), {}}, inc.Data())
	require.NoError(t, c.RestoreSnapshot(inc, a, none))
}

func TestCoordinator_CaptureSnapshot_errors(t *testing.T) {
	r := newRuntime(t)
	a, closed := instantiate(t, r, "a", 1), instantiate(t, r, "closed", 1)
	require.NoError(t, closed.Close(testCtx))

	tests := []struct {
		name        string
		mods        []api.Module
		expectedErr string
		code        string
	}{
		{name: "no modules", expectedErr: "no modules to capture", code: snapshot.CodeNoModules},
		{name: "nil", mods: []api.Module{a, nil}, expectedErr: "module closed: module at index 1 is nil", code: snapshot.CodeModuleClosed},
		{name: "closed", mods: []api.Module{closed, a}, expectedErr: `module closed: "closed" at index 0`, code: snapshot.CodeModuleClosed},
	}

	c := snapshot.NewCoordinator()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snap, err := c.CaptureSnapshot(tc.mods...)
			require.EqualError(t, err, tc.expectedErr)
			require.Equal(t, tc.code, snapshot.ErrorCode(err))
			require.Nil(t, snap)
		})
	}

	snap, err := c.CaptureSnapshot(a)
	require.NoError(t, err)
	require.Equal(t, uint64(1), snap.Version(), "failed captures must not use up versions")
}

func TestCoordinator_CaptureIncremental(t *testing.T) {
	r := newRuntime(t)
	a, b := instantiate(t, r, "a", 1), instantiate(t, r, "b", 2)
	require.True(t, a.Memory().WriteString(10, "hello"))

	c := snapshot.NewCoordinator()
	full, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)
	fullData := full.Data()

	require.True(t, a.Memory().WriteString(10, "HELLO"))
	require.True(t, b.Memory().WriteString(pageSize+3, "ab"))
	inc1, err := c.CaptureIncremental(full, a, b)
	require.NoError(t, err)
	require.Equal(t, uint64(2), inc1.Version())
	require.Equal(t, [][]byte{memoryOf(a), memoryOf(b)}, inc1.Data())
	require.True(t, len(inc1.CompressedData()) < len(full.CompressedData()))
	require.Equal(t, snapshot.SnapshotSummary{TotalModules: 2, TotalBytes: 3 * pageSize, ModifiedBytes: 7, Version: 2}, snapshot.Summarize(inc1))
	require.Equal(t, fullData, full.Data(), "baseline must not change")

	require.True(t, b.Memory().WriteByte(0, 1))
	inc2, err := c.CaptureIncremental(inc1, a, b)
	require.NoError(t, err)
	require.Equal(t, uint64(3), inc2.Version())
	require.Equal(t, [][]byte{memoryOf(a), memoryOf(b)}, inc2.Data())
	require.True(t, len(inc2.CompressedData()) < len(inc1.CompressedData()))
	require.Equal(t, uint64(1), snapshot.Summarize(inc2).ModifiedBytes)

	unchanged, err := c.CaptureIncremental(inc2, a, b)
	require.NoError(t, err)
	require.Equal(t, uint64(4), unchanged.Version())
	require.Equal(t, inc2.Data(), unchanged.Data())
	require.Equal(t, uint64(0), snapshot.Summarize(unchanged).ModifiedBytes)

	full2, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)
	require.Equal(t, uint64(5), full2.Version())
}

func TestCoordinator_CaptureIncremental_resized(t *testing.T) {
	r := newRuntime(t)

	t.Run("grown", func(t *testing.T) {
		a := instantiate(t, r, "grown", 1)
		c := snapshot.NewCoordinator()
		full, err := c.CaptureSnapshot(a)
		require.NoError(t, err)

		_, ok := a.Memory().Grow(1)
		require.True(t, ok)
		require.True(t, a.Memory().WriteByte(pageSize+1, 9))
		inc, err := c.CaptureIncremental(full, a)
		require.NoError(t, err)

		require.Equal(t, [][]byte{memoryOf(a)}, inc.Data())
		require.Equal(t, snapshot.SnapshotSummary{TotalModules: 1, TotalBytes: 2 * pageSize, ModifiedBytes: 1, Version: 2}, snapshot.Summarize(inc))
		require.Equal(t, []snapshot.DiffEntry{{Offset: pageSize + 1, NewValue: 9}}, full.Compare(inc))
	})

	t.Run("shrunk", func(t *testing.T) {
		big, small := instantiate(t, r, "big", 2), instantiate(t, r, "small", 1)
		require.True(t, big.Memory().WriteByte(pageSize+1, 9))
		require.True(t, big.Memory().WriteByte(2, 3))
		c := snapshot.NewCoordinator()
		full, err := c.CaptureSnapshot(big)
		require.NoError(t, err)

		inc, err := c.CaptureIncremental(full, small)
		require.NoError(t, err)
		require.Equal(t, [][]byte{memoryOf(small)}, inc.Data())
		require.Equal(t, snapshot.SnapshotSummary{TotalModules: 1, TotalBytes: pageSize, ModifiedBytes: 2, Version: 2}, snapshot.Summarize(inc))
		require.Equal(t, []snapshot.DiffEntry{{Offset: 2, OldValue: 3}, {Offset: pageSize + 1, OldValue: 9}}, full.Compare(inc))
	})
}

func TestCoordinator_CaptureIncremental_errors(t *testing.T) {
	r := newRuntime(t)
	a, b, closed := instantiate(t, r, "a", 1), instantiate(t, r, "b", 1), instantiate(t, r, "closed", 1)

	c := snapshot.NewCoordinator()
	full, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)
	require.NoError(t, closed.Close(testCtx))

	tests := []struct {
		name        string
		baseline    snapshot.Snapshot
		mods        []api.Module
		expectedErr string
		code        string
	}{
		{name: "nil baseline", mods: []api.Module{a, b}, expectedErr: "baseline snapshot is nil", code: snapshot.CodeNilSnapshot},
		{name: "no modules", baseline: full, expectedErr: "module count mismatch: baseline has 2, got 0", code: snapshot.CodeModuleCountMismatch},
		{name: "fewer modules", baseline: full, mods: []api.Module{a}, expectedErr: "module count mismatch: baseline has 2, got 1", code: snapshot.CodeModuleCountMismatch},
		{name: "more modules", baseline: full, mods: []api.Module{a, b, a}, expectedErr: "module count mismatch: baseline has 2, got 3", code: snapshot.CodeModuleCountMismatch},
		{name: "nil module", baseline: full, mods: []api.Module{nil, b}, expectedErr: "module closed: module at index 0 is nil", code: snapshot.CodeModuleClosed},
		{name: "closed module", baseline: full, mods: []api.Module{a, closed}, expectedErr: `module closed: "closed" at index 1`, code: snapshot.CodeModuleClosed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snap, err := c.CaptureIncremental(tc.baseline, tc.mods...)
			require.EqualError(t, err, tc.expectedErr)
			require.Equal(t, tc.code, snapshot.ErrorCode(err))
			require.Nil(t, snap)
		})
	}

	inc, err := c.CaptureIncremental(full, a, b)
	require.NoError(t, err)
	require.Equal(t, uint64(2), inc.Version(), "failed captures must not use up versions")
}

func TestCoordinator_CaptureIncremental_externalBaseline(t *testing.T) {
	r := newRuntime(t)
	a := instantiate(t, r, "a", 1)
	require.True(t, a.Memory().WriteString(0, "abc"))

	baseline := &fakeSnapshot{data: [][]byte{make([]byte, pageSize)}, version: 7}
	c := snapshot.NewCoordinator()
	inc, err := c.CaptureIncremental(baseline, a)
	require.NoError(t, err)
	require.Equal(t, uint64(1), inc.Version())
	require.Equal(t, [][]byte{memoryOf(a)}, inc.Data())
	require.Equal(t, uint64(3), snapshot.Summarize(inc).ModifiedBytes)

	baseline.data[0][0] = 'a'
	require.Equal(t, [][]byte{memoryOf(a)}, inc.Data(), "incremental must not depend on a mutable baseline")

	_, err = c.CaptureIncremental(&fakeSnapshot{})
	require.EqualError(t, err, "no modules to capture")
	require.Equal(t, snapshot.CodeNoModules, snapshot.ErrorCode(err))
}

func TestErrorCode(t *testing.T) {
	_, err := snapshot.NewCoordinator().CaptureSnapshot()
	require.Equal(t, snapshot.CodeNoModules, snapshot.ErrorCode(fmt.Errorf("capture: %w", err)))
	require.Equal(t, "", snapshot.ErrorCode(errors.New("other")))
	require.Equal(t, "", snapshot.ErrorCode(nil))
}

func TestSummarize_nil(t *testing.T) {
	require.Equal(t, snapshot.SnapshotSummary{}, snapshot.Summarize(nil))
}

func TestCoordinator_RestoreSnapshot(t *testing.T) {
	r := newRuntime(t)
	a, b := instantiate(t, r, "a", 1), instantiate(t, r, "b", 1)
	x, y := instantiate(t, r, "x", 1), instantiate(t, r, "y", 1)
	big, small := instantiate(t, r, "big", 2), instantiate(t, r, "small", 1)
	all := []api.Module{a, b, x, y, big, small}

	for i, mod := range all {
		fill(mod, byte(i+1))
	}
	c := snapshot.NewCoordinator()
	snapAB, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)
	snapBigA, err := c.CaptureSnapshot(big, a)
	require.NoError(t, err)
	snapSmall, err := c.CaptureSnapshot(small)
	require.NoError(t, err)
	snapABY, err := c.CaptureSnapshot(a, b, y)
	require.NoError(t, err)

	const dirty = 0xee
	page := func(v byte) []byte { return bytes.Repeat([]byte{v}, pageSize) }
	requireDirtyExcept := func(t *testing.T, restored map[api.Module][]byte) {
		t.Helper()
		for _, mod := range all {
			expected, ok := restored[mod]
			if !ok {
				expected = bytes.Repeat([]byte{dirty}, int(mod.Memory().Size()))
			}
			require.Equal(t, expected, memoryOf(mod), mod.Name())
		}
	}

	tests := []struct {
		name     string
		snap     snapshot.Snapshot
		mods     []api.Module
		restored map[api.Module][]byte
	}{
		{name: "same order", snap: snapAB, mods: []api.Module{a, b}, restored: map[api.Module][]byte{a: page(1), b: page(2)}},
		{name: "by identity", snap: snapAB, mods: []api.Module{b, a}, restored: map[api.Module][]byte{a: page(1), b: page(2)}},
		{name: "by position", snap: snapAB, mods: []api.Module{x, y}, restored: map[api.Module][]byte{x: page(1), y: page(2)}},
		{name: "by identity then position", snap: snapAB, mods: []api.Module{x, a}, restored: map[api.Module][]byte{a: page(1), x: page(2)}},
		{name: "subset by identity", snap: snapAB, mods: []api.Module{b}, restored: map[api.Module][]byte{b: page(2)}},
		{name: "subset partly matched", snap: snapABY, mods: []api.Module{x, b}, restored: map[api.Module][]byte{b: page(2)}},
		{name: "subset unmatched", snap: snapAB, mods: []api.Module{x}},
		{name: "no modules", snap: snapAB},
		{name: "larger target", snap: snapSmall, mods: []api.Module{big}, restored: map[api.Module][]byte{big: append(page(6), page(dirty)...)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, mod := range all {
				fill(mod, dirty)
			}
			require.NoError(t, c.RestoreSnapshot(tc.snap, tc.mods...))
			requireDirtyExcept(t, tc.restored)
		})
	}

	t.Run("insufficient memory", func(t *testing.T) {
		for _, mod := range all {
			fill(mod, dirty)
		}
		err := c.RestoreSnapshot(snapBigA, x, small)
		require.EqualError(t, err, `insufficient memory: module "x" has 65536 bytes, snapshot needs 131072`)
		require.Equal(t, snapshot.CodeInsufficientMemory, snapshot.ErrorCode(err))
		requireDirtyExcept(t, nil)
	})

	t.Run("target without memory", func(t *testing.T) {
		none := instantiate(t, r, "none", -1)
		err := c.RestoreSnapshot(snapSmall, none)
		require.EqualError(t, err, `insufficient memory: module "none" has 0 bytes, snapshot needs 65536`)
		require.Equal(t, snapshot.CodeInsufficientMemory, snapshot.ErrorCode(err))
	})
}

func fill(mod api.Module, v byte) {
	buf, _ := mod.Memory().Read(0, mod.Memory().Size())
	for i := range buf {
		buf[i] = v
	}
}

func TestCoordinator_RestoreSnapshot_errors(t *testing.T) {
	r := newRuntime(t)
	a, b, closed := instantiate(t, r, "a", 1), instantiate(t, r, "b", 1), instantiate(t, r, "closed", 1)

	c := snapshot.NewCoordinator()
	snap, err := c.CaptureSnapshot(a)
	require.NoError(t, err)
	require.NoError(t, closed.Close(testCtx))

	tests := []struct {
		name        string
		snap        snapshot.Snapshot
		mods        []api.Module
		expectedErr string
		code        string
	}{
		{name: "nil snapshot", mods: []api.Module{a}, expectedErr: "snapshot is nil", code: snapshot.CodeNilSnapshot},
		{name: "too many modules", snap: snap, mods: []api.Module{a, b}, expectedErr: "incompatible module count: snapshot has 1, got 2", code: snapshot.CodeIncompatibleModule},
		{name: "nil module", snap: snap, mods: []api.Module{nil}, expectedErr: "module closed: module at index 0 is nil", code: snapshot.CodeModuleClosed},
		{name: "closed module", snap: snap, mods: []api.Module{closed}, expectedErr: `module closed: "closed" at index 0`, code: snapshot.CodeModuleClosed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := c.RestoreSnapshot(tc.snap, tc.mods...)
			require.EqualError(t, err, tc.expectedErr)
			require.Equal(t, tc.code, snapshot.ErrorCode(err))
		})
	}
}

func TestCoordinator_RestoreSnapshot_incremental(t *testing.T) {
	r := newRuntime(t)
	a, b := instantiate(t, r, "a", 1), instantiate(t, r, "b", 1)

	c := snapshot.NewCoordinator()
	full, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)
	require.True(t, a.Memory().WriteString(100, "changed"))
	_, ok := b.Memory().Grow(1)
	require.True(t, ok)
	require.True(t, b.Memory().WriteString(pageSize+100, "grown"))
	inc, err := c.CaptureIncremental(full, a, b)
	require.NoError(t, err)
	expected := inc.Data()

	require.True(t, a.Memory().WriteString(100, "CHANGED"))
	require.True(t, b.Memory().WriteString(pageSize+100, "GROWN"))
	require.NoError(t, c.RestoreSnapshot(inc, b, a))
	require.Equal(t, expected, [][]byte{memoryOf(a), memoryOf(b)})

	require.NoError(t, c.RestoreSnapshot(full, a, b))
	fullData := full.Data()
	require.Equal(t, fullData[0], memoryOf(a))
	require.Equal(t, fullData[1], memoryOf(b)[:pageSize])
	require.Equal(t, []byte("grown"), memoryOf(b)[pageSize+100:pageSize+105], "memory past the snapshot is unchanged")
}

func TestCoordinator_concurrent(t *testing.T) {
	r := newRuntime(t)
	a, b := instantiate(t, r, "a", 1), instantiate(t, r, "b", 1)

	require.True(t, a.Memory().WriteString(0, "state"))

	c := snapshot.NewCoordinator()
	base, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)
	expected := base.Data()

	type result struct {
		snap snapshot.Snapshot
		err  error
	}
	const n = 20
	results := make(chan result, 3*n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			snap, err := c.CaptureSnapshot(a, b)
			results <- result{snap, err}
		}()
		go func() {
			defer wg.Done()
			snap, err := c.CaptureIncremental(base, a, b)
			results <- result{snap, err}
		}()
		go func() {
			defer wg.Done()
			results <- result{err: c.RestoreSnapshot(base, a, b)}
		}()
	}
	wg.Wait()
	close(results)

	var versions []uint64
	for res := range results {
		require.NoError(t, res.err)
		if res.snap != nil {
			versions = append(versions, res.snap.Version())
			require.Equal(t, expected, res.snap.Data())
		}
	}
	slices.Sort(versions)
	expectedVersions := make([]uint64, 2*n)
	for i := range expectedVersions {
		expectedVersions[i] = uint64(i + 2)
	}
	require.Equal(t, expectedVersions, versions)
}
