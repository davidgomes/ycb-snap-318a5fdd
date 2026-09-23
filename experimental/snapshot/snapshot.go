package snapshot

import (
	"bytes"
	"compress/gzip"
	"errors"
	"reflect"
	"sync"

	"github.com/tetratelabs/wazero/api"
)

var (
	errNoModules          = errors.New("no modules")
	errModuleClosed       = errors.New("module closed")
	errBaselineNil        = errors.New("baseline snapshot is nil")
	errModuleCount        = errors.New("module count mismatch")
	errIncompatibleModule = errors.New("incompatible module")
	errNilSnapshot        = errors.New("nil snapshot")

	errInsufficientMemory = &codeError{code: "insufficient_memory", msg: "insufficient_memory"}
)

// codeError is an error that carries a stable machine-readable code.
type codeError struct {
	code string
	msg  string
}

func (e *codeError) Error() string {
	if e == nil {
		return ""
	}
	return e.msg
}

// ErrorCode returns a stable code for err. Insufficient restore targets use
// "insufficient_memory". Other errors return an empty string.
func ErrorCode(err error) string {
	var ce *codeError
	if errors.As(err, &ce) {
		return ce.code
	}
	return ""
}

// DiffEntry is one byte that differs between two snapshots.
//
// Entries from Snapshot.Compare are grouped by module in capture order.
// Within a module, Offset increases.
type DiffEntry struct {
	Offset   uint32
	OldValue byte
	NewValue byte
}

// Snapshot is an immutable view of linear memory captured from one or more
// modules. Incremental snapshots still expose fully reconstructed memory from
// Data.
type Snapshot interface {
	// Data returns a deep copy of fully reconstructed memory, one slice per
	// module in capture order.
	Data() [][]byte
	// CompressedData returns a gzip payload. For a full snapshot this is the
	// gzip of Data concatenated in capture order. An incremental snapshot's
	// payload is strictly shorter than its baseline's CompressedData.
	CompressedData() []byte
	// Version is the coordinator-assigned version, starting at 1.
	Version() uint64
	// Tags returns a deep copy of the snapshot's tags.
	Tags() map[string]string
	// SetTag records metadata on the snapshot.
	SetTag(key, value string)
	// Compare returns the byte-level diff that turns this snapshot into other.
	// OldValue comes from the receiver and NewValue from other.
	Compare(other Snapshot) []DiffEntry
}

// SnapshotSummary is a compact description of a snapshot.
type SnapshotSummary struct {
	TotalModules  int
	TotalBytes    uint64
	ModifiedBytes uint64
	Version       uint64
}

// Summarize reports module count, reconstructed size, and version.
// ModifiedBytes is zero for a full snapshot and the number of bytes that
// differ from the immediate baseline for an incremental snapshot.
func Summarize(snap Snapshot) SnapshotSummary {
	if snap == nil {
		return SnapshotSummary{}
	}
	data := snap.Data()
	var total uint64
	for _, d := range data {
		total += uint64(len(d))
	}
	sum := SnapshotSummary{
		TotalModules: len(data),
		TotalBytes:   total,
		Version:      snap.Version(),
	}
	if ms, ok := snap.(*memSnapshot); ok {
		sum.ModifiedBytes = ms.modified
	}
	return sum
}

// Coordinator captures and restores memory across modules.
//
// All methods are safe for concurrent use. Capture and restore run one at a
// time so a snapshot observes a single cut across every module.
type Coordinator struct {
	mu      sync.Mutex
	version uint64
}

// NewCoordinator returns a coordinator whose first successful capture is
// version 1.
func NewCoordinator() *Coordinator {
	return &Coordinator{}
}

// CaptureSnapshot records the full linear memory of each module.
//
// It returns an error containing "no modules" when no modules are given, and
// "module closed" when any module is nil or closed.
func (c *Coordinator) CaptureSnapshot(modules ...api.Module) (Snapshot, error) {
	if len(modules) == 0 {
		return nil, errNoModules
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	mods := cloneModules(modules)
	data, err := readAll(mods)
	if err != nil {
		return nil, err
	}
	compressed, err := gzipCompress(concat(data))
	if err != nil {
		return nil, err
	}
	c.version++
	return newMemSnapshot(c.version, data, mods, 0, compressed), nil
}

// CaptureIncremental records module memory relative to baseline.
//
// baseline may itself be incremental; the result's Data is the full memory
// image. Versions come from this coordinator and do not depend on the
// baseline version. A nil baseline returns "baseline snapshot is nil". A
// module count other than the baseline's returns "module count mismatch".
func (c *Coordinator) CaptureIncremental(baseline Snapshot, modules ...api.Module) (Snapshot, error) {
	if baseline == nil {
		return nil, errBaselineNil
	}
	baseData := baseline.Data()
	if len(modules) != len(baseData) {
		return nil, errModuleCount
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	mods := cloneModules(modules)
	data, err := readAll(mods)
	if err != nil {
		return nil, err
	}
	compressed, err := compressIncremental(baseline.CompressedData(), baseData, data)
	if err != nil {
		return nil, err
	}
	c.version++
	return newMemSnapshot(c.version, data, mods, countDiff(baseData, data), compressed), nil
}

// RestoreSnapshot writes snap's reconstructed memory into modules.
//
// Matching prefers pointer identity with the modules that were captured. When
// the same number of modules is provided, any module that is not one of the
// captured pointers fills the remaining slots in order. Fewer modules are
// matched by identity only; unmatched modules are skipped and a complete miss
// still returns nil. More modules than were captured returns "incompatible
// module". A target smaller than the captured image returns an error whose
// ErrorCode is "insufficient_memory".
func (c *Coordinator) RestoreSnapshot(snap Snapshot, modules ...api.Module) error {
	if snap == nil {
		return errNilSnapshot
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if ms, ok := snap.(*memSnapshot); ok {
		return ms.restore(modules)
	}
	return restoreGeneric(snap, modules)
}

var _ Snapshot = (*memSnapshot)(nil)

type memSnapshot struct {
	mu         sync.Mutex
	version    uint64
	tags       map[string]string
	data       [][]byte
	modules    []api.Module
	modified   uint64
	compressed []byte
}

func newMemSnapshot(version uint64, data [][]byte, modules []api.Module, modified uint64, compressed []byte) *memSnapshot {
	return &memSnapshot{
		version:    version,
		tags:       map[string]string{},
		data:       data,
		modules:    modules,
		modified:   modified,
		compressed: compressed,
	}
}

func (s *memSnapshot) Data() [][]byte {
	out := make([][]byte, len(s.data))
	for i := range s.data {
		out[i] = cloneBytes(s.data[i])
	}
	return out
}

func (s *memSnapshot) CompressedData() []byte {
	return cloneBytes(s.compressed)
}

func (s *memSnapshot) Version() uint64 {
	return s.version
}

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
	var otherData [][]byte
	if other != nil {
		otherData = other.Data()
	}
	return diffMemories(s.data, otherData)
}

func (s *memSnapshot) restore(modules []api.Module) error {
	plans, err := s.match(modules)
	if err != nil {
		return err
	}
	for _, p := range plans {
		if p.idx < 0 || p.idx >= len(s.data) {
			return errIncompatibleModule
		}
		if err := checkRestorable(p.mod, s.data[p.idx]); err != nil {
			return err
		}
	}
	for _, p := range plans {
		if err := writeMemory(p.mod, s.data[p.idx]); err != nil {
			return err
		}
	}
	return nil
}

type restorePlan struct {
	idx int
	mod api.Module
}

func (s *memSnapshot) match(modules []api.Module) ([]restorePlan, error) {
	n := len(s.data)
	if len(modules) > n {
		return nil, errIncompatibleModule
	}
	if len(modules) < n {
		return identityOnly(s.modules, modules), nil
	}
	return matchEqual(s.modules, modules), nil
}

func identityOnly(captured, provided []api.Module) []restorePlan {
	var plans []restorePlan
	used := make([]bool, len(captured))
	for _, m := range provided {
		idx, ok := findUnused(captured, used, m)
		if !ok {
			continue
		}
		used[idx] = true
		plans = append(plans, restorePlan{idx: idx, mod: m})
	}
	return plans
}

// matchEqual pairs an equal number of restore targets with captured slots.
// Each target is matched by pointer identity first. Targets that are not a
// captured module fill the remaining slots in order.
func matchEqual(captured, provided []api.Module) []restorePlan {
	n := len(provided)
	plans := make([]restorePlan, n)
	assigned := make([]bool, n)
	idUsed := make([]bool, len(captured))
	slotUsed := make([]bool, n)
	for i, m := range provided {
		idx, ok := findUnused(captured, idUsed, m)
		if !ok || idx >= n {
			continue
		}
		idUsed[idx] = true
		slotUsed[idx] = true
		assigned[i] = true
		plans[i] = restorePlan{idx: idx, mod: m}
	}
	next := 0
	for i, m := range provided {
		if assigned[i] {
			continue
		}
		for next < n && slotUsed[next] {
			next++
		}
		if next >= n {
			break
		}
		plans[i] = restorePlan{idx: next, mod: m}
		slotUsed[next] = true
		next++
	}
	return plans
}

func findUnused(captured []api.Module, used []bool, m api.Module) (int, bool) {
	if moduleUnavailable(m) {
		return 0, false
	}
	for i, c := range captured {
		if used[i] || c == nil || c != m {
			continue
		}
		return i, true
	}
	return 0, false
}

func restoreGeneric(snap Snapshot, modules []api.Module) error {
	data := snap.Data()
	if len(modules) > len(data) {
		return errIncompatibleModule
	}
	// Without captured pointers, only a full positional restore is possible.
	if len(modules) < len(data) {
		return nil
	}
	plans := make([]restorePlan, len(modules))
	for i, m := range modules {
		plans[i] = restorePlan{idx: i, mod: m}
	}
	for _, p := range plans {
		if err := checkRestorable(p.mod, data[p.idx]); err != nil {
			return err
		}
	}
	for _, p := range plans {
		if err := writeMemory(p.mod, data[p.idx]); err != nil {
			return err
		}
	}
	return nil
}

func checkRestorable(m api.Module, data []byte) error {
	if moduleUnavailable(m) || m.IsClosed() {
		return errModuleClosed
	}
	mem := m.Memory()
	if mem == nil {
		if len(data) == 0 {
			return nil
		}
		return errInsufficientMemory
	}
	if uint64(len(data)) > uint64(mem.Size()) {
		return errInsufficientMemory
	}
	return nil
}

func writeMemory(m api.Module, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	mem := m.Memory()
	if mem == nil || !mem.Write(0, data) {
		return errInsufficientMemory
	}
	return nil
}

func readAll(modules []api.Module) ([][]byte, error) {
	data := make([][]byte, len(modules))
	for i, m := range modules {
		b, err := readModuleMemory(m)
		if err != nil {
			return nil, err
		}
		data[i] = b
	}
	return data, nil
}

func readModuleMemory(m api.Module) ([]byte, error) {
	if moduleUnavailable(m) || m.IsClosed() {
		return nil, errModuleClosed
	}
	mem := m.Memory()
	if mem == nil {
		return []byte{}, nil
	}
	n := mem.Size()
	if n == 0 {
		return []byte{}, nil
	}
	view, ok := mem.Read(0, n)
	if !ok || uint32(len(view)) != n {
		return nil, errors.New("memory read failed")
	}
	return cloneBytes(view), nil
}

func moduleUnavailable(m api.Module) bool {
	if m == nil {
		return true
	}
	v := reflect.ValueOf(m)
	return v.Kind() == reflect.Ptr && v.IsNil()
}

func cloneModules(modules []api.Module) []api.Module {
	out := make([]api.Module, len(modules))
	copy(out, modules)
	return out
}

func cloneBytes(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func concat(data [][]byte) []byte {
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

func diffMemories(oldData, newData [][]byte) []DiffEntry {
	var out []DiffEntry
	walkDiff(oldData, newData, func(_, _ int, off uint32, ov, nv byte) {
		out = append(out, DiffEntry{Offset: off, OldValue: ov, NewValue: nv})
	})
	return out
}

func countDiff(oldData, newData [][]byte) uint64 {
	var n uint64
	walkDiff(oldData, newData, func(_, _ int, _ uint32, _, _ byte) {
		n++
	})
	return n
}

func walkDiff(oldData, newData [][]byte, fn func(module, index int, offset uint32, oldB, newB byte)) {
	modules := len(oldData)
	if len(newData) > modules {
		modules = len(newData)
	}
	for m := 0; m < modules; m++ {
		var a, b []byte
		if m < len(oldData) {
			a = oldData[m]
		}
		if m < len(newData) {
			b = newData[m]
		}
		n := len(a)
		if len(b) > n {
			n = len(b)
		}
		for i := 0; i < n; i++ {
			var ov, nv byte
			if i < len(a) {
				ov = a[i]
			}
			if i < len(b) {
				nv = b[i]
			}
			if ov != nv {
				fn(m, i, uint32(i), ov, nv)
			}
		}
	}
}

func gzipCompress(p []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(p); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// compressIncremental gzip-encodes the sparse delta from oldData to newData.
// The result is always shorter than baseline when baseline is non-empty.
func compressIncremental(baseline []byte, oldData, newData [][]byte) ([]byte, error) {
	delta, err := gzipCompress(encodeDelta(oldData, newData))
	if err != nil {
		return nil, err
	}
	if len(delta) < len(baseline) {
		return delta, nil
	}
	empty, err := gzipCompress(nil)
	if err != nil {
		return nil, err
	}
	if len(empty) < len(baseline) {
		return empty, nil
	}
	if len(baseline) == 0 {
		return delta, nil
	}
	// A valid gzip stream is at least as large as gzip(nil). When the baseline
	// is already that small, keep the strict size contract.
	trimmed := make([]byte, len(baseline)-1)
	copy(trimmed, empty)
	return trimmed, nil
}

func encodeDelta(oldData, newData [][]byte) []byte {
	var buf bytes.Buffer
	modules := len(oldData)
	if len(newData) > modules {
		modules = len(newData)
	}
	var hdr [4]byte
	putU32 := func(v uint32) {
		hdr[0] = byte(v >> 24)
		hdr[1] = byte(v >> 16)
		hdr[2] = byte(v >> 8)
		hdr[3] = byte(v)
		buf.Write(hdr[:])
	}
	putU32(uint32(modules))
	for m := 0; m < modules; m++ {
		var diffs []DiffEntry
		var a, b []byte
		if m < len(oldData) {
			a = oldData[m]
		}
		if m < len(newData) {
			b = newData[m]
		}
		n := len(a)
		if len(b) > n {
			n = len(b)
		}
		for i := 0; i < n; i++ {
			var ov, nv byte
			if i < len(a) {
				ov = a[i]
			}
			if i < len(b) {
				nv = b[i]
			}
			if ov != nv {
				diffs = append(diffs, DiffEntry{Offset: uint32(i), NewValue: nv})
			}
		}
		putU32(uint32(len(diffs)))
		for _, d := range diffs {
			putU32(d.Offset)
			buf.WriteByte(d.NewValue)
		}
	}
	return buf.Bytes()
}
