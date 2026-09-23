package snapshot

import "sync"

var (
	registryMu sync.RWMutex
	registry   = map[string]*Coordinator{}
)

// Register stores c under name, replacing any coordinator already registered
// with that name. Register is safe for concurrent use.
func Register(name string, c *Coordinator) {
	registryMu.Lock()
	registry[name] = c
	registryMu.Unlock()
}

// Get returns the coordinator registered under name.
func Get(name string) (*Coordinator, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	c, ok := registry[name]
	return c, ok
}

// Unregister removes name from the registry. Unregister is safe for concurrent use.
func Unregister(name string) {
	registryMu.Lock()
	delete(registry, name)
	registryMu.Unlock()
}
