package experimental

import "github.com/tetratelabs/wazero/experimental/snapshot"

// NewSnapshotCoordinator creates a coordinator for multi-module memory
// snapshots.
func NewSnapshotCoordinator() *snapshot.Coordinator {
	return snapshot.NewCoordinator()
}
