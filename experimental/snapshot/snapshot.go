// Package snapshot captures and restores the linear memory of several modules
// together, so that a multi-module application can be inspected, compared, or
// rewound at a single consistent point.
//
// A Coordinator serializes its captures and restores, so a capture never
// observes a restore that is half applied. It does not pause guest execution:
// capture while the modules are not running, for example from a host function
// or between calls.
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

// Snapshot is the linear memory of one or more modules, in the order they
// were captured, at a point in time.
//
// Memory contents are immutable once captured: Data and Tags return copies
// that callers may modify freely. Tags may be added with SetTag at any time.
// All methods are safe for concurrent use.
type Snapshot interface {
	// Data returns the memory of each module in capture order. An
	// incremental snapshot returns the memory as reconstructed from its
	// baseline, not only the changes.
	Data() [][]byte

	// CompressedData returns the snapshot in gzip form. For a full snapshot,
	// this is the gzip of Data concatenated in capture order. For an
	// incremental snapshot, this is the gzip of the bytes that changed since
	// its baseline, which is smaller than the baseline's CompressedData unless
	// most of the memory changed.
	CompressedData() []byte

	// Version returns the position of this snapshot in the sequence captured
	// by its Coordinator, starting at 1.
	Version() uint64

	// Tags returns the annotations set with SetTag.
	Tags() map[string]string

	// SetTag annotates the snapshot, replacing any value already set for key.
	SetTag(key, value string)

	// Compare returns the bytes that differ between this snapshot's memory
	// and other's, grouped by module in capture order and sorted by offset
	// within each module. OldValue is from this snapshot and NewValue is from
	// other. Memory missing from either side, because a module is absent or
	// shorter, compares as zero. This returns nil if other is nil.
	Compare(other Snapshot) []DiffEntry
}

// DiffEntry is a byte that differs between two snapshots of a module's memory.
type DiffEntry struct {
	Offset   uint32
	OldValue byte
	NewValue byte
}

// snapshot implements Snapshot. A full snapshot owns the memory of every
// module in data. An incremental snapshot owns only what changed since base,
// in deltas, and reconstructs the rest from base on demand.
type snapshot struct {
	version uint64
	// modules are the modules captured, used to match restore targets by
	// identity. It is nil for decoded snapshots.
	modules    []api.Module
	compressed []byte

	data [][]byte

	base   *snapshot
	deltas []moduleDelta

	mu   sync.Mutex
	tags map[string]string
}

// moduleDelta is how the memory of one module changed since the baseline.
type moduleDelta struct {
	size uint64
	// runs are the maximal ranges below size that differ from the baseline.
	runs []run
	// modified counts bytes that differ from the baseline, including nonzero
	// baseline bytes past size.
	modified uint64
}

type run struct {
	offset uint64
	data   []byte
}

func newFullSnapshot(version uint64, modules []api.Module, data [][]byte) *snapshot {
	return &snapshot{
		version:    version,
		modules:    modules,
		compressed: gzipBytes(gzip.DefaultCompression, data...),
		data:       data,
	}
}

func newIncrementalSnapshot(version uint64, modules []api.Module, base *snapshot, data [][]byte) *snapshot {
	prev := base.reconstruct()
	deltas := make([]moduleDelta, len(data))
	for i := range data {
		deltas[i] = diffMemory(prev[i], data[i])
	}
	return &snapshot{
		version: version,
		modules: modules,
		// Deltas are small, so spend the extra time for the smallest output.
		compressed: gzipBytes(gzip.BestCompression, encodeDeltas(deltas)),
		base:       base,
		deltas:     deltas,
	}
}

// internalSnapshot returns snap as a *snapshot, copying the memory of a
// Snapshot implemented outside this package.
func internalSnapshot(snap Snapshot) *snapshot {
	if s, ok := snap.(*snapshot); ok {
		return s
	}
	return &snapshot{version: snap.Version(), data: snap.Data()}
}

func (s *snapshot) numModules() int {
	if s.base == nil {
		return len(s.data)
	}
	return len(s.deltas)
}

func (s *snapshot) totalBytes() (total uint64) {
	if s.base == nil {
		for _, d := range s.data {
			total += uint64(len(d))
		}
		return
	}
	for _, d := range s.deltas {
		total += d.size
	}
	return
}

func (s *snapshot) modifiedBytes() (modified uint64) {
	for _, d := range s.deltas {
		modified += d.modified
	}
	return
}

// reconstruct returns a copy of the memory of each module.
func (s *snapshot) reconstruct() [][]byte {
	if s.base == nil {
		data := make([][]byte, len(s.data))
		for i, d := range s.data {
			data[i] = cloneBytes(d)
		}
		return data
	}
	data := s.base.reconstruct()
	for i, d := range s.deltas {
		data[i] = d.apply(data[i])
	}
	return data
}

// Data implements Snapshot.Data.
func (s *snapshot) Data() [][]byte {
	return s.reconstruct()
}

// CompressedData implements Snapshot.CompressedData.
func (s *snapshot) CompressedData() []byte {
	return cloneBytes(s.compressed)
}

// Version implements Snapshot.Version.
func (s *snapshot) Version() uint64 {
	return s.version
}

// Tags implements Snapshot.Tags.
func (s *snapshot) Tags() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	tags := make(map[string]string, len(s.tags))
	for k, v := range s.tags {
		tags[k] = v
	}
	return tags
}

// SetTag implements Snapshot.SetTag.
func (s *snapshot) SetTag(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tags == nil {
		s.tags = map[string]string{}
	}
	s.tags[key] = value
}

// Compare implements Snapshot.Compare.
func (s *snapshot) Compare(other Snapshot) []DiffEntry {
	if isNil(other) {
		return nil
	}
	olds, news := s.reconstruct(), internalSnapshot(other).reconstruct()
	var diff []DiffEntry
	for m := 0; m < max(len(olds), len(news)); m++ {
		o, n := moduleAt(olds, m), moduleAt(news, m)
		end := max(len(o), len(n))
		for i := nextDiff(o, n, 0, end); i < end; i = nextDiff(o, n, i+1, end) {
			diff = append(diff, DiffEntry{Offset: uint32(i), OldValue: byteAt(o, i), NewValue: byteAt(n, i)})
		}
	}
	return diff
}

// diffMemory returns how cur differs from prev.
func diffMemory(prev, cur []byte) moduleDelta {
	d := moduleDelta{size: uint64(len(cur))}
	end := max(len(prev), len(cur))
	for i := nextDiff(prev, cur, 0, end); i < end; i = nextDiff(prev, cur, i, end) {
		start := i
		for i < end && byteAt(prev, i) != byteAt(cur, i) {
			i++
		}
		d.modified += uint64(i - start)
		if start < len(cur) {
			d.runs = append(d.runs, run{offset: uint64(start), data: cloneBytes(cur[start:min(i, len(cur))])})
		}
	}
	return d
}

// apply returns the memory that d describes, given the baseline memory prev,
// which it may modify.
func (d moduleDelta) apply(prev []byte) []byte {
	cur := prev
	if uint64(len(cur)) != d.size {
		cur = make([]byte, d.size)
		copy(cur, prev)
	}
	for _, r := range d.runs {
		copy(cur[r.offset:], r.data)
	}
	return cur
}

// encodeDeltas serializes deltas as varints: the module count, then for each
// module its size, run count, and runs. Each run is its offset past the end of
// the previous run, its length, and its bytes.
func encodeDeltas(deltas []moduleDelta) []byte {
	buf := binary.AppendUvarint(nil, uint64(len(deltas)))
	for _, d := range deltas {
		buf = binary.AppendUvarint(buf, d.size)
		buf = binary.AppendUvarint(buf, uint64(len(d.runs)))
		var end uint64
		for _, r := range d.runs {
			buf = binary.AppendUvarint(buf, r.offset-end)
			buf = binary.AppendUvarint(buf, uint64(len(r.data)))
			buf = append(buf, r.data...)
			end = r.offset + uint64(len(r.data))
		}
	}
	return buf
}

// diffBlock is how many bytes nextDiff compares at once while skipping
// unchanged memory, which is most of it.
const diffBlock = 256

// nextDiff returns the first offset in [i, end) where a and b differ, treating
// bytes past the end of either as zero, or end if there is none.
func nextDiff(a, b []byte, i, end int) int {
	for common := min(len(a), len(b)); i+diffBlock <= common && bytes.Equal(a[i:i+diffBlock], b[i:i+diffBlock]); {
		i += diffBlock
	}
	for i < end && byteAt(a, i) == byteAt(b, i) {
		i++
	}
	return i
}

func byteAt(b []byte, i int) byte {
	if i < len(b) {
		return b[i]
	}
	return 0
}

func moduleAt(data [][]byte, i int) []byte {
	if i < len(data) {
		return data[i]
	}
	return nil
}

// cloneBytes is like bytes.Clone, except the result is never nil.
func cloneBytes(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}

func gzipBytes(level int, chunks ...[]byte) []byte {
	var buf bytes.Buffer
	w, _ := gzip.NewWriterLevel(&buf, level)
	// Writes to a bytes.Buffer cannot fail.
	for _, c := range chunks {
		_, _ = w.Write(c)
	}
	_ = w.Close()
	return buf.Bytes()
}

// isNil reports whether v is nil or a nil pointer in an interface, such as the
// api.Memory of a module without memory.
func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
}
