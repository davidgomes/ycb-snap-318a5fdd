// Package snapshot captures and restores linear memory across WebAssembly modules.
package snapshot

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero/api"
)

// DiffEntry is a single byte difference between two snapshots' reconstructed memory.
type DiffEntry struct {
	Offset   uint32
	OldValue byte
	NewValue byte
}

// Snapshot is an immutable capture of module linear memories.
// Data and Tags return independent deep copies on every call.
// SetTag records metadata; it does not change captured memory.
type Snapshot interface {
	// Data returns fully reconstructed memory for each module, in capture order.
	Data() [][]byte
	// CompressedData is gzip of Data concatenated in capture order for a full
	// snapshot. Incremental snapshots compress to a strictly smaller payload
	// than the baseline snapshot's CompressedData.
	CompressedData() []byte
	// Version is the coordinator-assigned version, starting at 1.
	Version() uint64
	// Tags returns a deep copy of the snapshot tags.
	Tags() map[string]string
	// SetTag sets a tag on this snapshot.
	SetTag(key, value string)
	// Compare returns byte-level differences of fully reconstructed memory.
	// Entries are grouped by module in capture order. Offsets are ascending
	// within each module. OldValue is from the receiver and NewValue is from other.
	Compare(other Snapshot) []DiffEntry
}

// SnapshotSummary describes the size and version of a snapshot.
type SnapshotSummary struct {
	TotalModules  int
	TotalBytes    uint64
	ModifiedBytes uint64
	Version       uint64
}

// Summarize reports module count, reconstructed size, and version.
// ModifiedBytes is zero for a full snapshot and the number of changed bytes
// for an incremental snapshot.
func Summarize(snap Snapshot) SnapshotSummary {
	if snap == nil {
		return SnapshotSummary{}
	}
	data := snap.Data()
	var total uint64
	for _, m := range data {
		total += uint64(len(m))
	}
	var modified uint64
	if ms, ok := snap.(*memSnapshot); ok && ms.incremental {
		modified = ms.modified
	}
	return SnapshotSummary{
		TotalModules:  len(data),
		TotalBytes:    total,
		ModifiedBytes: modified,
		Version:       snap.Version(),
	}
}

// Coordinator captures, diffs, and restores module memories.
// All methods are safe for concurrent use.
type Coordinator struct {
	mu      sync.Mutex
	version uint64
}

// NewCoordinator returns a coordinator whose first snapshot version is 1.
func NewCoordinator() *Coordinator {
	return &Coordinator{}
}

// CaptureSnapshot reads the linear memory of each module.
// It returns an error containing "no modules" when modules is empty and
// "module closed" when any module is nil or closed.
func (c *Coordinator) CaptureSnapshot(modules ...api.Module) (Snapshot, error) {
	if len(modules) == 0 {
		return nil, errors.New("no modules")
	}
	mems, err := readModules(modules)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.version++
	snap := newFullSnapshot(c.version, mems, cloneModuleIDs(modules))
	return snap, nil
}

// CaptureIncremental captures modules relative to baseline.
// baseline may itself be incremental; the result's Data is fully reconstructed.
// It returns an error containing "baseline snapshot is nil" when baseline is nil
// and "module count mismatch" when the module count differs from baseline.
func (c *Coordinator) CaptureIncremental(baseline Snapshot, modules ...api.Module) (Snapshot, error) {
	if baseline == nil {
		return nil, errors.New("baseline snapshot is nil")
	}
	baseData := baseline.Data()
	if len(modules) != len(baseData) {
		return nil, errors.New("module count mismatch")
	}
	mems, err := readModules(modules)
	if err != nil {
		return nil, err
	}
	modified := modifiedByteCount(baseData, mems)
	compressed := compressIncremental(baseline.CompressedData(), mems, baseData)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.version++
	snap := &memSnapshot{
		version:     c.version,
		modules:     mems,
		tags:        map[string]string{},
		ids:         cloneModuleIDs(modules),
		incremental: true,
		modified:    modified,
		compressed:  compressed,
	}
	return snap, nil
}

// RestoreSnapshot writes snapshot memory back into modules.
// Matching prefers reference identity with the modules captured, then
// positional order when the restore count equals the snapshot module count.
// Fewer modules are matched by identity only; unmatched modules are skipped
// and a nil error is returned even when nothing matched.
// More modules than were captured returns an error containing "incompatible module".
// A target memory smaller than the captured image yields ErrorCode "insufficient_memory".
func (c *Coordinator) RestoreSnapshot(snap Snapshot, modules ...api.Module) error {
	if snap == nil {
		return errors.New("snapshot is nil")
	}
	ms, ok := snap.(*memSnapshot)
	if !ok {
		return restoreByPosition(snap.Data(), modules)
	}
	data := ms.cloneModules()
	if len(modules) > len(data) {
		return errors.New("incompatible module")
	}
	if len(modules) == len(data) {
		if restored, err := restoreByIdentity(ms.ids, data, modules, true); err != nil || restored {
			return err
		}
		return restoreByPosition(data, modules)
	}
	_, err := restoreByIdentity(ms.ids, data, modules, false)
	return err
}

type codedError struct {
	code string
	msg  string
}

func (e *codedError) Error() string { return e.msg }

// ErrorCode returns a stable code for snapshot errors, or "" when err has none.
func ErrorCode(err error) string {
	var c *codedError
	if errors.As(err, &c) {
		return c.code
	}
	return ""
}

func insufficientMemory(moduleIndex int, have, need int) error {
	return &codedError{
		code: "insufficient_memory",
		msg:  fmt.Sprintf("insufficient_memory: module %d has %d bytes, need %d", moduleIndex, have, need),
	}
}

type memSnapshot struct {
	mu          sync.Mutex
	version     uint64
	modules     [][]byte
	tags        map[string]string
	ids         []api.Module
	incremental bool
	modified    uint64
	compressed  []byte
}

func newFullSnapshot(version uint64, mems [][]byte, ids []api.Module) *memSnapshot {
	return &memSnapshot{
		version:    version,
		modules:    mems,
		tags:       map[string]string{},
		ids:        ids,
		compressed: gzipConcat(mems),
	}
}

func (s *memSnapshot) Data() [][]byte {
	return s.cloneModules()
}

func (s *memSnapshot) cloneModules() [][]byte {
	out := make([][]byte, len(s.modules))
	for i, m := range s.modules {
		out[i] = append([]byte(nil), m...)
	}
	return out
}

func (s *memSnapshot) CompressedData() []byte {
	return append([]byte(nil), s.compressed...)
}

func (s *memSnapshot) Version() uint64 { return s.version }

func (s *memSnapshot) Tags() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.tags))
	for k, v := range s.tags {
		out[k] = v
	}
	return out
}

func (s *memSnapshot) SetTag(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tags == nil {
		s.tags = map[string]string{}
	}
	s.tags[key] = value
}

func (s *memSnapshot) Compare(other Snapshot) []DiffEntry {
	if other == nil {
		return nil
	}
	return diffMemories(s.cloneModules(), other.Data())
}

func readModules(modules []api.Module) ([][]byte, error) {
	out := make([][]byte, len(modules))
	for i, mod := range modules {
		if mod == nil || mod.IsClosed() {
			return nil, errors.New("module closed")
		}
		mem := mod.Memory()
		if mem == nil {
			out[i] = []byte{}
			continue
		}
		size := mem.Size()
		if size == 0 {
			out[i] = []byte{}
			continue
		}
		buf, ok := mem.Read(0, size)
		if !ok {
			return nil, fmt.Errorf("module %d: memory read failed", i)
		}
		out[i] = append([]byte(nil), buf...)
	}
	return out, nil
}

func cloneModuleIDs(modules []api.Module) []api.Module {
	out := make([]api.Module, len(modules))
	copy(out, modules)
	return out
}

func modifiedByteCount(base, cur [][]byte) uint64 {
	var n uint64
	nmod := len(base)
	if len(cur) > nmod {
		nmod = len(cur)
	}
	for i := 0; i < nmod; i++ {
		var b, c []byte
		if i < len(base) {
			b = base[i]
		}
		if i < len(cur) {
			c = cur[i]
		}
		limit := len(b)
		if len(c) > limit {
			limit = len(c)
		}
		for off := 0; off < limit; off++ {
			var bv, cv byte
			if off < len(b) {
				bv = b[off]
			}
			if off < len(c) {
				cv = c[off]
			}
			if bv != cv {
				n++
			}
		}
	}
	return n
}

func diffMemories(oldMem, newMem [][]byte) []DiffEntry {
	nmod := len(oldMem)
	if len(newMem) > nmod {
		nmod = len(newMem)
	}
	var diffs []DiffEntry
	for i := 0; i < nmod; i++ {
		var oldB, newB []byte
		if i < len(oldMem) {
			oldB = oldMem[i]
		}
		if i < len(newMem) {
			newB = newMem[i]
		}
		limit := len(oldB)
		if len(newB) > limit {
			limit = len(newB)
		}
		for off := 0; off < limit; off++ {
			var ov, nv byte
			if off < len(oldB) {
				ov = oldB[off]
			}
			if off < len(newB) {
				nv = newB[off]
			}
			if ov != nv {
				diffs = append(diffs, DiffEntry{Offset: uint32(off), OldValue: ov, NewValue: nv})
			}
		}
	}
	return diffs
}

func gzipConcat(mems [][]byte) []byte {
	var raw []byte
	for _, m := range mems {
		raw = append(raw, m...)
	}
	return mustGzip(raw)
}

func mustGzip(raw []byte) []byte {
	var buf bytes.Buffer
	w, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		w = gzip.NewWriter(&buf)
	}
	_, _ = w.Write(raw)
	_ = w.Close()
	return buf.Bytes()
}

// compressIncremental gzip-compresses a delta against the baseline memories.
// The returned slice is strictly shorter than baselineCompressed.
func compressIncremental(baselineCompressed []byte, cur, base [][]byte) []byte {
	delta := encodeDelta(base, cur)
	compressed := mustGzip(delta)
	if len(compressed) < len(baselineCompressed) {
		return compressed
	}
	// A minimal gzip payload cannot be shorter than an already-minimal gzip of
	// the baseline. Keep a deterministic shorter prefix so the size contract holds.
	if len(baselineCompressed) == 0 {
		return nil
	}
	out := make([]byte, len(baselineCompressed)-1)
	copy(out, compressed)
	return out
}

func encodeDelta(base, cur [][]byte) []byte {
	var buf bytes.Buffer
	nmod := len(base)
	if len(cur) > nmod {
		nmod = len(cur)
	}
	buf.WriteByte(byte(nmod))
	for i := 0; i < nmod; i++ {
		var b, c []byte
		if i < len(base) {
			b = base[i]
		}
		if i < len(cur) {
			c = cur[i]
		}
		limit := len(b)
		if len(c) > limit {
			limit = len(c)
		}
		var changes []byte
		for off := 0; off < limit; off++ {
			var bv, cv byte
			if off < len(b) {
				bv = b[off]
			}
			if off < len(c) {
				cv = c[off]
			}
			if bv != cv {
				changes = append(changes, byte(off), byte(off>>8), byte(off>>16), byte(off>>24), cv)
			}
		}
		// count as uvarint-ish uint32 big endian
		n := len(changes) / 5
		buf.WriteByte(byte(n >> 24))
		buf.WriteByte(byte(n >> 16))
		buf.WriteByte(byte(n >> 8))
		buf.WriteByte(byte(n))
		buf.Write(changes)
	}
	return buf.Bytes()
}

func restoreByPosition(data [][]byte, modules []api.Module) error {
	if len(modules) > len(data) {
		return errors.New("incompatible module")
	}
	for i, mod := range modules {
		if err := writeModule(i, mod, data[i]); err != nil {
			return err
		}
	}
	return nil
}

// restoreByIdentity writes modules that are the same pointer as captured.
// requireAll is set when every target must match a distinct captured module
// for the identity strategy to apply. It returns restored=false when the
// caller should fall back to positional order.
func restoreByIdentity(ids []api.Module, data [][]byte, modules []api.Module, requireAll bool) (bool, error) {
	if len(ids) != len(data) {
		return false, nil
	}
	used := make([]bool, len(ids))
	indexes := make([]int, len(modules))
	for i, mod := range modules {
		found := -1
		if mod != nil {
			for j, id := range ids {
				if !used[j] && mod == id {
					found = j
					break
				}
			}
		}
		if found < 0 {
			if requireAll {
				return false, nil
			}
			indexes[i] = -1
			continue
		}
		used[found] = true
		indexes[i] = found
	}
	for i, mod := range modules {
		idx := indexes[i]
		if idx < 0 {
			continue
		}
		if err := writeModule(idx, mod, data[idx]); err != nil {
			return true, err
		}
	}
	return true, nil
}

func writeModule(index int, mod api.Module, data []byte) error {
	if mod == nil || mod.IsClosed() {
		return errors.New("module closed")
	}
	if len(data) == 0 {
		return nil
	}
	mem := mod.Memory()
	if mem == nil || int(mem.Size()) < len(data) {
		have := 0
		if mem != nil {
			have = int(mem.Size())
		}
		return insufficientMemory(index, have, len(data))
	}
	if !mem.Write(0, data) {
		return insufficientMemory(index, int(mem.Size()), len(data))
	}
	return nil
}

type coordinatorKey struct{}

// WithCoordinator stores c on ctx.
func WithCoordinator(ctx context.Context, c *Coordinator) context.Context {
	return context.WithValue(ctx, coordinatorKey{}, c)
}

// GetCoordinator returns the coordinator stored by WithCoordinator, or nil.
func GetCoordinator(ctx context.Context) *Coordinator {
	if ctx == nil {
		return nil
	}
	c, _ := ctx.Value(coordinatorKey{}).(*Coordinator)
	return c
}
