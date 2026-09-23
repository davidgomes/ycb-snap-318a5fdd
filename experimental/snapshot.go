package experimental

import "github.com/tetratelabs/wazero/experimental/snapshot"

// NewSnapshotCoordinator returns a coordinator for consistent multi-module
// linear-memory snapshots.
func NewSnapshotCoordinator() *snapshot.Coordinator {
	return snapshot.NewCoordinator()
}
