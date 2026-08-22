package experimental_test

import (
	"context"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
	"github.com/tetratelabs/wazero/experimental/snapshot"
	"github.com/tetratelabs/wazero/experimental/wazerotest"
	"github.com/tetratelabs/wazero/internal/testing/binaryencoding"
	"github.com/tetratelabs/wazero/internal/testing/require"
	"github.com/tetratelabs/wazero/internal/wasm"
)

func TestNewSnapshotCoordinator(t *testing.T) {
	c := experimental.NewSnapshotCoordinator()
	require.NotNil(t, c)

	m := wazerotest.NewModule(&wazerotest.Memory{Bytes: []byte{1, 2, 3}})
	s, err := c.CaptureSnapshot(m)
	require.NoError(t, err)
	require.Equal(t, uint64(1), s.Version())
	require.Equal(t, [][]byte{{1, 2, 3}}, s.Data())

	var _ *snapshot.Coordinator = c
}

func TestSnapshotCoordinatorRealModule(t *testing.T) {
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	t.Cleanup(func() { _ = r.Close(ctx) })

	bin := binaryencoding.EncodeModule(&wasm.Module{
		MemorySection: &wasm.Memory{Min: 1},
		ExportSection: []wasm.Export{{Name: "memory", Type: api.ExternTypeMemory}},
	})
	mod, err := r.Instantiate(ctx, bin)
	require.NoError(t, err)

	require.True(t, mod.Memory().WriteByte(0, 42))
	require.True(t, mod.Memory().WriteByte(1, 43))

	c := experimental.NewSnapshotCoordinator()
	s, err := c.CaptureSnapshot(mod)
	require.NoError(t, err)
	require.Equal(t, byte(42), s.Data()[0][0])

	require.True(t, mod.Memory().WriteByte(0, 1))
	require.True(t, mod.Memory().WriteByte(1, 2))
	require.NoError(t, c.RestoreSnapshot(s, mod))

	v0, ok := mod.Memory().ReadByte(0)
	require.True(t, ok)
	v1, ok := mod.Memory().ReadByte(1)
	require.True(t, ok)
	require.Equal(t, byte(42), v0)
	require.Equal(t, byte(43), v1)

	inc, err := c.CaptureIncremental(s, mod)
	require.NoError(t, err)
	require.True(t, len(inc.CompressedData()) < len(s.CompressedData()))
}
