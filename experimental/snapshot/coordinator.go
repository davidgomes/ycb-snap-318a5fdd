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
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(mods) == 0 {
		return nil, errors.New("no modules")
	}
	data, captured, err := copyModules(mods)
	if err != nil {
		return nil, err
	}
	c.version++
	return newFullSnapshot(c.version, data, captured), nil
}

// CaptureIncremental copies current linear memory and records a compact
// incremental snapshot against baseline. baseline may itself be incremental.
//
// It returns an error containing "baseline snapshot is nil" when baseline is
// nil, and "module count mismatch" when the number of modules differs from
// the baseline.
func (c *Coordinator) CaptureIncremental(baseline Snapshot, mods ...api.Module) (Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if baseline == nil {
		return nil, errors.New("baseline snapshot is nil")
	}
	baseData := baseline.Data()
	if len(mods) != len(baseData) {
		return nil, errors.New("module count mismatch")
	}
	data, captured, err := copyModules(mods)
	if err != nil {
		return nil, err
	}
	modified := countModified(baseData, data)
	c.version++
	return newIncrementalSnapshot(c.version, data, captured, baseline, modified), nil
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
	c.mu.Lock()
	defer c.mu.Unlock()

	if snap == nil {
		return errors.New("snapshot is nil")
	}
	data := snap.Data()
	if len(mods) > len(data) {
		return errors.New("incompatible module")
	}
	captured := snapshotModules(snap)
	assignments := matchModules(captured, mods, len(data))
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

func snapshotModules(snap Snapshot) []api.Module {
	if s, ok := snap.(*snapshot); ok {
		return s.modules
	}
	return nil
}

// matchModules maps each provided module to a snapshot slot.
// A negative index means the module is unmatched and should be skipped.
func matchModules(captured, provided []api.Module, snapCount int) []int {
	assignments := make([]int, len(provided))
	used := make([]bool, snapCount)
	allIdentity := len(provided) > 0

	for i, m := range provided {
		assignments[i] = -1
		if m == nil {
			allIdentity = false
			continue
		}
		for j := 0; j < snapCount; j++ {
			if used[j] {
				continue
			}
			if j < len(captured) && captured[j] != nil && captured[j] == m {
				assignments[i] = j
				used[j] = true
				break
			}
		}
		if assignments[i] < 0 {
			allIdentity = false
		}
	}

	if len(provided) == snapCount && !allIdentity {
		for i := range provided {
			assignments[i] = i
		}
	}
	return assignments
}

func copyModules(mods []api.Module) (data [][]byte, captured []api.Module, err error) {
	data = make([][]byte, len(mods))
	captured = make([]api.Module, len(mods))
	for i, m := range mods {
		b, copyErr := readMemory(m)
		if copyErr != nil {
			return nil, nil, copyErr
		}
		data[i] = b
		captured[i] = m
	}
	return data, captured, nil
}

func readMemory(m api.Module) ([]byte, error) {
	if m == nil || m.IsClosed() {
		return nil, errors.New("module closed")
	}
	mem := m.Memory()
	if mem == nil {
		return []byte{}, nil
	}
	size := mem.Size()
	if size == 0 {
		pages, _ := mem.Grow(0)
		if pages == 0 || pages >= 65536 {
			return []byte{}, nil
		}
		size = pages * 65536
	}
	buf, ok := mem.Read(0, size)
	if !ok {
		return []byte{}, nil
	}
	return append([]byte{}, buf...), nil
}

func writeMemory(m api.Module, data []byte) error {
	if m == nil || m.IsClosed() {
		return errors.New("module closed")
	}
	if len(data) == 0 {
		return nil
	}
	mem := m.Memory()
	if mem == nil || uint64(mem.Size()) < uint64(len(data)) {
		return newCodedError("insufficient_memory", "insufficient_memory")
	}
	if !mem.Write(0, data) {
		return newCodedError("insufficient_memory", "insufficient_memory")
	}
	return nil
}
