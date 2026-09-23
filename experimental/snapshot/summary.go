package snapshot

// SnapshotSummary describes the size and version of a snapshot.
type SnapshotSummary struct {
	TotalModules  int
	TotalBytes    uint64
	ModifiedBytes uint64
	Version       uint64
}

// Summarize reports module count, reconstructed byte count, and version.
// ModifiedBytes is zero for full snapshots and the number of changed bytes
// for incremental snapshots.
func Summarize(snap Snapshot) SnapshotSummary {
	if snap == nil || isNil(snap) {
		return SnapshotSummary{}
	}
	data := snap.Data()
	var total uint64
	for _, d := range data {
		total += uint64(len(d))
	}
	var modified uint64
	if m, ok := snap.(interface{ modifiedBytes() uint64 }); ok {
		modified = m.modifiedBytes()
	}
	return SnapshotSummary{
		TotalModules:  len(data),
		TotalBytes:    total,
		ModifiedBytes: modified,
		Version:       snap.Version(),
	}
}
