package snapshot

import "sync"

var (
	registryMu sync.RWMutex
	registry   = map[string]*Coordinator{}
)

// Register stores c under name, replacing any coordinator already stored there.
// Register, Get, and Unregister are safe for concurrent use.
func Register(name string, c *Coordinator) {
	registryMu.Lock()
	registry[name] = c
	registryMu.Unlock()
}

// Get returns the coordinator stored under name.
func Get(name string) (*Coordinator, bool) {
	registryMu.RLock()
	c, ok := registry[name]
	registryMu.RUnlock()
	return c, ok
}

// Unregister removes name from the registry. It does nothing when name is absent.
func Unregister(name string) {
	registryMu.Lock()
	delete(registry, name)
	registryMu.Unlock()
}
