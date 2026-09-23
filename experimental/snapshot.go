package experimental

import "github.com/tetratelabs/wazero/experimental/snapshot"

// NewSnapshotCoordinator returns a coordinator for multi-module linear memory snapshots.
// It delegates to snapshot.NewCoordinator.
func NewSnapshotCoordinator() *snapshot.Coordinator {
	return snapshot.NewCoordinator()
}
