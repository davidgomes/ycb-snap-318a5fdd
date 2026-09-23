package snapshot

import (
	"context"
	"errors"
	"sync"
)

var (
	errNoModules           = errors.New("no modules")
	errModuleClosed        = errors.New("module closed")
	errBaselineNil         = errors.New("baseline snapshot is nil")
	errModuleCountMismatch = errors.New("module count mismatch")
	errIncompatibleModule  = errors.New("incompatible module")
	errNilSnapshot         = errors.New("snapshot is nil")
)

type codedError struct {
	code string
	msg  string
}

func (e *codedError) Error() string { return e.msg }

func (e *codedError) ErrorCode() string { return e.code }

func insufficientMemory() error {
	return &codedError{code: "insufficient_memory", msg: "insufficient_memory"}
}

// ErrorCode returns a stable code for err, or "" when err is nil or has none.
// Restore into a memory smaller than the captured image yields "insufficient_memory".
func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var coder interface{ ErrorCode() string }
	if errors.As(err, &coder) {
		return coder.ErrorCode()
	}
	return ""
}

// SnapshotSummary describes the size and version of a snapshot.
type SnapshotSummary struct {
	TotalModules  int
	TotalBytes    uint64
	ModifiedBytes uint64
	Version       uint64
}

type modifiedCounter interface {
	modifiedBytes() uint64
}

// Summarize reports module count, reconstructed size, and version.
// ModifiedBytes is zero for a full snapshot and the changed-byte count for an incremental snapshot.
func Summarize(snap Snapshot) SnapshotSummary {
	if snap == nil {
		return SnapshotSummary{}
	}
	data := snap.Data()
	var total uint64
	for _, mem := range data {
		total += uint64(len(mem))
	}
	var modified uint64
	if mc, ok := snap.(modifiedCounter); ok {
		modified = mc.modifiedBytes()
	}
	return SnapshotSummary{
		TotalModules:  len(data),
		TotalBytes:    total,
		ModifiedBytes: modified,
		Version:       snap.Version(),
	}
}

// Chain is an oldest-first sequence of snapshots.
// All methods are safe for concurrent use.
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
	defer c.mu.Unlock()
	c.snaps = append(c.snaps, snap)
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

// Snapshots returns a copy of the chain from oldest to newest.
func (c *Chain) Snapshots() []Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Snapshot(nil), c.snaps...)
}

var (
	registryMu sync.RWMutex
	registry   = map[string]*Coordinator{}
)

// Register stores c under name, replacing any previous coordinator with that name.
// Register is safe for concurrent use.
func Register(name string, c *Coordinator) {
	registryMu.Lock()
	registry[name] = c
	registryMu.Unlock()
}

// Get returns the coordinator registered as name.
func Get(name string) (*Coordinator, bool) {
	registryMu.RLock()
	c, ok := registry[name]
	registryMu.RUnlock()
	return c, ok
}

// Unregister removes name from the registry. It is a no-op when name is absent.
func Unregister(name string) {
	registryMu.Lock()
	delete(registry, name)
	registryMu.Unlock()
}

type coordinatorKey struct{}

// WithCoordinator returns a context that carries c.
func WithCoordinator(ctx context.Context, c *Coordinator) context.Context {
	return context.WithValue(ctx, coordinatorKey{}, c)
}

// GetCoordinator returns the coordinator stored by WithCoordinator, or nil when absent.
func GetCoordinator(ctx context.Context) *Coordinator {
	c, _ := ctx.Value(coordinatorKey{}).(*Coordinator)
	return c
}
