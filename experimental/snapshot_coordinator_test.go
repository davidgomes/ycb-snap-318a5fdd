package experimental_test

import (
	"testing"

	"github.com/tetratelabs/wazero/experimental"
	"github.com/tetratelabs/wazero/experimental/snapshot"
	"github.com/tetratelabs/wazero/experimental/wazerotest"
	"github.com/tetratelabs/wazero/internal/testing/require"
)

func TestNewSnapshotCoordinator(t *testing.T) {
	c := experimental.NewSnapshotCoordinator()
	require.NotNil(t, c)
	mod := wazerotest.NewModule(&wazerotest.Memory{Bytes: []byte{4, 5, 6}})
	snap, err := c.CaptureSnapshot(mod)
	require.NoError(t, err)
	require.Equal(t, uint64(1), snap.Version())
	require.Equal(t, []byte{4, 5, 6}, snap.Data()[0])

	var _ *snapshot.Coordinator = c
}
