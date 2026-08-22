package experimental

import "github.com/tetratelabs/wazero/experimental/snapshot"

func NewSnapshotCoordinator() *snapshot.Coordinator {
	return snapshot.NewCoordinator()
}
