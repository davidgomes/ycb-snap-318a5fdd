package snapshot_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental/snapshot"
	"github.com/tetratelabs/wazero/internal/testing/binaryencoding"
	"github.com/tetratelabs/wazero/internal/testing/require"
	"github.com/tetratelabs/wazero/internal/wasm"
)

func TestRealRuntimeModule(t *testing.T) {
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)

	bin := binaryencoding.EncodeModule(&wasm.Module{
		MemorySection: &wasm.Memory{Min: 1},
		ExportSection: []wasm.Export{{Name: "memory", Type: api.ExternTypeMemory}},
	})
	a, err := r.InstantiateWithConfig(ctx, bin, wazero.NewModuleConfig().WithName("a"))
	require.NoError(t, err)
	b, err := r.InstantiateWithConfig(ctx, bin, wazero.NewModuleConfig().WithName("b"))
	require.NoError(t, err)
	require.True(t, a.Memory().WriteByte(10, 1))
	require.True(t, b.Memory().WriteByte(20, 2))

	c := snapshot.NewCoordinator()
	snap, err := c.CaptureSnapshot(a, b)
	require.NoError(t, err)
	require.Equal(t, byte(1), snap.Data()[0][10])
	require.Equal(t, byte(2), snap.Data()[1][20])

	var raw []byte
	for _, part := range snap.Data() {
		raw = append(raw, part...)
	}
	require.Equal(t, gzipOf(raw), snap.CompressedData())

	require.True(t, a.Memory().WriteByte(10, 3))
	inc, err := c.CaptureIncremental(snap, a, b)
	require.NoError(t, err)
	require.Equal(t, byte(3), inc.Data()[0][10])
	require.Equal(t, byte(1), snap.Data()[0][10])
	require.True(t, len(inc.CompressedData()) < len(snap.CompressedData()))
	require.Equal(t, uint64(1), snapshot.Summarize(inc).ModifiedBytes)

	require.NoError(t, c.RestoreSnapshot(snap, b, a))
	v, ok := a.Memory().ReadByte(10)
	require.True(t, ok)
	require.Equal(t, byte(1), v)

	require.NoError(t, a.Close(ctx))
	_, err = c.CaptureSnapshot(a)
	require.Contains(t, err.Error(), "module closed")
}

func gzipOf(p []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(p)
	_ = w.Close()
	return buf.Bytes()
}
