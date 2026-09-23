package experimental_test

import (
	"testing"

	"github.com/tetratelabs/wazero/experimental"
	"github.com/tetratelabs/wazero/experimental/wazerotest"
	"github.com/tetratelabs/wazero/internal/testing/require"
)

func TestNewSnapshotCoordinator(t *testing.T) {
	c := experimental.NewSnapshotCoordinator()
	require.NotNil(t, c)
	mod := wazerotest.NewModule(wazerotest.NewMemory(1))
	snap, err := c.CaptureSnapshot(mod)
	require.NoError(t, err)
	require.Equal(t, uint64(1), snap.Version())
	require.Equal(t, 1, len(snap.Data()))
}
