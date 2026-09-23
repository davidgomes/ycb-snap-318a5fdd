package experimental_test

import (
	"context"
	"testing"

	"github.com/tetratelabs/wazero/experimental"
	"github.com/tetratelabs/wazero/experimental/snapshot"
	"github.com/tetratelabs/wazero/experimental/wazerotest"
	"github.com/tetratelabs/wazero/internal/testing/require"
)

func TestNewSnapshotCoordinator(t *testing.T) {
	c := experimental.NewSnapshotCoordinator()
	require.NotNil(t, c)

	mod := &wazerotest.Module{ExportMemory: &wazerotest.Memory{Bytes: []byte{1, 2, 3, 4}}}
	snap, err := c.CaptureSnapshot(mod)
	require.NoError(t, err)
	require.Equal(t, uint64(1), snap.Version())
	require.Equal(t, []byte{1, 2, 3, 4}, snap.Data()[0])

	ctx := snapshot.WithCoordinator(context.Background(), c)
	require.Equal(t, c, snapshot.GetCoordinator(ctx))
}
