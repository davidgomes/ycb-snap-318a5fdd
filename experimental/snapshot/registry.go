package snapshot

import (
	"context"
	"sync"
)

var registry = struct {
	sync.RWMutex
	coordinators map[string]*Coordinator
}{coordinators: map[string]*Coordinator{}}

// Register adds c to the global registry under name, replacing any existing
// entry.
func Register(name string, c *Coordinator) {
	registry.Lock()
	defer registry.Unlock()
	registry.coordinators[name] = c
}

// Get returns the Coordinator registered under name.
func Get(name string) (*Coordinator, bool) {
	registry.RLock()
	defer registry.RUnlock()
	c, ok := registry.coordinators[name]
	return c, ok
}

// Unregister removes the Coordinator registered under name, if any.
func Unregister(name string) {
	registry.Lock()
	defer registry.Unlock()
	delete(registry.coordinators, name)
}

type coordinatorKey struct{}

// WithCoordinator returns a context carrying c.
func WithCoordinator(ctx context.Context, c *Coordinator) context.Context {
	return context.WithValue(ctx, coordinatorKey{}, c)
}

// GetCoordinator returns the Coordinator carried by ctx, or nil if absent.
func GetCoordinator(ctx context.Context) *Coordinator {
	if ctx == nil {
		return nil
	}
	c, _ := ctx.Value(coordinatorKey{}).(*Coordinator)
	return c
}
