package snapshot

import (
	"reflect"
	"slices"
	"sync"

	"github.com/tetratelabs/wazero/api"
)

// Coordinator captures and restores the memory of several modules as one
// unit, numbering the snapshots it captures.
//
// The zero value is ready to use, and all methods are safe for concurrent use.
type Coordinator struct {
	// mu makes each capture and restore atomic with respect to the others,
	// and orders versions by when memory was read.
	mu      sync.Mutex
	version uint64
}

// NewCoordinator returns a Coordinator whose first snapshot is version 1.
func NewCoordinator() *Coordinator {
	return &Coordinator{}
}

// CaptureSnapshot copies the memory of each module, in order. A module without
// memory is captured as empty.
func (c *Coordinator) CaptureSnapshot(mods ...api.Module) (Snapshot, error) {
	if len(mods) == 0 {
		return nil, errorf(CodeNoModules, "no modules to capture")
	}
	data, version, err := c.capture(mods)
	if err != nil {
		return nil, err
	}
	return newFullSnapshot(version, slices.Clone(mods), data), nil
}

// CaptureIncremental captures the memory of each module, storing only what
// changed since the corresponding module in baseline, which may itself be
// incremental. Modules must be given in the same order as in baseline.
func (c *Coordinator) CaptureIncremental(baseline Snapshot, mods ...api.Module) (Snapshot, error) {
	if isNil(baseline) {
		return nil, errorf(CodeNilSnapshot, "baseline snapshot is nil")
	}
	base := internalSnapshot(baseline)
	if n := base.numModules(); len(mods) != n {
		return nil, errorf(CodeModuleCountMismatch, "module count mismatch: baseline has %d, got %d", n, len(mods))
	}
	if len(mods) == 0 {
		return nil, errorf(CodeNoModules, "no modules to capture")
	}
	data, version, err := c.capture(mods)
	if err != nil {
		return nil, err
	}
	return newIncrementalSnapshot(version, slices.Clone(mods), base, data), nil
}

// capture copies the memory of mods and assigns it the next version.
func (c *Coordinator) capture(mods []api.Module) ([][]byte, uint64, error) {
	if err := checkModules(mods); err != nil {
		return nil, 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	data := make([][]byte, len(mods))
	for i, mod := range mods {
		data[i] = readMemory(mod.Memory())
	}
	c.version++
	return data, c.version, nil
}

// RestoreSnapshot writes the memory captured in snap back to mods.
//
// Each module receives the memory of the captured module it is identical to.
// When as many modules are given as snap holds, the rest receive the memory of
// the remaining captured modules in order; otherwise they are left unchanged.
// Memory past the end of what was captured is left unchanged.
//
// No module is written unless every module that will be is large enough.
func (c *Coordinator) RestoreSnapshot(snap Snapshot, mods ...api.Module) error {
	if isNil(snap) {
		return errorf(CodeNilSnapshot, "snapshot is nil")
	}
	s := internalSnapshot(snap)
	n := s.numModules()
	if len(mods) > n {
		return errorf(CodeIncompatibleModule, "incompatible module count: snapshot has %d, got %d", n, len(mods))
	}
	if err := checkModules(mods); err != nil {
		return err
	}
	sources := s.restoreSources(mods)
	if !slices.ContainsFunc(sources, func(j int) bool { return j >= 0 }) {
		return nil
	}
	data := s.reconstruct()

	c.mu.Lock()
	defer c.mu.Unlock()
	for i, j := range sources {
		if j < 0 {
			continue
		}
		if size, want := memorySize(mods[i].Memory()), uint64(len(data[j])); size < want {
			return errorf(CodeInsufficientMemory, "insufficient memory: module %q has %d bytes, snapshot needs %d",
				mods[i].Name(), size, want)
		}
	}
	for i, j := range sources {
		if j >= 0 && len(data[j]) > 0 {
			mods[i].Memory().Write(0, data[j])
		}
	}
	return nil
}

// restoreSources returns, for each of mods, the index of the captured module
// whose memory it receives, or -1 if none.
func (s *snapshot) restoreSources(mods []api.Module) []int {
	n := s.numModules()
	sources := make([]int, len(mods))
	used := make([]bool, n)
	for i, mod := range mods {
		sources[i] = -1
		for j, captured := range s.modules {
			if !used[j] && sameModule(captured, mod) {
				sources[i], used[j] = j, true
				break
			}
		}
	}
	if len(mods) != n {
		return sources
	}
	next := 0
	for i := range sources {
		if sources[i] >= 0 {
			continue
		}
		for used[next] {
			next++
		}
		sources[i], used[next] = next, true
	}
	return sources
}

func checkModules(mods []api.Module) error {
	for i, mod := range mods {
		if isNil(mod) {
			return errorf(CodeModuleClosed, "module closed: module at index %d is nil", i)
		}
		if mod.IsClosed() {
			return errorf(CodeModuleClosed, "module closed: %q at index %d", mod.Name(), i)
		}
	}
	return nil
}

// sameModule reports whether a and b are the same module instance, without
// panicking on implementations that are not comparable.
func sameModule(a, b api.Module) bool {
	t := reflect.TypeOf(a)
	return t != nil && t == reflect.TypeOf(b) && t.Comparable() && a == b
}

// maxRead is the most memory read at once, as api.Memory.Read cannot express
// the length of a 4GiB memory.
const maxRead = 1 << 30

// readMemory returns a copy of mem, which is empty if the module has no memory.
func readMemory(mem api.Memory) []byte {
	size := memorySize(mem)
	buf := make([]byte, size)
	for off := uint64(0); off < size; off += maxRead {
		view, _ := mem.Read(uint32(off), uint32(min(size-off, maxRead)))
		copy(buf[off:], view)
	}
	return buf
}

func memorySize(mem api.Memory) uint64 {
	if isNil(mem) {
		return 0
	}
	if size := mem.Size(); size != 0 {
		return uint64(size)
	}
	// Size overflows to zero for a 4GiB memory, but the page count does not.
	pages, _ := mem.Grow(0)
	return uint64(pages) << 16
}
