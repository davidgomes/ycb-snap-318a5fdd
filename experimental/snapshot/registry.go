package snapshot

import (
	"context"
	"sync"
)

type coordinatorKey struct{}

var (
	registryMu sync.RWMutex
	registry   = map[string]*Coordinator{}
)

// Register stores c under name, replacing any previous entry.
// The registry is safe for concurrent use.
func Register(name string, c *Coordinator) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = c
}

// Get returns the Coordinator registered under name, if any.
func Get(name string) (*Coordinator, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	c, ok := registry[name]
	return c, ok
}

// Unregister removes the Coordinator registered under name, if any.
func Unregister(name string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	delete(registry, name)
}

// WithCoordinator returns a child context that carries c.
func WithCoordinator(ctx context.Context, c *Coordinator) context.Context {
	return context.WithValue(ctx, coordinatorKey{}, c)
}

// GetCoordinator returns the Coordinator stored in ctx, or nil if absent.
func GetCoordinator(ctx context.Context) *Coordinator {
	c, _ := ctx.Value(coordinatorKey{}).(*Coordinator)
	return c
}
