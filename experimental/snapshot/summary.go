package snapshot

// SnapshotSummary describes the size of a Snapshot.
type SnapshotSummary struct {
	// TotalModules is the number of modules captured.
	TotalModules int
	// TotalBytes is the length of the memory of all modules, as returned by
	// Snapshot.Data.
	TotalBytes uint64
	// ModifiedBytes is the number of bytes that changed since the baseline of
	// an incremental snapshot, and zero for a full snapshot.
	ModifiedBytes uint64
	// Version is Snapshot.Version.
	Version uint64
}

// Summarize returns a summary of snap, or the zero value if snap is nil.
func Summarize(snap Snapshot) SnapshotSummary {
	if isNil(snap) {
		return SnapshotSummary{}
	}
	s := internalSnapshot(snap)
	return SnapshotSummary{
		TotalModules:  s.numModules(),
		TotalBytes:    s.totalBytes(),
		ModifiedBytes: s.modifiedBytes(),
		Version:       s.version,
	}
}
