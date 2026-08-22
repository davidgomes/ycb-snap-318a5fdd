package snapshot

import (
	"errors"
	"sync"

	"github.com/tetratelabs/wazero/api"
)

// Coordinator captures and restores linear memory across one or more modules.
// All methods are safe for concurrent use.
type Coordinator struct {
	mu      sync.Mutex
	version uint64
}

// NewCoordinator returns an empty Coordinator. The first successful capture
// receives version 1.
func NewCoordinator() *Coordinator {
	return &Coordinator{}
}

// CaptureSnapshot copies the current linear memory of each module.
//
// It returns an error containing "no modules" when mods is empty, and
// "module closed" when any module is nil or closed.
func (c *Coordinator) CaptureSnapshot(mods ...api.Module) (Snapshot, error) {
	if len(mods) == 0 {
		return nil, errors.New("no modules")
	}
	if err := validateModules(mods); err != nil {
		return nil, err
	}
	data, err := readAll(mods)
	if err != nil {
		return nil, err
	}
	return newFullSnapshot(c.nextVersion(), data, mods), nil
}

// CaptureIncremental copies current linear memory and records a compact
// incremental snapshot against baseline. baseline may itself be incremental.
//
// It returns an error containing "baseline snapshot is nil" when baseline is
// nil, and "module count mismatch" when the number of modules differs from
// the baseline.
func (c *Coordinator) CaptureIncremental(baseline Snapshot, mods ...api.Module) (Snapshot, error) {
	if baseline == nil {
		return nil, errors.New("baseline snapshot is nil")
	}
	baseData := baseline.Data()
	if len(mods) != len(baseData) {
		return nil, errors.New("module count mismatch")
	}
	if err := validateModules(mods); err != nil {
		return nil, err
	}
	data, err := readAll(mods)
	if err != nil {
		return nil, err
	}
	modified := countModified(baseData, data)
	return newIncrementalSnapshot(c.nextVersion(), data, mods, baseline, modified), nil
}

// RestoreSnapshot writes snapshot memory back into the provided modules.
//
// Matching prefers reference identity with the modules that were captured.
// When the number of restore targets equals the snapshot module count,
// unmatched targets fall back to positional order. When fewer modules are
// provided, only identity matching is used; unmatched modules are skipped.
// Passing more modules than were captured returns an error containing
// "incompatible module".
func (c *Coordinator) RestoreSnapshot(snap Snapshot, mods ...api.Module) error {
	if snap == nil {
		if len(mods) > 0 {
			return errors.New("incompatible module")
		}
		return nil
	}
	data := snap.Data()
	captured := snapshotModules(snap, len(data))
	if len(mods) > len(data) {
		return errors.New("incompatible module")
	}
	assignments := matchModules(captured, mods)
	for i, snapIdx := range assignments {
		if snapIdx < 0 {
			continue
		}
		if err := writeMemory(mods[i], data[snapIdx]); err != nil {
			return err
		}
	}
	return nil
}

func (c *Coordinator) nextVersion() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.version++
	return c.version
}

func snapshotModules(snap Snapshot, n int) []api.Module {
	if s, ok := snap.(*snapshot); ok && len(s.modules) == n {
		return s.modules
	}
	return make([]api.Module, n)
}

func matchModules(captured, mods []api.Module) []int {
	assignments := make([]int, len(mods))
	for i := range assignments {
		assignments[i] = -1
	}
	used := make([]bool, len(captured))

	for i, mod := range mods {
		for j, cap := range captured {
			if used[j] || !sameModule(mod, cap) {
				continue
			}
			assignments[i] = j
			used[j] = true
			break
		}
	}

	if len(mods) != len(captured) {
		return assignments
	}
	for i := range mods {
		if assignments[i] != -1 || used[i] {
			continue
		}
		assignments[i] = i
		used[i] = true
	}
	return assignments
}

func sameModule(a, b api.Module) bool {
	return a != nil && b != nil && a == b
}

func validateModules(mods []api.Module) error {
	for _, m := range mods {
		if m == nil || m.IsClosed() {
			return errors.New("module closed")
		}
	}
	return nil
}

func readAll(mods []api.Module) ([][]byte, error) {
	data := make([][]byte, len(mods))
	for i, m := range mods {
		copied, err := readMemory(m)
		if err != nil {
			return nil, err
		}
		data[i] = copied
	}
	return data, nil
}

func readMemory(m api.Module) ([]byte, error) {
	mem := m.Memory()
	if mem == nil {
		return []byte{}, nil
	}
	size := mem.Size()
	if size == 0 {
		return []byte{}, nil
	}
	buf, ok := mem.Read(0, size)
	if !ok {
		return nil, errors.New("failed to read module memory")
	}
	return append([]byte{}, buf...), nil
}

func writeMemory(m api.Module, data []byte) error {
	if m == nil {
		return errors.New("module closed")
	}
	if len(data) == 0 {
		return nil
	}
	mem := m.Memory()
	if mem == nil || uint64(mem.Size()) < uint64(len(data)) {
		return newCodedError("insufficient_memory", "insufficient memory")
	}
	if !mem.Write(0, data) {
		return newCodedError("insufficient_memory", "insufficient memory")
	}
	return nil
}
