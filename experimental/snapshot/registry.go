package snapshot

import (
	"context"
	"sync"
)

var registry = struct {
	sync.RWMutex
	coordinators map[string]*Coordinator
}{coordinators: map[string]*Coordinator{}}

// Register makes c available process-wide as name, replacing any Coordinator
// already registered as name.
func Register(name string, c *Coordinator) {
	registry.Lock()
	defer registry.Unlock()
	registry.coordinators[name] = c
}

// Get returns the Coordinator registered as name, if any.
func Get(name string) (*Coordinator, bool) {
	registry.RLock()
	defer registry.RUnlock()
	c, ok := registry.coordinators[name]
	return c, ok
}

// Unregister removes the Coordinator registered as name, if any.
func Unregister(name string) {
	registry.Lock()
	defer registry.Unlock()
	delete(registry.coordinators, name)
}

type coordinatorKey struct{}

// WithCoordinator returns a copy of ctx that carries c, for example so that
// host functions can capture snapshots via GetCoordinator.
func WithCoordinator(ctx context.Context, c *Coordinator) context.Context {
	return context.WithValue(ctx, coordinatorKey{}, c)
}

// GetCoordinator returns the Coordinator added by WithCoordinator, or nil.
func GetCoordinator(ctx context.Context) *Coordinator {
	c, _ := ctx.Value(coordinatorKey{}).(*Coordinator)
	return c
}
