package snapshot_test

import (
	"testing"

	"github.com/tetratelabs/wazero/experimental/snapshot"
	"github.com/tetratelabs/wazero/internal/testing/require"
)

func TestChain(t *testing.T) {
	chain := snapshot.NewChain()
	require.Nil(t, chain.Head())
	require.Equal(t, 0, chain.Len())
	require.Equal(t, 0, len(chain.Snapshots()))

	r := newRuntime(t)
	a := instantiate(t, r, "a", 1)
	c := snapshot.NewCoordinator()
	full, err := c.CaptureSnapshot(a)
	require.NoError(t, err)
	inc, err := c.CaptureIncremental(full, a)
	require.NoError(t, err)

	chain.Push(full)
	chain.Push(inc)
	require.Same(t, inc, chain.Head())
	require.Equal(t, 2, chain.Len())

	snaps := chain.Snapshots()
	require.Equal(t, []snapshot.Snapshot{full, inc}, snaps)
	snaps[0] = nil
	require.Equal(t, []snapshot.Snapshot{full, inc}, chain.Snapshots())
}
