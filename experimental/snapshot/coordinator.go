package snapshot

import (
	"sync"

	"github.com/tetratelabs/wazero/api"
)

// Coordinator captures and restores memory snapshots for a set of modules.
// All methods are safe for concurrent use. Versions start at 1 and increase
// by one across CaptureSnapshot and CaptureIncremental with no gaps.
type Coordinator struct {
	mu      sync.Mutex
	version uint64
}

// NewCoordinator returns a coordinator whose first successful capture is version 1.
func NewCoordinator() *Coordinator {
	return &Coordinator{version: 1}
}

// CaptureSnapshot records the full linear memory of each module.
// An empty argument list returns an error containing "no modules".
// A nil or closed module returns an error containing "module closed".
func (c *Coordinator) CaptureSnapshot(modules ...api.Module) (Snapshot, error) {
	if len(modules) == 0 {
		return nil, errNoModules
	}
	data, mods, err := readModules(modules)
	if err != nil {
		return nil, err
	}
	compressed, err := gzipBytes(concatData(data))
	if err != nil {
		return nil, err
	}
	return newSnapshot(data, mods, c.nextVersion(), compressed, false, 0), nil
}

// CaptureIncremental records memory relative to baseline, which may itself be
// incremental. Data on the result is fully reconstructed.
// A nil baseline returns an error containing "baseline snapshot is nil".
// A different module count returns an error containing "module count mismatch".
func (c *Coordinator) CaptureIncremental(baseline Snapshot, modules ...api.Module) (Snapshot, error) {
	if baseline == nil || isNil(baseline) {
		return nil, errBaselineNil
	}
	baseData := baseline.Data()
	if len(modules) != len(baseData) {
		return nil, errModuleCountMismatch
	}
	data, mods, err := readModules(modules)
	if err != nil {
		return nil, err
	}
	modified := countModified(baseData, data)
	compressed, err := compressIncremental(encodeDelta(baseData, data), len(baseline.CompressedData()))
	if err != nil {
		return nil, err
	}
	return newSnapshot(data, mods, c.nextVersion(), compressed, true, modified), nil
}

// RestoreSnapshot writes snap back into the given modules.
// Matching prefers reference identity (the same module pointer that was
// captured). When the restore count equals the snapshot module count and a
// module was not captured, that slot is restored by position. Fewer modules
// are matched by identity only; unmatched modules are skipped and a complete
// miss still returns nil. More modules than were captured returns an error
// containing "incompatible module". A restore target that cannot hold the
// captured bytes fails with ErrorCode "insufficient_memory".
func (c *Coordinator) RestoreSnapshot(snap Snapshot, modules ...api.Module) error {
	if snap == nil || isNil(snap) {
		return errSnapshotNil
	}
	data := snap.Data()
	if len(modules) > len(data) {
		return errIncompatibleModule
	}
	for _, m := range modules {
		if err := validateModule(m); err != nil {
			return err
		}
	}
	indexes := matchRestore(snapshotModules(snap), len(data), modules)
	type op struct {
		mod  api.Module
		data []byte
	}
	ops := make([]op, 0, len(modules))
	for i, mod := range modules {
		src := indexes[i]
		if src < 0 {
			continue
		}
		payload := data[src]
		if err := memoryFits(mod, payload); err != nil {
			return err
		}
		ops = append(ops, op{mod: mod, data: payload})
	}
	for _, op := range ops {
		if err := writeModuleMemory(op.mod, op.data); err != nil {
			return err
		}
	}
	return nil
}

func (c *Coordinator) nextVersion() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := c.version
	c.version++
	return v
}

func snapshotModules(snap Snapshot) []api.Module {
	s, ok := snap.(*memSnapshot)
	if !ok || s == nil {
		return nil
	}
	return s.modules
}

// matchRestore returns a data index for each target, or -1 when the target
// is skipped. Identity wins over position for a given target. Position is
// used only when the counts are equal and the target is not a captured module.
func matchRestore(captured []api.Module, dataLen int, targets []api.Module) []int {
	out := make([]int, len(targets))
	for i := range out {
		out[i] = -1
	}
	id := make(map[api.Module]int, len(captured))
	for j, m := range captured {
		if m == nil || isNil(m) {
			continue
		}
		if _, exists := id[m]; !exists {
			id[m] = j
		}
	}
	equalCount := len(targets) == dataLen
	for i, m := range targets {
		if j, ok := id[m]; ok && j < dataLen {
			out[i] = j
			continue
		}
		if equalCount {
			out[i] = i
		}
	}
	return out
}
