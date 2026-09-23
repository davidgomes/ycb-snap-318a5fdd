package experimental

import "github.com/tetratelabs/wazero/experimental/snapshot"

// NewSnapshotCoordinator returns a new snapshot.Coordinator.
func NewSnapshotCoordinator() *snapshot.Coordinator {
	return snapshot.NewCoordinator()
}
