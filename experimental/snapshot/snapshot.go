// Package snapshot captures and restores consistent memory state across
// multiple WebAssembly modules.
package snapshot

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero/api"
)

// DiffEntry is a single byte difference between two snapshots.
type DiffEntry struct {
	Offset   uint32
	OldValue byte
	NewValue byte
}

// Snapshot is an immutable capture of memory across modules.
type Snapshot interface {
	Data() [][]byte
	CompressedData() []byte
	Version() uint64
	Tags() map[string]string
	SetTag(key, value string)
	Compare(other Snapshot) []DiffEntry
}

type codedError struct {
	code, msg string
}

func (e *codedError) Error() string { return e.msg }

// ErrorCode returns the machine-readable code of an error returned by this
// package, or "" if none.
func ErrorCode(err error) string {
	var ce *codedError
	if errors.As(err, &ce) {
		return ce.code
	}
	return ""
}

func newErr(code, format string, args ...any) error {
	return &codedError{code: code, msg: fmt.Sprintf(format, args...)}
}

type snapshot struct {
	version    uint64
	modules    []api.Module
	data       [][]byte
	compressed []byte
	modified   uint64
	incr       bool

	mu   sync.RWMutex
	tags map[string]string
}

func copyData(d [][]byte) [][]byte {
	out := make([][]byte, len(d))
	for i, b := range d {
		out[i] = append([]byte(nil), b...)
	}
	return out
}

func (s *snapshot) Data() [][]byte { return copyData(s.data) }

func (s *snapshot) CompressedData() []byte { return append([]byte(nil), s.compressed...) }

func (s *snapshot) Version() uint64 { return s.version }

func (s *snapshot) Tags() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.tags))
	for k, v := range s.tags {
		out[k] = v
	}
	return out
}

func (s *snapshot) SetTag(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tags == nil {
		s.tags = map[string]string{}
	}
	s.tags[key] = value
}

func (s *snapshot) Compare(other Snapshot) []DiffEntry {
	if other == nil {
		return nil
	}
	return diff(s.data, other.Data())
}

func diff(oldD, newD [][]byte) []DiffEntry {
	var out []DiffEntry
	n := len(oldD)
	if len(newD) > n {
		n = len(newD)
	}
	for m := 0; m < n; m++ {
		var a, b []byte
		if m < len(oldD) {
			a = oldD[m]
		}
		if m < len(newD) {
			b = newD[m]
		}
		l := len(a)
		if len(b) > l {
			l = len(b)
		}
		for i := 0; i < l; i++ {
			var x, y byte
			if i < len(a) {
				x = a[i]
			}
			if i < len(b) {
				y = b[i]
			}
			if x != y {
				out = append(out, DiffEntry{Offset: uint32(i), OldValue: x, NewValue: y})
			}
		}
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

// Coordinator captures and restores snapshots across modules.
type Coordinator struct {
	mu      sync.Mutex
	version uint64
}

// NewCoordinator returns a new Coordinator.
func NewCoordinator() *Coordinator { return &Coordinator{} }

func readMemories(mods []api.Module) ([][]byte, error) {
	if len(mods) == 0 {
		return nil, errors.New("snapshot: no modules provided")
	}
	data := make([][]byte, len(mods))
	for i, m := range mods {
		if m == nil || m.IsClosed() {
			return nil, newErr("module_closed", "snapshot: module closed at index %d", i)
		}
		if mem := m.Memory(); mem != nil {
			b, ok := mem.Read(0, mem.Size())
			if !ok {
				return nil, fmt.Errorf("snapshot: failed to read memory of module %d", i)
			}
			data[i] = append([]byte(nil), b...)
		} else {
			data[i] = []byte{}
		}
	}
	return data, nil
}

// CaptureSnapshot captures a full snapshot of the given modules' memories.
func (c *Coordinator) CaptureSnapshot(mods ...api.Module) (Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, err := readMemories(mods)
	if err != nil {
		return nil, err
	}
	c.version++
	return &snapshot{
		version:    c.version,
		modules:    append([]api.Module(nil), mods...),
		data:       data,
		compressed: gzipBytes(bytes.Join(data, nil)),
		tags:       map[string]string{},
	}, nil
}

// CaptureIncremental captures the changes since baseline.
func (c *Coordinator) CaptureIncremental(baseline Snapshot, mods ...api.Module) (Snapshot, error) {
	if baseline == nil {
		return nil, errors.New("snapshot: baseline snapshot is nil")
	}
	base := baseline.Data()
	if len(mods) != len(base) {
		return nil, fmt.Errorf("snapshot: module count mismatch: baseline has %d, got %d", len(base), len(mods))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	data, err := readMemories(mods)
	if err != nil {
		return nil, err
	}

	// Encoding: per module, new length then runs of (offset, length, bytes).
	var enc []byte
	var modified uint64
	for m := range data {
		enc = binary.AppendUvarint(enc, uint64(len(data[m])))
		a, b := base[m], data[m]
		var runs []byte
		nruns := 0
		for i := 0; i < len(b); {
			if i < len(a) && a[i] == b[i] {
				i++
				continue
			}
			start := i
			for i < len(b) && !(i < len(a) && a[i] == b[i]) {
				i++
			}
			runs = binary.AppendUvarint(runs, uint64(start))
			runs = binary.AppendUvarint(runs, uint64(i-start))
			runs = append(runs, b[start:i]...)
			modified += uint64(i - start)
			nruns++
		}
		enc = binary.AppendUvarint(enc, uint64(nruns))
		enc = append(enc, runs...)
	}
	c.version++
	return &snapshot{
		version:    c.version,
		modules:    append([]api.Module(nil), mods...),
		data:       data,
		compressed: gzipBytes(enc),
		modified:   modified,
		incr:       true,
		tags:       map[string]string{},
	}, nil
}

// RestoreSnapshot writes snapshot memory back into the given modules.
func (c *Coordinator) RestoreSnapshot(snap Snapshot, mods ...api.Module) error {
	if snap == nil {
		return errors.New("snapshot: snapshot is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	data := snap.Data()
	if len(mods) > len(data) {
		return newErr("incompatible_module", "snapshot: incompatible module count: snapshot has %d, got %d", len(data), len(mods))
	}
	var captured []api.Module
	if s, ok := snap.(*snapshot); ok {
		captured = s.modules
	}
	for i, m := range mods {
		idx := -1
		for j, cm := range captured {
			if cm == m {
				idx = j
				break
			}
		}
		if idx < 0 && len(mods) == len(data) {
			idx = i
		}
		if idx < 0 {
			continue
		}
		if m == nil || m.IsClosed() {
			return newErr("module_closed", "snapshot: module closed at index %d", i)
		}
		src := data[idx]
		mem := m.Memory()
		var size uint32
		if mem != nil {
			size = mem.Size()
		}
		if uint64(size) < uint64(len(src)) {
			return newErr("insufficient_memory", "snapshot: insufficient memory in module %d: need %d, have %d", i, len(src), size)
		}
		if len(src) > 0 && !mem.Write(0, src) {
			return fmt.Errorf("snapshot: failed to write memory of module %d", i)
		}
	}
	return nil
}

var registry sync.Map

// Register stores c under name, replacing any existing entry.
func Register(name string, c *Coordinator) { registry.Store(name, c) }

// Get returns the Coordinator registered under name.
func Get(name string) (*Coordinator, bool) {
	v, ok := registry.Load(name)
	if !ok {
		return nil, false
	}
	return v.(*Coordinator), true
}

// Unregister removes the Coordinator registered under name.
func Unregister(name string) { registry.Delete(name) }

type ctxKey struct{}

// WithCoordinator returns a context carrying c.
func WithCoordinator(ctx context.Context, c *Coordinator) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}

// GetCoordinator returns the Coordinator in ctx, or nil.
func GetCoordinator(ctx context.Context) *Coordinator {
	c, _ := ctx.Value(ctxKey{}).(*Coordinator)
	return c
}

// SnapshotSummary describes a snapshot.
type SnapshotSummary struct {
	TotalModules  int
	TotalBytes    uint64
	ModifiedBytes uint64
	Version       uint64
}

// Summarize returns a summary of snap.
func Summarize(snap Snapshot) SnapshotSummary {
	if snap == nil {
		return SnapshotSummary{}
	}
	data := snap.Data()
	s := SnapshotSummary{TotalModules: len(data), Version: snap.Version()}
	for _, d := range data {
		s.TotalBytes += uint64(len(d))
	}
	if impl, ok := snap.(*snapshot); ok && impl.incr {
		s.ModifiedBytes = impl.modified
	}
	return s
}

// Chain is an ordered sequence of snapshots.
type Chain struct {
	mu    sync.RWMutex
	snaps []Snapshot
}

// NewChain returns an empty Chain.
func NewChain() *Chain { return &Chain{} }

// Push appends snap to the chain.
func (ch *Chain) Push(snap Snapshot) {
	ch.mu.Lock()
	ch.snaps = append(ch.snaps, snap)
	ch.mu.Unlock()
}

// Head returns the most recent snapshot, or nil.
func (ch *Chain) Head() Snapshot {
	ch.mu.RLock()
	defer ch.mu.RUnlock()
	if len(ch.snaps) == 0 {
		return nil
	}
	return ch.snaps[len(ch.snaps)-1]
}

// Len returns the number of snapshots.
func (ch *Chain) Len() int {
	ch.mu.RLock()
	defer ch.mu.RUnlock()
	return len(ch.snaps)
}

// Snapshots returns a copy of the snapshots, oldest first.
func (ch *Chain) Snapshots() []Snapshot {
	ch.mu.RLock()
	defer ch.mu.RUnlock()
	return append([]Snapshot(nil), ch.snaps...)
}

type wireSnapshot struct {
	Version uint64            `json:"version"`
	Data    [][]byte          `json:"data"`
	Tags    map[string]string `json:"tags"`
}

// MarshalSnapshot encodes snap portably.
func MarshalSnapshot(snap Snapshot) ([]byte, error) {
	if snap == nil {
		return nil, errors.New("snapshot: snapshot is nil")
	}
	return json.Marshal(wireSnapshot{Version: snap.Version(), Data: snap.Data(), Tags: snap.Tags()})
}

// UnmarshalSnapshot decodes data produced by MarshalSnapshot into a full snapshot.
func UnmarshalSnapshot(data []byte) (Snapshot, error) {
	var w wireSnapshot
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, fmt.Errorf("snapshot: invalid encoding: %w", err)
	}
	if w.Data == nil {
		return nil, errors.New("snapshot: invalid encoding: missing data")
	}
	for i := range w.Data {
		if w.Data[i] == nil {
			w.Data[i] = []byte{}
		}
	}
	if w.Tags == nil {
		w.Tags = map[string]string{}
	}
	return &snapshot{
		version:    w.Version,
		data:       w.Data,
		compressed: gzipBytes(bytes.Join(w.Data, nil)),
		tags:       w.Tags,
	}, nil
}
