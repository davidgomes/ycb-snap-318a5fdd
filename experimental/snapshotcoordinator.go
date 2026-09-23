package experimental

import "github.com/tetratelabs/wazero/experimental/snapshot"

// NewSnapshotCoordinator returns a snapshot.Coordinator, which captures and
// restores the linear memory of several modules together.
//
// Unlike Snapshotter, this does not capture the execution stack.
func NewSnapshotCoordinator() *snapshot.Coordinator {
	return snapshot.NewCoordinator()
}
