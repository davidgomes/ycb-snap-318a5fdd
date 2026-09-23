// Package snapshot captures, diffs and restores the linear memory of several
// modules at once, so that multi-module applications can be inspected at a
// consistent point in time.
//
// # Notes
//
//   - This is an experimental API and subject to change.
package snapshot

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/tetratelabs/wazero/api"
)

// DiffEntry is a single byte that differs between two snapshots.
type DiffEntry struct {
	Offset   uint32
	OldValue byte
	NewValue byte
}

// Snapshot is an immutable capture of the memory of one or more modules.
type Snapshot interface {
	// Data returns a deep copy of the fully reconstructed memory of each
	// module, in capture order.
	Data() [][]byte
	// CompressedData returns the gzip-compressed form of the snapshot. For
	// full snapshots this is the gzip of Data() concatenated in capture
	// order. For incremental snapshots it is the compressed delta against
	// the baseline.
	CompressedData() []byte
	// Version is the capture version, monotonically increasing per
	// Coordinator and starting at 1.
	Version() uint64
	// Tags returns a copy of the tags on this snapshot.
	Tags() map[string]string
	// SetTag sets a tag on this snapshot.
	SetTag(key, value string)
	// Compare returns the byte-level differences from this snapshot to
	// other, grouped by module in capture order with offsets ascending.
	// Bytes past the end of the shorter memory compare as zero.
	Compare(other Snapshot) []DiffEntry
}

// Error codes returned by ErrorCode.
const (
	CodeNoModules           = "no_modules"
	CodeModuleClosed        = "module_closed"
	CodeNilBaseline         = "nil_baseline"
	CodeModuleCountMismatch = "module_count_mismatch"
	CodeIncompatibleModule  = "incompatible_module"
	CodeInsufficientMemory  = "insufficient_memory"
	CodeInvalidSnapshot     = "invalid_snapshot"
)

// Error is returned by this package and carries a machine-readable code.
type Error struct {
	Code string
	Msg  string
}

// Error implements error.
func (e *Error) Error() string { return "snapshot: " + e.Msg }

func newError(code, format string, args ...interface{}) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// ErrorCode returns the code of an error returned by this package, or the
// empty string if err is nil or was not produced by this package.
func ErrorCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

type snapshot struct {
	version     uint64
	modules     []api.Module
	data        [][]byte
	compressed  []byte
	incremental bool
	modified    uint64

	mu   sync.RWMutex
	tags map[string]string
}

func (s *snapshot) Data() [][]byte { return copyData(s.data) }

func (s *snapshot) CompressedData() []byte { return append([]byte(nil), s.compressed...) }

func (s *snapshot) Version() uint64 { return s.version }

func (s *snapshot) Tags() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return copyTags(s.tags)
}

func (s *snapshot) SetTag(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tags[key] = value
}

func (s *snapshot) Compare(other Snapshot) []DiffEntry {
	if other == nil {
		return nil
	}
	return diff(s.data, other.Data())
}

// dataOf avoids a deep copy for snapshots produced by this package.
func dataOf(s Snapshot) [][]byte {
	if in, ok := s.(*snapshot); ok {
		return in.data
	}
	return s.Data()
}

func diff(oldData, newData [][]byte) []DiffEntry {
	var out []DiffEntry
	n := max(len(oldData), len(newData))
	for i := 0; i < n; i++ {
		var o, nw []byte
		if i < len(oldData) {
			o = oldData[i]
		}
		if i < len(newData) {
			nw = newData[i]
		}
		size := max(len(o), len(nw))
		for off := 0; off < size; off++ {
			ov, nv := byteAt(o, off), byteAt(nw, off)
			if ov != nv {
				out = append(out, DiffEntry{Offset: uint32(off), OldValue: ov, NewValue: nv})
			}
		}
	}
	return out
}

func byteAt(b []byte, i int) byte {
	if i < len(b) {
		return b[i]
	}
	return 0
}

func copyData(data [][]byte) [][]byte {
	out := make([][]byte, len(data))
	for i, d := range data {
		out[i] = append([]byte{}, d...)
	}
	return out
}

func copyTags(tags map[string]string) map[string]string {
	out := make(map[string]string, len(tags))
	for k, v := range tags {
		out[k] = v
	}
	return out
}

func gzipBytes(b []byte) []byte {
	var buf bytes.Buffer
	w, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Bytes()
}

func newFullSnapshot(version uint64, modules []api.Module, data [][]byte, tags map[string]string) *snapshot {
	return &snapshot{
		version:    version,
		modules:    modules,
		data:       data,
		compressed: gzipBytes(bytes.Join(data, nil)),
		tags:       tags,
	}
}

// encodeDelta encodes, per module, the new length followed by runs of bytes
// that differ from the baseline. It returns the encoding and the number of
// changed bytes.
func encodeDelta(base, cur [][]byte) ([]byte, uint64) {
	var buf []byte
	var modified uint64
	for i := range cur {
		b, c := base[i], cur[i]
		buf = binary.AppendUvarint(buf, uint64(len(c)))
		type run struct{ start, end int }
		var runs []run
		size := max(len(b), len(c))
		for off := 0; off < size; off++ {
			if byteAt(b, off) == byteAt(c, off) {
				continue
			}
			modified++
			if l := len(runs); l > 0 && off-runs[l-1].end <= 4 {
				runs[l-1].end = off + 1
			} else {
				runs = append(runs, run{off, off + 1})
			}
		}
		buf = binary.AppendUvarint(buf, uint64(len(runs)))
		prev := 0
		for _, r := range runs {
			buf = binary.AppendUvarint(buf, uint64(r.start-prev))
			buf = binary.AppendUvarint(buf, uint64(r.end-r.start))
			for off := r.start; off < r.end; off++ {
				buf = append(buf, byteAt(c, off))
			}
			prev = r.end
		}
	}
	return buf, modified
}

// Coordinator captures and restores snapshots across modules. It is safe for
// concurrent use.
type Coordinator struct {
	mu      sync.Mutex
	version uint64
}

// NewCoordinator returns a new Coordinator whose first snapshot has version 1.
func NewCoordinator() *Coordinator {
	return &Coordinator{}
}

func checkModules(modules []api.Module) error {
	for i, m := range modules {
		if m == nil || m.IsClosed() {
			return newError(CodeModuleClosed, "module closed at index %d", i)
		}
	}
	return nil
}

func readMemory(m api.Module) []byte {
	mem := m.Memory()
	if mem == nil {
		return []byte{}
	}
	b, ok := mem.Read(0, mem.Size())
	if !ok {
		return []byte{}
	}
	return append([]byte{}, b...)
}

func readAll(modules []api.Module) [][]byte {
	data := make([][]byte, len(modules))
	for i, m := range modules {
		data[i] = readMemory(m)
	}
	return data
}

// CaptureSnapshot captures a full snapshot of the memory of all modules.
func (c *Coordinator) CaptureSnapshot(modules ...api.Module) (Snapshot, error) {
	if len(modules) == 0 {
		return nil, newError(CodeNoModules, "no modules to capture")
	}
	if err := checkModules(modules); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	data := readAll(modules)
	c.version++
	return newFullSnapshot(c.version, append([]api.Module{}, modules...), data, map[string]string{}), nil
}

// CaptureIncremental captures the memory of all modules as a delta against
// baseline, which may itself be incremental.
func (c *Coordinator) CaptureIncremental(baseline Snapshot, modules ...api.Module) (Snapshot, error) {
	if baseline == nil {
		return nil, newError(CodeNilBaseline, "baseline snapshot is nil")
	}
	base := dataOf(baseline)
	if len(modules) != len(base) {
		return nil, newError(CodeModuleCountMismatch, "module count mismatch: baseline has %d, got %d", len(base), len(modules))
	}
	if len(modules) == 0 {
		return nil, newError(CodeNoModules, "no modules to capture")
	}
	if err := checkModules(modules); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	data := readAll(modules)
	delta, modified := encodeDelta(base, data)
	c.version++
	return &snapshot{
		version:     c.version,
		modules:     append([]api.Module{}, modules...),
		data:        data,
		compressed:  gzipBytes(delta),
		incremental: true,
		modified:    modified,
		tags:        map[string]string{},
	}, nil
}

// RestoreSnapshot writes the captured memory back into modules.
//
// Each module is matched to a captured module by identity. When the number of
// modules equals the number captured, unmatched modules fall back to their
// position. Otherwise, unmatched modules are skipped.
func (c *Coordinator) RestoreSnapshot(snap Snapshot, modules ...api.Module) error {
	if snap == nil {
		return newError(CodeInvalidSnapshot, "snapshot is nil")
	}
	data := dataOf(snap)
	if len(modules) > len(data) {
		return newError(CodeIncompatibleModule, "incompatible module count: snapshot has %d, got %d", len(data), len(modules))
	}
	if err := checkModules(modules); err != nil {
		return err
	}
	var captured []api.Module
	if in, ok := snap.(*snapshot); ok {
		captured = in.modules
	}

	type target struct {
		mem  api.Memory
		data []byte
	}
	var targets []target
	for i, m := range modules {
		idx := -1
		for j, cm := range captured {
			if cm == m {
				idx = j
				break
			}
		}
		if idx < 0 && len(modules) == len(data) {
			idx = i
		}
		if idx < 0 {
			continue
		}
		d := data[idx]
		mem := m.Memory()
		var size uint32
		if mem != nil {
			size = mem.Size()
		}
		if uint64(size) < uint64(len(d)) {
			return newError(CodeInsufficientMemory, "insufficient memory in module %q: need %d bytes, have %d", m.Name(), len(d), size)
		}
		if len(d) > 0 {
			targets = append(targets, target{mem, d})
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range targets {
		if !t.mem.Write(0, t.data) {
			return newError(CodeInsufficientMemory, "insufficient memory: write of %d bytes failed", len(t.data))
		}
	}
	return nil
}

// SnapshotSummary describes a snapshot.
type SnapshotSummary struct {
	TotalModules  int
	TotalBytes    uint64
	ModifiedBytes uint64
	Version       uint64
}

// Summarize returns a summary of snap. ModifiedBytes is the number of bytes
// changed from the baseline for incremental snapshots, and zero otherwise.
func Summarize(snap Snapshot) SnapshotSummary {
	if snap == nil {
		return SnapshotSummary{}
	}
	data := dataOf(snap)
	s := SnapshotSummary{TotalModules: len(data), Version: snap.Version()}
	for _, d := range data {
		s.TotalBytes += uint64(len(d))
	}
	if in, ok := snap.(*snapshot); ok && in.incremental {
		s.ModifiedBytes = in.modified
	}
	return s
}

// Chain is an ordered history of snapshots. It is safe for concurrent use.
type Chain struct {
	mu    sync.RWMutex
	snaps []Snapshot
}

// NewChain returns an empty Chain.
func NewChain() *Chain { return &Chain{} }

// Push appends snap to the chain.
func (c *Chain) Push(snap Snapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snaps = append(c.snaps, snap)
}

// Head returns the most recently pushed snapshot, or nil if empty.
func (c *Chain) Head() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.snaps) == 0 {
		return nil
	}
	return c.snaps[len(c.snaps)-1]
}

// Len returns the number of snapshots in the chain.
func (c *Chain) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.snaps)
}

// Snapshots returns a copy of the snapshots, oldest first.
func (c *Chain) Snapshots() []Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Snapshot{}, c.snaps...)
}

var marshalMagic = []byte("WZSNAP\x01")

// MarshalSnapshot encodes the fully reconstructed memory, version and tags of
// snap in a portable binary format.
func MarshalSnapshot(snap Snapshot) ([]byte, error) {
	if snap == nil {
		return nil, newError(CodeInvalidSnapshot, "snapshot is nil")
	}
	buf := append([]byte{}, marshalMagic...)
	buf = binary.AppendUvarint(buf, snap.Version())

	tags := snap.Tags()
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	buf = binary.AppendUvarint(buf, uint64(len(keys)))
	for _, k := range keys {
		buf = appendBytes(buf, []byte(k))
		buf = appendBytes(buf, []byte(tags[k]))
	}

	data := dataOf(snap)
	buf = binary.AppendUvarint(buf, uint64(len(data)))
	for _, d := range data {
		buf = appendBytes(buf, d)
	}
	return buf, nil
}

func appendBytes(buf, b []byte) []byte {
	buf = binary.AppendUvarint(buf, uint64(len(b)))
	return append(buf, b...)
}

type decoder struct {
	b   []byte
	err error
}

func (d *decoder) uvarint() uint64 {
	if d.err != nil {
		return 0
	}
	v, n := binary.Uvarint(d.b)
	if n <= 0 {
		d.err = newError(CodeInvalidSnapshot, "malformed snapshot encoding")
		return 0
	}
	d.b = d.b[n:]
	return v
}

func (d *decoder) bytes() []byte {
	n := d.uvarint()
	if d.err != nil {
		return nil
	}
	if n > uint64(len(d.b)) {
		d.err = newError(CodeInvalidSnapshot, "truncated snapshot encoding")
		return nil
	}
	out := append([]byte{}, d.b[:n]...)
	d.b = d.b[n:]
	return out
}

// UnmarshalSnapshot decodes data produced by MarshalSnapshot into a full
// snapshot.
func UnmarshalSnapshot(data []byte) (Snapshot, error) {
	if !bytes.HasPrefix(data, marshalMagic) {
		return nil, newError(CodeInvalidSnapshot, "invalid snapshot header")
	}
	d := &decoder{b: data[len(marshalMagic):]}
	version := d.uvarint()

	numTags := d.uvarint()
	if d.err == nil && numTags > uint64(len(d.b)) {
		return nil, newError(CodeInvalidSnapshot, "invalid tag count %d", numTags)
	}
	tags := make(map[string]string, numTags)
	for i := uint64(0); i < numTags && d.err == nil; i++ {
		k := d.bytes()
		v := d.bytes()
		tags[string(k)] = string(v)
	}

	numModules := d.uvarint()
	if d.err == nil && numModules > uint64(len(d.b)) {
		return nil, newError(CodeInvalidSnapshot, "invalid module count %d", numModules)
	}
	mems := make([][]byte, 0, numModules)
	for i := uint64(0); i < numModules && d.err == nil; i++ {
		mems = append(mems, d.bytes())
	}
	if d.err != nil {
		return nil, d.err
	}
	if len(d.b) != 0 {
		return nil, newError(CodeInvalidSnapshot, "trailing bytes in snapshot encoding")
	}
	return newFullSnapshot(version, nil, mems, tags), nil
}
