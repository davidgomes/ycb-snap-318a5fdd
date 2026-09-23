package snapshot

import "sync"

// Chain is an ordered history of snapshots, such as a full snapshot followed
// by incrementals captured against it. It is safe for concurrent use.
type Chain struct {
	mu        sync.RWMutex
	snapshots []Snapshot
}

// NewChain returns an empty Chain.
func NewChain() *Chain {
	return &Chain{}
}

// Push appends snap to the chain.
func (c *Chain) Push(snap Snapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshots = append(c.snapshots, snap)
}

// Head returns the most recently pushed snapshot, or nil if empty.
func (c *Chain) Head() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.snapshots) == 0 {
		return nil
	}
	return c.snapshots[len(c.snapshots)-1]
}

// Len returns the number of snapshots in the chain.
func (c *Chain) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.snapshots)
}

// Snapshots returns a copy of the snapshots in the chain, oldest first.
func (c *Chain) Snapshots() []Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Snapshot{}, c.snapshots...)
}

// SnapshotSummary describes the size of a snapshot.
type SnapshotSummary struct {
	// TotalModules is the number of captured modules.
	TotalModules int
	// TotalBytes is the size of the fully reconstructed memory of all modules.
	TotalBytes uint64
	// ModifiedBytes is the number of bytes changed relative to the baseline of
	// an incremental snapshot, and zero for a full snapshot.
	ModifiedBytes uint64
	// Version is the version of the snapshot.
	Version uint64
}

// Summarize returns a summary of snap, or a zero summary if snap is nil.
func Summarize(snap Snapshot) SnapshotSummary {
	if isNil(snap) {
		return SnapshotSummary{}
	}
	data := dataOf(snap)
	s := SnapshotSummary{TotalModules: len(data), Version: snap.Version()}
	for _, d := range data {
		s.TotalBytes += uint64(len(d))
	}
	if in, ok := snap.(*snapshot); ok && in.incremental {
		s.ModifiedBytes = in.modifiedBytes
	}
	return s
}
