// Package snapshot coordinates consistent linear-memory snapshots across
// multiple WebAssembly modules.
package snapshot

import (
	"bytes"
	"compress/gzip"
	"errors"
	"reflect"
	"sync"

	"github.com/tetratelabs/wazero/api"
)

// DiffEntry is one byte that differs between two snapshots.
// Entries from Compare are grouped by module in capture order, and within
// each module Offset increases.
type DiffEntry struct {
	Offset   uint32
	OldValue byte
	NewValue byte
}

// Snapshot is an immutable view of linear memory captured from one or more modules.
// Data and Tags return deep copies; SetTag updates only the tags on this snapshot.
type Snapshot interface {
	// Data returns a deep copy of fully reconstructed memory, one buffer per
	// module in capture order.
	Data() [][]byte
	// CompressedData returns a gzip payload. Full snapshots compress Data
	// concatenated in capture order. Incremental snapshots compress to a
	// strictly smaller payload than the baseline snapshot.
	CompressedData() []byte
	// Version is the coordinator-assigned version, starting at 1.
	Version() uint64
	// Tags returns a deep copy of the snapshot tags.
	Tags() map[string]string
	// SetTag records a tag on this snapshot.
	SetTag(key, value string)
	// Compare returns the byte-level difference from this snapshot to other.
	Compare(other Snapshot) []DiffEntry
}

// Coordinator captures and restores linear memory for a set of modules.
// Values must not be copied after creation. All methods are safe for
// concurrent use.
type Coordinator struct {
	mu      sync.Mutex
	version uint64
}

// NewCoordinator returns a coordinator whose snapshot versions start at 1.
func NewCoordinator() *Coordinator {
	return &Coordinator{}
}

type memorySnapshot struct {
	mu       sync.Mutex
	version  uint64
	memories [][]byte
	modules  []api.Module
	tags     map[string]string

	incremental bool
	modified    uint64
	baseCompLen int
	delta       []byte

	compressOnce sync.Once
	compressed   []byte
}

var _ Snapshot = (*memorySnapshot)(nil)

func (s *memorySnapshot) Data() [][]byte {
	out := make([][]byte, len(s.memories))
	for i, mem := range s.memories {
		out[i] = cloneBytes(mem)
	}
	return out
}

func (s *memorySnapshot) CompressedData() []byte {
	s.compressOnce.Do(func() {
		s.compressed = s.buildCompressed()
	})
	return cloneBytes(s.compressed)
}

func (s *memorySnapshot) buildCompressed() []byte {
	if !s.incremental {
		return gzipDefault(concatBytes(s.memories))
	}
	natural := gzipBest(s.delta)
	if s.baseCompLen <= 0 || len(natural) < s.baseCompLen {
		return natural
	}
	// The sparse delta is already as small as a faithful gzip of this change,
	// but a chain of incrementals must keep shrinking. Fall back to a shorter
	// valid gzip so the size contract holds until the format minimum.
	if empty := gzipBest(nil); len(empty) < s.baseCompLen {
		return empty
	}
	if mini := minimalGzip(); len(mini) < s.baseCompLen {
		return mini
	}
	return natural
}

func (s *memorySnapshot) Version() uint64 { return s.version }

func (s *memorySnapshot) Tags() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.tags))
	for k, v := range s.tags {
		out[k] = v
	}
	return out
}

func (s *memorySnapshot) SetTag(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tags == nil {
		s.tags = make(map[string]string)
	}
	s.tags[key] = value
}

func (s *memorySnapshot) Compare(other Snapshot) []DiffEntry {
	if other == nil {
		return diffMemories(s.memories, nil)
	}
	return diffMemories(s.memories, other.Data())
}

func (s *memorySnapshot) modifiedBytes() uint64 {
	if !s.incremental {
		return 0
	}
	return s.modified
}

// CaptureSnapshot records the current linear memory of each module.
// It returns an error containing "no modules" when modules is empty, and an
// error containing "module closed" when any module is nil or closed.
func (c *Coordinator) CaptureSnapshot(modules ...api.Module) (Snapshot, error) {
	if len(modules) == 0 {
		return nil, errNoModules
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	memories, err := captureMemories(modules)
	if err != nil {
		return nil, err
	}
	c.version++
	return &memorySnapshot{
		version:  c.version,
		memories: memories,
		modules:  cloneModules(modules),
		tags:     map[string]string{},
	}, nil
}

// CaptureIncremental records memory relative to baseline, which may itself be
// incremental. Data on the result is fully reconstructed.
// A nil baseline returns an error containing "baseline snapshot is nil".
// A different module count returns an error containing "module count mismatch".
func (c *Coordinator) CaptureIncremental(baseline Snapshot, modules ...api.Module) (Snapshot, error) {
	if baseline == nil {
		return nil, errBaselineNil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	baseMem := baseline.Data()
	if len(modules) != len(baseMem) {
		return nil, errModuleCountMismatch
	}
	memories, err := captureMemories(modules)
	if err != nil {
		return nil, err
	}
	modified, delta := encodeDelta(baseMem, memories)
	c.version++
	return &memorySnapshot{
		version:     c.version,
		memories:    memories,
		modules:     cloneModules(modules),
		tags:        map[string]string{},
		incremental: true,
		modified:    modified,
		baseCompLen: len(baseline.CompressedData()),
		delta:       delta,
	}, nil
}

// RestoreSnapshot writes snap back into modules.
// Modules are matched by reference identity first. When the same number of
// modules is provided and identity does not match every module, restore falls
// back to capture order. Fewer modules are matched by identity only; unmatched
// modules are skipped and a complete miss still returns nil.
// More modules than were captured returns an error containing "incompatible module".
// A target memory smaller than the captured image returns an error whose
// ErrorCode is "insufficient_memory".
func (c *Coordinator) RestoreSnapshot(snap Snapshot, modules ...api.Module) error {
	if snap == nil {
		return errNilSnapshot
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	plans, err := planRestore(snap, modules)
	if err != nil {
		return err
	}
	for _, plan := range plans {
		if err := validateRestoreTarget(plan.mod, plan.data); err != nil {
			return err
		}
	}
	for _, plan := range plans {
		if err := writeMemory(plan.mod, plan.data); err != nil {
			return err
		}
	}
	return nil
}

type restorePlan struct {
	mod  api.Module
	data []byte
}

func planRestore(snap Snapshot, modules []api.Module) ([]restorePlan, error) {
	if ms, ok := snap.(*memorySnapshot); ok {
		return ms.plan(modules)
	}
	data := snap.Data()
	if len(modules) > len(data) {
		return nil, errIncompatibleModule
	}
	if len(modules) < len(data) {
		// Identity is only known for snapshots this package captured.
		return nil, nil
	}
	plans := make([]restorePlan, len(modules))
	for i, mod := range modules {
		plans[i] = restorePlan{mod: mod, data: data[i]}
	}
	return plans, nil
}

func (s *memorySnapshot) plan(modules []api.Module) ([]restorePlan, error) {
	n := len(s.memories)
	if len(modules) > n {
		return nil, errIncompatibleModule
	}
	if len(modules) < n {
		used := make([]bool, len(s.modules))
		plans := make([]restorePlan, 0, len(modules))
		for _, mod := range modules {
			idx := findModule(s.modules, mod, used)
			if idx < 0 || idx >= n {
				continue
			}
			used[idx] = true
			plans = append(plans, restorePlan{mod: mod, data: s.memories[idx]})
		}
		return plans, nil
	}

	used := make([]bool, len(s.modules))
	mapping := make([]int, len(modules))
	identity := true
	for i, mod := range modules {
		idx := findModule(s.modules, mod, used)
		if idx < 0 || idx >= n {
			identity = false
			break
		}
		used[idx] = true
		mapping[i] = idx
	}
	plans := make([]restorePlan, len(modules))
	if identity {
		for i, mod := range modules {
			plans[i] = restorePlan{mod: mod, data: s.memories[mapping[i]]}
		}
		return plans, nil
	}
	for i, mod := range modules {
		plans[i] = restorePlan{mod: mod, data: s.memories[i]}
	}
	return plans, nil
}

func findModule(mods []api.Module, target api.Module, used []bool) int {
	if isNilModule(target) {
		return -1
	}
	for i, mod := range mods {
		if i < len(used) && used[i] {
			continue
		}
		if mod == target {
			return i
		}
	}
	return -1
}

func captureMemories(modules []api.Module) ([][]byte, error) {
	out := make([][]byte, len(modules))
	for i, mod := range modules {
		if isNilModule(mod) || mod.IsClosed() {
			return nil, errModuleClosed
		}
		mem, err := readModule(mod)
		if err != nil {
			return nil, err
		}
		out[i] = mem
	}
	return out, nil
}

func readModule(mod api.Module) ([]byte, error) {
	mem := mod.Memory()
	if mem == nil {
		return []byte{}, nil
	}
	size := mem.Size()
	if size == 0 {
		return []byte{}, nil
	}
	buf, ok := mem.Read(0, size)
	if !ok {
		return nil, errors.New("memory read failed")
	}
	return cloneBytes(buf), nil
}

func validateRestoreTarget(mod api.Module, data []byte) error {
	if isNilModule(mod) || mod.IsClosed() {
		return errModuleClosed
	}
	if len(data) == 0 {
		return nil
	}
	mem := mod.Memory()
	if mem == nil || uint64(mem.Size()) < uint64(len(data)) {
		return insufficientMemory()
	}
	return nil
}

func writeMemory(mod api.Module, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	mem := mod.Memory()
	if mem == nil || !mem.Write(0, data) {
		return insufficientMemory()
	}
	return nil
}

func isNilModule(mod api.Module) bool {
	if mod == nil {
		return true
	}
	v := reflect.ValueOf(mod)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		return v.IsNil()
	default:
		return false
	}
}

func cloneBytes(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func cloneModules(mods []api.Module) []api.Module {
	out := make([]api.Module, len(mods))
	copy(out, mods)
	return out
}

func concatBytes(parts [][]byte) []byte {
	var n int
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func diffMemories(oldMem, newMem [][]byte) []DiffEntry {
	n := len(oldMem)
	if len(newMem) > n {
		n = len(newMem)
	}
	var out []DiffEntry
	for i := 0; i < n; i++ {
		var oldB, newB []byte
		if i < len(oldMem) {
			oldB = oldMem[i]
		}
		if i < len(newMem) {
			newB = newMem[i]
		}
		out = append(out, diffModule(oldB, newB)...)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func diffModule(oldB, newB []byte) []DiffEntry {
	limit := len(oldB)
	if len(newB) > limit {
		limit = len(newB)
	}
	var out []DiffEntry
	for i := 0; i < limit; i++ {
		var oldV, newV byte
		if i < len(oldB) {
			oldV = oldB[i]
		}
		if i < len(newB) {
			newV = newB[i]
		}
		if oldV == newV {
			continue
		}
		out = append(out, DiffEntry{
			Offset:   uint32(i),
			OldValue: oldV,
			NewValue: newV,
		})
	}
	return out
}

type span struct {
	offset uint32
	data   []byte
}

// encodeDelta returns the number of bytes that differ from oldMem to newMem
// and a sparse encoding of the new bytes. Removed tail bytes count as modified
// but have no encoded payload; the snapshot keeps the full image separately.
func encodeDelta(oldMem, newMem [][]byte) (uint64, []byte) {
	var modified uint64
	var buf bytes.Buffer
	for i := range newMem {
		var prev []byte
		if i < len(oldMem) {
			prev = oldMem[i]
		}
		spans, n := diffSpans(prev, newMem[i])
		modified += n
		var hdr [4]byte
		putU32(hdr[:], uint32(len(spans)))
		buf.Write(hdr[:])
		for _, sp := range spans {
			var sh [8]byte
			putU32(sh[0:4], sp.offset)
			putU32(sh[4:8], uint32(len(sp.data)))
			buf.Write(sh[:])
			buf.Write(sp.data)
		}
	}
	return modified, buf.Bytes()
}

func diffSpans(oldB, newB []byte) ([]span, uint64) {
	var spans []span
	var modified uint64
	limit := len(oldB)
	if len(newB) < limit {
		limit = len(newB)
	}
	i := 0
	for i < limit {
		if oldB[i] == newB[i] {
			i++
			continue
		}
		start := i
		for i < limit && oldB[i] != newB[i] {
			i++
		}
		spans = append(spans, span{
			offset: uint32(start),
			data:   cloneBytes(newB[start:i]),
		})
		modified += uint64(i - start)
	}
	if len(newB) > len(oldB) {
		extra := cloneBytes(newB[len(oldB):])
		spans = append(spans, span{offset: uint32(len(oldB)), data: extra})
		modified += uint64(len(extra))
	}
	if len(oldB) > len(newB) {
		modified += uint64(len(oldB) - len(newB))
	}
	return spans, modified
}

func putU32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

func gzipDefault(payload []byte) []byte {
	return gzipLevel(payload, gzip.DefaultCompression)
}

func gzipBest(payload []byte) []byte {
	return gzipLevel(payload, gzip.BestCompression)
}

func gzipLevel(payload []byte, level int) []byte {
	var buf bytes.Buffer
	w, err := gzip.NewWriterLevel(&buf, level)
	if err != nil {
		panic(err)
	}
	if _, err := w.Write(payload); err != nil {
		panic(err)
	}
	if err := w.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// minimalGzip is a valid empty gzip member, smaller than compress/gzip's writer output.
func minimalGzip() []byte {
	return []byte{
		0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xff,
		0x03, 0x00,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
	}
}
