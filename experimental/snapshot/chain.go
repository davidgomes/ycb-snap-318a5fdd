package snapshot

import "sync"

// Chain is an oldest-first sequence of snapshots.
type Chain struct {
	mu    sync.Mutex
	snaps []Snapshot
}

// NewChain returns an empty chain.
func NewChain() *Chain {
	return &Chain{}
}

// Push appends snap.
func (c *Chain) Push(snap Snapshot) {
	c.mu.Lock()
	c.snaps = append(c.snaps, snap)
	c.mu.Unlock()
}

// Head returns the most recently pushed snapshot, or nil when the chain is empty.
func (c *Chain) Head() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.snaps) == 0 {
		return nil
	}
	return c.snaps[len(c.snaps)-1]
}

// Len returns the number of snapshots in the chain.
func (c *Chain) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.snaps)
}

// Snapshots returns a copy of the chain, oldest first.
func (c *Chain) Snapshots() []Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Snapshot, len(c.snaps))
	copy(out, c.snaps)
	return out
}
