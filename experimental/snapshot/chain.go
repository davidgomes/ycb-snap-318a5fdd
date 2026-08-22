package snapshot

// Chain is an ordered list of snapshots, oldest first.
type Chain struct {
	snaps []Snapshot
}

// NewChain returns an empty snapshot chain.
func NewChain() *Chain {
	return &Chain{}
}

// Push appends snap to the end of the chain.
func (c *Chain) Push(snap Snapshot) {
	c.snaps = append(c.snaps, snap)
}

// Head returns the most recently pushed snapshot, or nil when empty.
func (c *Chain) Head() Snapshot {
	if len(c.snaps) == 0 {
		return nil
	}
	return c.snaps[len(c.snaps)-1]
}

// Len returns the number of snapshots in the chain.
func (c *Chain) Len() int {
	return len(c.snaps)
}

// Snapshots returns a copy of the chain, oldest first.
func (c *Chain) Snapshots() []Snapshot {
	out := make([]Snapshot, len(c.snaps))
	copy(out, c.snaps)
	return out
}
