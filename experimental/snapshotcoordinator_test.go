package experimental_test

import (
	"testing"

	"github.com/tetratelabs/wazero/experimental"
	"github.com/tetratelabs/wazero/experimental/wazerotest"
	"github.com/tetratelabs/wazero/internal/testing/require"
)

func TestNewSnapshotCoordinator(t *testing.T) {
	mod := wazerotest.NewModule(wazerotest.NewMemory(wazerotest.PageSize))
	c1, c2 := experimental.NewSnapshotCoordinator(), experimental.NewSnapshotCoordinator()
	require.NotSame(t, c1, c2)

	snap, err := c1.CaptureSnapshot(mod)
	require.NoError(t, err)
	require.Equal(t, uint64(1), snap.Version())

	snap, err = c1.CaptureSnapshot(mod)
	require.NoError(t, err)
	require.Equal(t, uint64(2), snap.Version())

	snap, err = c2.CaptureSnapshot(mod)
	require.NoError(t, err)
	require.Equal(t, uint64(1), snap.Version(), "each coordinator numbers its own snapshots")
}
