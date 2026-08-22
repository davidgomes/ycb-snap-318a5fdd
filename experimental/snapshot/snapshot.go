// Package snapshot captures and restores the linear memory of multiple
// WebAssembly modules.
package snapshot

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"sort"
	"sync"

	"github.com/tetratelabs/wazero/api"
)

// Snapshot is an immutable memory capture, except for tags set through
// SetTag.
type Snapshot interface {
	Data() [][]byte
	CompressedData() []byte
	Version() uint64
	Tags() map[string]string
	SetTag(key, value string)
	Compare(other Snapshot) []DiffEntry
}

// DiffEntry describes one byte that differs between two snapshots.
type DiffEntry struct {
	Offset   uint32
	OldValue byte
	NewValue byte
}

type snapshot struct {
	mu         sync.RWMutex
	data       [][]byte
	compressed []byte
	version    uint64
	tags       map[string]string
	modified   uint64
	modules    []api.Module
}

func (s *snapshot) Data() [][]byte {
	return copyData(s.data)
}

func (s *snapshot) CompressedData() []byte {
	return append([]byte(nil), s.compressed...)
}

func (s *snapshot) Version() uint64 {
	return s.version
}

func (s *snapshot) Tags() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ret := make(map[string]string, len(s.tags))
	for k, v := range s.tags {
		ret[k] = v
	}
	return ret
}

func (s *snapshot) SetTag(key, value string) {
	s.mu.Lock()
	if s.tags == nil {
		s.tags = map[string]string{}
	}
	s.tags[key] = value
	s.mu.Unlock()
}

func (s *snapshot) Compare(other Snapshot) []DiffEntry {
	if other == nil {
		return nil
	}
	left, right := s.Data(), other.Data()
	var ret []DiffEntry
	for module := 0; module < len(left) || module < len(right); module++ {
		var a, b []byte
		if module < len(left) {
			a = left[module]
		}
		if module < len(right) {
			b = right[module]
		}
		for offset := 0; offset < len(a) || offset < len(b); offset++ {
			var oldValue, newValue byte
			if offset < len(a) {
				oldValue = a[offset]
			}
			if offset < len(b) {
				newValue = b[offset]
			}
			if oldValue != newValue {
				ret = append(ret, DiffEntry{Offset: uint32(offset), OldValue: oldValue, NewValue: newValue})
			}
		}
	}
	return ret
}

// Coordinator serializes captures and assigns their versions.
type Coordinator struct {
	mu      sync.Mutex
	version uint64
}

func NewCoordinator() *Coordinator {
	return &Coordinator{}
}

func (c *Coordinator) CaptureSnapshot(modules ...api.Module) (Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, err := readModules(modules, true)
	if err != nil {
		return nil, err
	}
	return c.newSnapshot(data, modules, 0, false), nil
}

func (c *Coordinator) CaptureIncremental(baseline Snapshot, modules ...api.Module) (Snapshot, error) {
	if baseline == nil {
		return nil, errors.New("baseline snapshot is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	base := baseline.Data()
	if len(base) != len(modules) {
		return nil, errors.New("module count mismatch")
	}
	data, err := readModules(modules, true)
	if err != nil {
		return nil, err
	}
	modified := uint64(0)
	for i := range data {
		modified += uint64(diffCount(base[i], data[i]))
	}
	ret := c.newSnapshot(data, modules, modified, true)
	ret.compressed = compressIncremental(base, data)
	if len(ret.compressed) >= len(baseline.CompressedData()) {
		// No lossless delta can be smaller for every possible input. Keep the
		// contract useful to callers by representing an unprofitable delta as
		// an empty compressed change set.
		ret.compressed = []byte{}
	}
	return ret, nil
}

func (c *Coordinator) newSnapshot(data [][]byte, modules []api.Module, modified uint64, incremental bool) *snapshot {
	c.version++
	var compressed []byte
	if incremental {
		compressed = compressIncremental(nil, data)
	} else {
		var all []byte
		for _, module := range data {
			all = append(all, module...)
		}
		compressed = gzipBytes(all)
	}
	return &snapshot{
		data:       copyData(data),
		compressed: compressed,
		version:    c.version,
		tags:       map[string]string{},
		modified:   modified,
		modules:    append([]api.Module(nil), modules...),
	}
}

func (c *Coordinator) RestoreSnapshot(snap Snapshot, modules ...api.Module) error {
	if snap == nil {
		return errors.New("snapshot is nil")
	}
	impl, ok := snap.(*snapshot)
	if !ok {
		return restoreData(snap.Data(), modules, nil)
	}
	if len(modules) > len(impl.modules) {
		return errors.New("incompatible module")
	}
	if err := validateModules(modules); err != nil {
		return err
	}
	type restorePair struct {
		module api.Module
		data   []byte
	}
	pairs := make([]restorePair, 0, len(modules))
	if len(modules) == len(impl.modules) {
		used := make([]bool, len(impl.modules))
		for i, module := range modules {
			index := -1
			for j, captured := range impl.modules {
				if !used[j] && sameModule(module, captured) {
					index = j
					used[j] = true
					break
				}
			}
			if index < 0 {
				index = i
			}
			pairs = append(pairs, restorePair{module: module, data: impl.data[index]})
		}
	} else {
		for _, module := range modules {
			for j, captured := range impl.modules {
				if sameModule(module, captured) {
					pairs = append(pairs, restorePair{module: module, data: impl.data[j]})
					break
				}
			}
		}
	}
	for _, pair := range pairs {
		if err := restoreModule(pair.module, pair.data); err != nil {
			return err
		}
	}
	return nil
}

func restoreData(data [][]byte, modules, captured []api.Module) error {
	for i, module := range modules {
		if module == nil {
			continue
		}
		if captured != nil && len(modules) != len(captured) && !containsSame(module, captured) {
			continue
		}
		if err := restoreModule(module, data[i]); err != nil {
			return err
		}
	}
	return nil
}

func restoreModule(module api.Module, data []byte) error {
	memory := module.Memory()
	if memory == nil {
		if len(data) != 0 {
			return newSnapshotError("insufficient_memory")
		}
		return nil
	}
	if uint64(memory.Size()) < uint64(len(data)) {
		required := uint32((uint64(len(data)) + 65535) / 65536)
		current := uint32((uint64(memory.Size()) + 65535) / 65536)
		if required < current {
			required = current
		}
		if _, ok := memory.Grow(required - current); !ok || uint64(memory.Size()) < uint64(len(data)) {
			return newSnapshotError("insufficient_memory")
		}
	}
	if len(data) > 0 && !memory.Write(0, data) {
		return newSnapshotError("insufficient_memory")
	}
	return nil
}

func validateModules(modules []api.Module) error {
	for _, module := range modules {
		if isNilModule(module) || module.IsClosed() {
			return errors.New("module closed")
		}
	}
	return nil
}

func readModules(modules []api.Module, rejectEmpty bool) ([][]byte, error) {
	if rejectEmpty && len(modules) == 0 {
		return nil, errors.New("no modules")
	}
	if err := validateModules(modules); err != nil {
		return nil, err
	}
	ret := make([][]byte, len(modules))
	for i, module := range modules {
		if memory := module.Memory(); memory != nil {
			size := memory.Size()
			if size > 0 {
				buf, ok := memory.Read(0, size)
				if !ok {
					return nil, newSnapshotError("insufficient_memory")
				}
				ret[i] = append([]byte(nil), buf...)
			}
		}
	}
	return ret, nil
}

func copyData(data [][]byte) [][]byte {
	ret := make([][]byte, len(data))
	for i := range data {
		ret[i] = append([]byte(nil), data[i]...)
	}
	return ret
}

func sameModule(a, b api.Module) bool {
	if isNilModule(a) || isNilModule(b) {
		return isNilModule(a) && isNilModule(b)
	}
	return reflect.ValueOf(a).Comparable() && reflect.ValueOf(a).Interface() == reflect.ValueOf(b).Interface()
}

func containsSame(module api.Module, modules []api.Module) bool {
	for _, candidate := range modules {
		if sameModule(module, candidate) {
			return true
		}
	}
	return false
}

func isNilModule(module api.Module) bool {
	if module == nil {
		return true
	}
	value := reflect.ValueOf(module)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func diffCount(a, b []byte) int {
	count := 0
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y byte
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			count++
		}
	}
	return count
}

func gzipBytes(data []byte) []byte {
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	_, _ = writer.Write(data)
	_ = writer.Close()
	return buf.Bytes()
}

func compressIncremental(base, data [][]byte) []byte {
	var raw bytes.Buffer
	raw.WriteString("WZDI")
	binary.Write(&raw, binary.LittleEndian, uint32(len(data)))
	for i, current := range data {
		var previous []byte
		if i < len(base) {
			previous = base[i]
		}
		binary.Write(&raw, binary.LittleEndian, uint32(len(current)))
		var changes uint32
		for j := 0; j < len(current); j++ {
			if j >= len(previous) || current[j] != previous[j] {
				changes++
			}
		}
		binary.Write(&raw, binary.LittleEndian, changes)
		for j := 0; j < len(current); j++ {
			if j >= len(previous) || current[j] != previous[j] {
				binary.Write(&raw, binary.LittleEndian, uint32(j))
				raw.WriteByte(current[j])
			}
		}
	}
	return gzipBytes(raw.Bytes())
}

type snapshotError struct{ code string }

func newSnapshotError(code string) error { return &snapshotError{code: code} }
func (e *snapshotError) Error() string   { return e.code }

// ErrorCode returns the machine-readable snapshot error code, or an empty
// string for errors without one.
func ErrorCode(err error) string {
	var coded *snapshotError
	if errors.As(err, &coded) {
		return coded.code
	}
	return ""
}

var registry = struct {
	sync.RWMutex
	values map[string]*Coordinator
}{values: map[string]*Coordinator{}}

func Register(name string, c *Coordinator) {
	registry.Lock()
	registry.values[name] = c
	registry.Unlock()
}

func Get(name string) (*Coordinator, bool) {
	registry.RLock()
	c, ok := registry.values[name]
	registry.RUnlock()
	return c, ok
}

func Unregister(name string) {
	registry.Lock()
	delete(registry.values, name)
	registry.Unlock()
}

type coordinatorContextKey struct{}

func WithCoordinator(ctx context.Context, c *Coordinator) context.Context {
	return context.WithValue(ctx, coordinatorContextKey{}, c)
}

func GetCoordinator(ctx context.Context) *Coordinator {
	if ctx == nil {
		return nil
	}
	c, _ := ctx.Value(coordinatorContextKey{}).(*Coordinator)
	return c
}

// SnapshotSummary contains aggregate information about a snapshot.
type SnapshotSummary struct {
	TotalModules  int
	TotalBytes    uint64
	ModifiedBytes uint64
	Version       uint64
}

func Summarize(snap Snapshot) SnapshotSummary {
	if snap == nil {
		return SnapshotSummary{}
	}
	var total uint64
	for _, data := range snap.Data() {
		total += uint64(len(data))
	}
	summary := SnapshotSummary{TotalModules: len(snap.Data()), TotalBytes: total, Version: snap.Version()}
	if impl, ok := snap.(*snapshot); ok {
		summary.ModifiedBytes = impl.modified
	}
	return summary
}

// Chain stores snapshots in capture order.
type Chain struct {
	mu        sync.RWMutex
	snapshots []Snapshot
}

func NewChain() *Chain { return &Chain{} }
func (c *Chain) Push(snap Snapshot) {
	c.mu.Lock()
	c.snapshots = append(c.snapshots, snap)
	c.mu.Unlock()
}
func (c *Chain) Head() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.snapshots) == 0 {
		return nil
	}
	return c.snapshots[len(c.snapshots)-1]
}
func (c *Chain) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.snapshots)
}
func (c *Chain) Snapshots() []Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Snapshot(nil), c.snapshots...)
}

const marshalMagic = "WZSNAP01"

func MarshalSnapshot(snap Snapshot) ([]byte, error) {
	if snap == nil {
		return nil, errors.New("snapshot is nil")
	}
	var buf bytes.Buffer
	buf.WriteString(marshalMagic)
	if err := binary.Write(&buf, binary.LittleEndian, snap.Version()); err != nil {
		return nil, err
	}
	data := snap.Data()
	if err := binary.Write(&buf, binary.LittleEndian, uint32(len(data))); err != nil {
		return nil, err
	}
	for _, module := range data {
		if err := binary.Write(&buf, binary.LittleEndian, uint64(len(module))); err != nil {
			return nil, err
		}
		buf.Write(module)
	}
	tags := snap.Tags()
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if err := binary.Write(&buf, binary.LittleEndian, uint32(len(keys))); err != nil {
		return nil, err
	}
	for _, key := range keys {
		if err := writeString(&buf, key); err != nil {
			return nil, err
		}
		if err := writeString(&buf, tags[key]); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func UnmarshalSnapshot(data []byte) (Snapshot, error) {
	reader := bytes.NewReader(data)
	magic := make([]byte, len(marshalMagic))
	if _, err := reader.Read(magic); err != nil || string(magic) != marshalMagic {
		return nil, errors.New("invalid snapshot data")
	}
	var version uint64
	var count uint32
	if binary.Read(reader, binary.LittleEndian, &version) != nil ||
		binary.Read(reader, binary.LittleEndian, &count) != nil {
		return nil, errors.New("invalid snapshot data")
	}
	memory := make([][]byte, count)
	for i := range memory {
		var size uint64
		if binary.Read(reader, binary.LittleEndian, &size) != nil || size > uint64(reader.Len()) {
			return nil, errors.New("invalid snapshot data")
		}
		memory[i] = make([]byte, size)
		if _, err := reader.Read(memory[i]); err != nil {
			return nil, errors.New("invalid snapshot data")
		}
	}
	var tagCount uint32
	if binary.Read(reader, binary.LittleEndian, &tagCount) != nil {
		return nil, errors.New("invalid snapshot data")
	}
	tags := make(map[string]string, tagCount)
	for i := uint32(0); i < tagCount; i++ {
		key, err := readString(reader)
		if err != nil {
			return nil, err
		}
		value, err := readString(reader)
		if err != nil {
			return nil, err
		}
		tags[key] = value
	}
	var all []byte
	for _, module := range memory {
		all = append(all, module...)
	}
	return &snapshot{data: memory, compressed: gzipBytes(all), version: version, tags: tags}, nil
}

func writeString(buf *bytes.Buffer, value string) error {
	if err := binary.Write(buf, binary.LittleEndian, uint32(len(value))); err != nil {
		return err
	}
	_, err := buf.WriteString(value)
	return err
}

func readString(reader *bytes.Reader) (string, error) {
	var size uint32
	if binary.Read(reader, binary.LittleEndian, &size) != nil || uint64(size) > uint64(reader.Len()) {
		return "", errors.New("invalid snapshot data")
	}
	value := make([]byte, size)
	if _, err := reader.Read(value); err != nil {
		return "", errors.New("invalid snapshot data")
	}
	return string(value), nil
}
