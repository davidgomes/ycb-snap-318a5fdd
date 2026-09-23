package snapshot

import (
	"slices"
	"sync"
)

// Chain is a history of snapshots, such as a full snapshot followed by
// incremental snapshots of it. It is safe for concurrent use.
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

// Head returns the snapshot pushed last, or nil if the chain is empty.
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

// Snapshots returns the snapshots in the chain, oldest first.
func (c *Chain) Snapshots() []Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return slices.Clone(c.snapshots)
}
