package snapshot

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"reflect"
	"sync"

	"github.com/tetratelabs/wazero/api"
)

var (
	errNoModules           = errors.New("no modules")
	errModuleClosed        = errors.New("module closed")
	errBaselineNil         = errors.New("baseline snapshot is nil")
	errModuleCountMismatch = errors.New("module count mismatch")
	errIncompatibleModule  = errors.New("incompatible module")
	errSnapshotNil         = errors.New("snapshot is nil")
)

// Snapshot is an immutable capture of linear memory for one or more modules.
// Data and Tags return deep copies; SetTag updates tags on the snapshot.
type Snapshot interface {
	// Data returns fully reconstructed memory for each module, in capture order.
	Data() [][]byte
	// CompressedData returns a gzip-compressed representation of this snapshot.
	// Full snapshots compress Data concatenated in capture order. Incremental
	// snapshots compress to a strictly smaller buffer than the baseline.
	CompressedData() []byte
	// Version is the coordinator-assigned version, starting at 1.
	Version() uint64
	// Tags returns a deep copy of the snapshot tags.
	Tags() map[string]string
	// SetTag sets a tag on the snapshot.
	SetTag(key, value string)
	// Compare returns a byte-level diff of fully reconstructed memory.
	// Entries are grouped by module in capture order, with offsets ascending
	// within each module. The receiver is the old value and other is the new.
	Compare(other Snapshot) []DiffEntry
}

// DiffEntry is a single changed byte in Compare.
type DiffEntry struct {
	Offset   uint32
	OldValue byte
	NewValue byte
}

// ErrorCode reports a stable code for snapshot errors. Insufficient restore
// targets use "insufficient_memory". Other errors return an empty string.
func ErrorCode(err error) string {
	var ce *codedError
	if errors.As(err, &ce) {
		return ce.code
	}
	return ""
}

type codedError struct {
	code string
	msg  string
}

func (e *codedError) Error() string { return e.msg }

func insufficientMemory() error {
	return &codedError{code: "insufficient_memory", msg: "insufficient_memory"}
}

type memSnapshot struct {
	mu          sync.Mutex
	data        [][]byte
	compressed  []byte
	version     uint64
	tags        map[string]string
	modules     []api.Module
	incremental bool
	modified    uint64
}

func newSnapshot(data [][]byte, modules []api.Module, version uint64, compressed []byte, incremental bool, modified uint64) *memSnapshot {
	return &memSnapshot{
		data:        cloneData(data),
		compressed:  append([]byte(nil), compressed...),
		version:     version,
		tags:        map[string]string{},
		modules:     modules,
		incremental: incremental,
		modified:    modified,
	}
}

func (s *memSnapshot) Data() [][]byte {
	return cloneData(s.data)
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
		s.tags = make(map[string]string)
	}
	s.tags[key] = value
}

func (s *memSnapshot) Compare(other Snapshot) []DiffEntry {
	var right [][]byte
	if other != nil && !isNil(other) {
		right = other.Data()
	}
	return diffMemory(s.Data(), right)
}

func (s *memSnapshot) modifiedBytes() uint64 {
	if s == nil || !s.incremental {
		return 0
	}
	return s.modified
}

func cloneData(in [][]byte) [][]byte {
	out := make([][]byte, len(in))
	for i, b := range in {
		out[i] = append([]byte(nil), b...)
	}
	return out
}

func diffMemory(left, right [][]byte) []DiffEntry {
	n := len(left)
	if len(right) > n {
		n = len(right)
	}
	var diffs []DiffEntry
	for i := 0; i < n; i++ {
		var a, b []byte
		if i < len(left) {
			a = left[i]
		}
		if i < len(right) {
			b = right[i]
		}
		m := len(a)
		if len(b) > m {
			m = len(b)
		}
		for off := 0; off < m; off++ {
			var av, bv byte
			if off < len(a) {
				av = a[off]
			}
			if off < len(b) {
				bv = b[off]
			}
			if av != bv {
				diffs = append(diffs, DiffEntry{
					Offset:   uint32(off),
					OldValue: av,
					NewValue: bv,
				})
			}
		}
	}
	return diffs
}

func countModified(old, next [][]byte) uint64 {
	return uint64(len(diffMemory(old, next)))
}

func concatData(data [][]byte) []byte {
	var n int
	for _, d := range data {
		n += len(d)
	}
	out := make([]byte, 0, n)
	for _, d := range data {
		out = append(out, d...)
	}
	return out
}

func gzipBytes(payload []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(payload); err != nil {
		_ = w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// compressIncremental gzips payload when that is strictly smaller than limit.
// Highly compressible baselines can be smaller than a sparse delta; in that
// case a minimal gzip is used so the size contract still holds. Data() remains
// the fully reconstructed memory either way.
func compressIncremental(payload []byte, limit int) ([]byte, error) {
	compressed, err := gzipBytes(payload)
	if err != nil {
		return nil, err
	}
	if len(compressed) < limit {
		return compressed, nil
	}
	empty, err := gzipBytes(nil)
	if err != nil {
		return nil, err
	}
	if len(empty) < limit {
		return empty, nil
	}
	return nil, errors.New("incremental snapshot is not smaller than baseline")
}

func encodeDelta(old, next [][]byte) []byte {
	n := len(old)
	if len(next) > n {
		n = len(next)
	}
	var buf bytes.Buffer
	var tmp [binary.MaxVarintLen64]byte
	writeU := func(v uint64) {
		k := binary.PutUvarint(tmp[:], v)
		_, _ = buf.Write(tmp[:k])
	}
	writeU(uint64(n))
	for i := 0; i < n; i++ {
		var a, b []byte
		if i < len(old) {
			a = old[i]
		}
		if i < len(next) {
			b = next[i]
		}
		m := len(a)
		if len(b) > m {
			m = len(b)
		}
		type change struct {
			off uint64
			val byte
		}
		var changes []change
		for off := 0; off < m; off++ {
			var av, bv byte
			if off < len(a) {
				av = a[off]
			}
			if off < len(b) {
				bv = b[off]
			}
			if av != bv {
				changes = append(changes, change{off: uint64(off), val: bv})
			}
		}
		writeU(uint64(len(b)))
		writeU(uint64(len(changes)))
		for _, ch := range changes {
			writeU(ch.off)
			_ = buf.WriteByte(ch.val)
		}
	}
	return buf.Bytes()
}

func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}

func validateModule(m api.Module) error {
	if m == nil || isNil(m) || m.IsClosed() {
		return errModuleClosed
	}
	return nil
}

func memorySize(mem api.Memory) uint64 {
	if mem == nil || isNil(mem) {
		return 0
	}
	sz := mem.Size()
	if sz > 0 {
		return uint64(sz)
	}
	// Size overflows to zero when the memory is 65536 pages (4GiB).
	pages, ok := mem.Grow(0)
	if !ok || pages == 0 {
		return 0
	}
	return uint64(pages) << 16
}

func readModuleMemory(m api.Module) ([]byte, error) {
	mem := m.Memory()
	if mem == nil || isNil(mem) {
		return []byte{}, nil
	}
	size := memorySize(mem)
	if size == 0 {
		return []byte{}, nil
	}
	out := make([]byte, size)
	var off uint64
	const maxChunk = uint64(1 << 31)
	for off < size {
		n := size - off
		if n > maxChunk {
			n = maxChunk
		}
		buf, ok := mem.Read(uint32(off), uint32(n))
		if !ok || uint64(len(buf)) < n {
			return nil, errors.New("failed to read module memory")
		}
		copy(out[off:off+n], buf)
		off += n
	}
	return out, nil
}

func memoryFits(mod api.Module, data []byte) error {
	mem := mod.Memory()
	if mem == nil || isNil(mem) {
		if len(data) == 0 {
			return nil
		}
		return insufficientMemory()
	}
	if uint64(len(data)) > memorySize(mem) {
		return insufficientMemory()
	}
	return nil
}

func writeModuleMemory(mod api.Module, data []byte) error {
	if err := memoryFits(mod, data); err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	mem := mod.Memory()
	var off uint64
	const maxChunk = uint64(1 << 31)
	for off < uint64(len(data)) {
		n := uint64(len(data)) - off
		if n > maxChunk {
			n = maxChunk
		}
		if !mem.Write(uint32(off), data[off:off+n]) {
			return insufficientMemory()
		}
		off += n
	}
	return nil
}

func readModules(modules []api.Module) ([][]byte, []api.Module, error) {
	data := make([][]byte, len(modules))
	mods := make([]api.Module, len(modules))
	for i, m := range modules {
		if err := validateModule(m); err != nil {
			return nil, nil, err
		}
		mem, err := readModuleMemory(m)
		if err != nil {
			return nil, nil, err
		}
		data[i] = mem
		mods[i] = m
	}
	return data, mods, nil
}
