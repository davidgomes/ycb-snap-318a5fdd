package snapshot

// SnapshotSummary is a compact description of a snapshot's reconstructed memory.
type SnapshotSummary struct {
	TotalModules  int
	TotalBytes    uint64
	ModifiedBytes uint64
	Version       uint64
}

// Summarize reports reconstructed-memory totals for snap.
//
// TotalModules is the captured module count. TotalBytes is the total number of
// reconstructed bytes. ModifiedBytes is zero for full snapshots and the
// changed-byte count for incrementals. Version matches snap.Version.
func Summarize(snap Snapshot) SnapshotSummary {
	if snap == nil {
		return SnapshotSummary{}
	}
	data := snap.Data()
	var total uint64
	for _, d := range data {
		total += uint64(len(d))
	}
	var modified uint64
	if s, ok := snap.(*snapshot); ok && s.incremental {
		modified = s.modified
	}
	return SnapshotSummary{
		TotalModules:  len(data),
		TotalBytes:    total,
		ModifiedBytes: modified,
		Version:       snap.Version(),
	}
}
