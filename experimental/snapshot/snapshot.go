// Package snapshot captures, compares and restores the linear memory of
// multiple modules as one consistent unit, which helps debugging applications
// composed of several WebAssembly modules.
//
// Note: This is an experimental API and may change or be removed at any time.
package snapshot

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"reflect"
	"sync"

	"github.com/tetratelabs/wazero/api"
)

// DiffEntry is a single byte that differs between two snapshots.
type DiffEntry struct {
	// Offset is the position of the byte in the memory of its module.
	Offset uint32
	// OldValue is the byte in the snapshot Compare was called on.
	OldValue byte
	// NewValue is the byte in the snapshot passed to Compare.
	NewValue byte
}

// Snapshot is an immutable capture of the memory of one or more modules.
//
// Tags are the only mutable part of a Snapshot and are safe for concurrent
// use.
type Snapshot interface {
	// Data returns the fully reconstructed memory of each module, in capture
	// order. Each call returns an independent deep copy.
	Data() [][]byte

	// CompressedData returns a gzip-compressed representation of the
	// snapshot. For a full snapshot this is the gzip of Data() concatenated
	// in capture order. For an incremental snapshot this is the gzip of the
	// changes relative to its baseline.
	CompressedData() []byte

	// Version is the capture sequence number assigned by the Coordinator,
	// starting at 1.
	Version() uint64

	// Tags returns a copy of the tags attached to this snapshot.
	Tags() map[string]string

	// SetTag attaches or replaces a tag.
	SetTag(key, value string)

	// Compare returns the bytes that differ from this snapshot (OldValue) to
	// other (NewValue), grouped by module in capture order with offsets in
	// ascending order within each module. When memory sizes differ, missing
	// bytes compare as zero. Returns nil when other is nil.
	Compare(other Snapshot) []DiffEntry
}

type snapshot struct {
	version uint64
	data    [][]byte
	// modules are the captured modules, used to match restore targets by
	// identity. Nil for decoded snapshots.
	modules []api.Module

	incremental   bool
	delta         []byte
	modifiedBytes uint64

	compressOnce sync.Once
	compressed   []byte

	tagsMu sync.RWMutex
	tags   map[string]string
}

func (s *snapshot) Data() [][]byte {
	out := make([][]byte, len(s.data))
	for i, d := range s.data {
		out[i] = append([]byte{}, d...)
	}
	return out
}

func (s *snapshot) CompressedData() []byte {
	s.compressOnce.Do(func() {
		var buf bytes.Buffer
		if s.incremental {
			w, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
			_, _ = w.Write(s.delta)
			_ = w.Close()
		} else {
			w := gzip.NewWriter(&buf)
			for _, d := range s.data {
				_, _ = w.Write(d)
			}
			_ = w.Close()
		}
		s.compressed = buf.Bytes()
	})
	return append([]byte{}, s.compressed...)
}

func (s *snapshot) Version() uint64 {
	return s.version
}

func (s *snapshot) Tags() map[string]string {
	s.tagsMu.RLock()
	defer s.tagsMu.RUnlock()
	out := make(map[string]string, len(s.tags))
	for k, v := range s.tags {
		out[k] = v
	}
	return out
}

func (s *snapshot) SetTag(key, value string) {
	s.tagsMu.Lock()
	defer s.tagsMu.Unlock()
	if s.tags == nil {
		s.tags = map[string]string{}
	}
	s.tags[key] = value
}

func (s *snapshot) Compare(other Snapshot) []DiffEntry {
	if isNil(other) {
		return nil
	}
	return diff(s.data, dataOf(other))
}

// Coordinator captures and restores snapshots across multiple modules. All
// methods are safe for concurrent use.
type Coordinator struct {
	mu      sync.Mutex
	version uint64
}

// NewCoordinator returns a Coordinator whose first snapshot has version 1.
func NewCoordinator() *Coordinator {
	return &Coordinator{}
}

// CaptureSnapshot captures a full snapshot of the memory of all modules.
func (c *Coordinator) CaptureSnapshot(modules ...api.Module) (Snapshot, error) {
	if len(modules) == 0 {
		return nil, newError(CodeNoModules, "no modules to capture")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := checkOpen(modules); err != nil {
		return nil, err
	}
	data := make([][]byte, len(modules))
	for i, m := range modules {
		data[i] = readMemory(m)
	}
	c.version++
	return &snapshot{
		version: c.version,
		data:    data,
		modules: append([]api.Module{}, modules...),
	}, nil
}

// CaptureIncremental captures the memory of all modules, storing only the
// changes relative to baseline. The baseline may itself be incremental, and
// modules must be given in the same order as when baseline was captured.
func (c *Coordinator) CaptureIncremental(baseline Snapshot, modules ...api.Module) (Snapshot, error) {
	if isNil(baseline) {
		return nil, newError(CodeNilBaseline, "baseline snapshot is nil")
	}
	base := dataOf(baseline)
	if len(modules) != len(base) {
		if len(modules) == 0 {
			return nil, newError(CodeModuleCountMismatch,
				"module count mismatch: no modules provided, baseline has %d", len(base))
		}
		return nil, newError(CodeModuleCountMismatch,
			"module count mismatch: got %d modules, baseline has %d", len(modules), len(base))
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := checkOpen(modules); err != nil {
		return nil, err
	}
	data := make([][]byte, len(modules))
	for i, m := range modules {
		data[i] = readMemory(m)
	}
	delta, modified := encodeDelta(base, data)
	c.version++
	return &snapshot{
		version:       c.version,
		data:          data,
		modules:       append([]api.Module{}, modules...),
		incremental:   true,
		delta:         delta,
		modifiedBytes: modified,
	}, nil
}

// RestoreSnapshot writes the memory captured in snap back into modules. Any
// memory beyond the captured size is zeroed.
//
// Each module is matched to captured memory by identity. When as many
// modules are given as were captured, unmatched modules fall back to their
// position. Otherwise, unmatched modules are skipped. No memory is written
// unless every matched module can hold its captured memory.
func (c *Coordinator) RestoreSnapshot(snap Snapshot, modules ...api.Module) error {
	if isNil(snap) {
		return newError(CodeNilSnapshot, "snapshot is nil")
	}
	data := dataOf(snap)
	if len(modules) > len(data) {
		return newError(CodeIncompatibleModule,
			"incompatible module count: got %d modules, snapshot has %d", len(modules), len(data))
	}
	var captured []api.Module
	if in, ok := snap.(*snapshot); ok {
		captured = in.modules
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := checkOpen(modules); err != nil {
		return err
	}
	targets := matchModules(captured, modules, len(data))
	for i, m := range modules {
		j := targets[i]
		if j < 0 {
			continue
		}
		if size := memorySize(m); uint64(size) < uint64(len(data[j])) {
			return newError(CodeInsufficientMemory,
				"insufficient memory: module at index %d has %d bytes, snapshot requires %d", i, size, len(data[j]))
		}
	}
	for i, m := range modules {
		j := targets[i]
		if j < 0 {
			continue
		}
		mem := m.Memory()
		if mem == nil {
			continue
		}
		buf, _ := mem.Read(0, mem.Size())
		n := copy(buf, data[j])
		clear(buf[n:])
	}
	return nil
}

// matchModules returns, for each module, the index of the captured memory to
// restore into it, or -1 to skip it.
func matchModules(captured, modules []api.Module, count int) []int {
	targets := make([]int, len(modules))
	claimed := make([]bool, count)
	for i, m := range modules {
		targets[i] = -1
		for j, cm := range captured {
			if j < count && !claimed[j] && sameModule(cm, m) {
				targets[i] = j
				claimed[j] = true
				break
			}
		}
	}
	if len(modules) != count {
		return targets
	}
	for i := range modules {
		if targets[i] >= 0 {
			continue
		}
		j := i
		if claimed[j] {
			for j = 0; claimed[j]; j++ {
			}
		}
		targets[i] = j
		claimed[j] = true
	}
	return targets
}

func sameModule(a, b api.Module) bool {
	if a == nil || b == nil {
		return false
	}
	if t := reflect.TypeOf(a); t != reflect.TypeOf(b) || !t.Comparable() {
		return false
	}
	return a == b
}

func checkOpen(modules []api.Module) error {
	for i, m := range modules {
		if m == nil {
			return newError(CodeModuleClosed, "module closed: module at index %d is nil", i)
		}
		if m.IsClosed() {
			return newError(CodeModuleClosed, "module closed: module %q at index %d", m.Name(), i)
		}
	}
	return nil
}

func memorySize(m api.Module) uint32 {
	if mem := m.Memory(); mem != nil {
		return mem.Size()
	}
	return 0
}

func readMemory(m api.Module) []byte {
	mem := m.Memory()
	if mem == nil {
		return []byte{}
	}
	buf, _ := mem.Read(0, mem.Size())
	return append([]byte{}, buf...)
}

// isNil reports whether s is nil, including a nil *snapshot.
func isNil(s Snapshot) bool {
	if s == nil {
		return true
	}
	in, ok := s.(*snapshot)
	return ok && in == nil
}

// dataOf returns the memory of s without copying when s is internal. The
// result must not be modified.
func dataOf(s Snapshot) [][]byte {
	if in, ok := s.(*snapshot); ok {
		return in.data
	}
	return s.Data()
}

func byteAt(b []byte, i int) byte {
	if i < len(b) {
		return b[i]
	}
	return 0
}

func diff(old, cur [][]byte) []DiffEntry {
	var out []DiffEntry
	for i := 0; i < max(len(old), len(cur)); i++ {
		var o, n []byte
		if i < len(old) {
			o = old[i]
		}
		if i < len(cur) {
			n = cur[i]
		}
		for off := 0; off < max(len(o), len(n)); off++ {
			if ov, nv := byteAt(o, off), byteAt(n, off); ov != nv {
				out = append(out, DiffEntry{Offset: uint32(off), OldValue: ov, NewValue: nv})
			}
		}
	}
	return out
}

// mergeGap is the largest run of unchanged bytes folded into a surrounding
// changed run, as that is cheaper than the header of a new run.
const mergeGap = 3

// encodeDelta encodes the changes from base to cur, and returns the number of
// changed bytes. base and cur must have the same number of modules.
//
// The encoding is the module count, then per module: its size, the number of
// runs, and per run the gap since the previous run, its length and its bytes.
// All integers are uvarints.
func encodeDelta(base, cur [][]byte) ([]byte, uint64) {
	var modified uint64
	delta := binary.AppendUvarint(nil, uint64(len(cur)))
	for i, c := range cur {
		b := base[i]
		var runs [][2]int
		start, end := -1, -1
		for off := 0; off < len(c); off++ {
			if c[off] == byteAt(b, off) {
				continue
			}
			modified++
			if start >= 0 && off-end <= mergeGap {
				end = off + 1
				continue
			}
			if start >= 0 {
				runs = append(runs, [2]int{start, end})
			}
			start, end = off, off+1
		}
		if start >= 0 {
			runs = append(runs, [2]int{start, end})
		}
		for off := len(c); off < len(b); off++ {
			if b[off] != 0 {
				modified++
			}
		}

		delta = binary.AppendUvarint(delta, uint64(len(c)))
		delta = binary.AppendUvarint(delta, uint64(len(runs)))
		prev := 0
		for _, r := range runs {
			delta = binary.AppendUvarint(delta, uint64(r[0]-prev))
			delta = binary.AppendUvarint(delta, uint64(r[1]-r[0]))
			delta = append(delta, c[r[0]:r[1]]...)
			prev = r[1]
		}
	}
	return delta, modified
}
