package snapshot

import "sync"

var (
	registryMu sync.RWMutex
	registry   = map[string]*Coordinator{}
)

// Register stores c under name, replacing any existing entry.
// Register is safe for concurrent use.
func Register(name string, c *Coordinator) {
	registryMu.Lock()
	registry[name] = c
	registryMu.Unlock()
}

// Get returns the coordinator registered as name.
// Get is safe for concurrent use.
func Get(name string) (*Coordinator, bool) {
	registryMu.RLock()
	c, ok := registry[name]
	registryMu.RUnlock()
	return c, ok
}

// Unregister removes name from the registry.
// Unregister is safe for concurrent use.
func Unregister(name string) {
	registryMu.Lock()
	delete(registry, name)
	registryMu.Unlock()
}
