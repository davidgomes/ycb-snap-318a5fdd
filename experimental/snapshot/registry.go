package snapshot

import (
	"context"
	"sync"
)

var registry sync.Map // map[string]*Coordinator

// Register stores c under name, replacing any existing entry.
func Register(name string, c *Coordinator) {
	registry.Store(name, c)
}

// Get returns the Coordinator registered under name.
func Get(name string) (*Coordinator, bool) {
	v, ok := registry.Load(name)
	if !ok {
		return nil, false
	}
	return v.(*Coordinator), true
}

// Unregister removes the Coordinator registered under name.
func Unregister(name string) {
	registry.Delete(name)
}

type coordinatorKey struct{}

// WithCoordinator returns a context carrying c.
func WithCoordinator(ctx context.Context, c *Coordinator) context.Context {
	return context.WithValue(ctx, coordinatorKey{}, c)
}

// GetCoordinator returns the Coordinator in ctx, or nil if absent.
func GetCoordinator(ctx context.Context) *Coordinator {
	c, _ := ctx.Value(coordinatorKey{}).(*Coordinator)
	return c
}
