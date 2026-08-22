package snapshot

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"sync"

	"github.com/tetratelabs/wazero/api"
)

type DiffEntry struct {
	Offset             uint32
	OldValue, NewValue byte
}

type Error struct{ code string }

func (e *Error) Error() string { return e.code }
func ErrorCode(err error) string {
	if e, ok := err.(*Error); ok {
		return e.code
	}
	return ""
}

type Snapshot interface {
	Data() [][]byte
	CompressedData() []byte
	Version() uint64
	Tags() map[string]string
	SetTag(key, value string)
	Compare(other Snapshot) []DiffEntry
}

type snapshot struct {
	data        [][]byte
	compressed  []byte
	version     uint64
	tags        map[string]string
	incremental bool
	modified    uint64
	modules     []api.Module
}

func cloneData(d [][]byte) [][]byte {
	r := make([][]byte, len(d))
	for i := range d {
		r[i] = append([]byte(nil), d[i]...)
	}
	return r
}
func (s *snapshot) Data() [][]byte         { return cloneData(s.data) }
func (s *snapshot) CompressedData() []byte { return append([]byte(nil), s.compressed...) }
func (s *snapshot) Version() uint64        { return s.version }
func (s *snapshot) Tags() map[string]string {
	r := map[string]string{}
	for k, v := range s.tags {
		r[k] = v
	}
	return r
}
func (s *snapshot) SetTag(k, v string) { s.tags[k] = v }
func (s *snapshot) Compare(o Snapshot) []DiffEntry {
	if o == nil {
		return nil
	}
	a, b := s.data, o.Data()
	out := []DiffEntry{}
	var base uint32
	for i := 0; i < len(a) && i < len(b); i++ {
		n := len(a[i])
		if len(b[i]) > n {
			n = len(b[i])
		}
		for j := 0; j < n; j++ {
			var x, y byte
			if j < len(a[i]) {
				x = a[i][j]
			}
			if j < len(b[i]) {
				y = b[i][j]
			}
			if x != y {
				out = append(out, DiffEntry{base + uint32(j), x, y})
			}
		}
		base += uint32(len(a[i]))
	}
	return out
}

type Coordinator struct {
	mu      sync.Mutex
	version uint64
}

func NewCoordinator() *Coordinator { return &Coordinator{} }
func readModules(ms []api.Module) ([][]byte, error) {
	if len(ms) == 0 {
		return nil, errors.New("no modules")
	}
	d := make([][]byte, len(ms))
	for i, m := range ms {
		if m == nil || m.IsClosed() {
			return nil, errors.New("module closed")
		}
		mem := m.Memory()
		if mem == nil {
			d[i] = []byte{}
			continue
		}
		n := mem.Size()
		b, ok := mem.Read(0, n)
		if !ok {
			return nil, errors.New("module memory read failed")
		}
		d[i] = append([]byte(nil), b...)
	}
	return d, nil
}
func compress(d [][]byte) []byte {
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	for _, x := range d {
		_, _ = z.Write(x)
	}
	_ = z.Close()
	return b.Bytes()
}
func (c *Coordinator) next() uint64 { c.version++; return c.version }
func (c *Coordinator) CaptureSnapshot(ms ...api.Module) (Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, e := readModules(ms)
	if e != nil {
		return nil, e
	}
	return &snapshot{data: d, compressed: compress(d), version: c.next(), tags: map[string]string{}, modules: append([]api.Module(nil), ms...)}, nil
}
func (c *Coordinator) CaptureIncremental(base Snapshot, ms ...api.Module) (Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if base == nil {
		return nil, errors.New("baseline snapshot is nil")
	}
	if len(ms) != len(base.Data()) {
		return nil, errors.New("module count mismatch")
	}
	d, e := readModules(ms)
	if e != nil {
		return nil, e
	}
	old := base.Data()
	changed := uint64(0)
	for i := range d {
		for j := 0; j < len(d[i]) && j < len(old[i]); j++ {
			if d[i][j] != old[i][j] {
				changed++
			}
		}
		if len(d[i]) > len(old[i]) {
			changed += uint64(len(d[i]) - len(old[i]))
		}
	}
	return &snapshot{data: d, compressed: compress(d), version: c.next(), tags: base.Tags(), incremental: true, modified: changed, modules: append([]api.Module(nil), ms...)}, nil
}
func (c *Coordinator) RestoreSnapshot(s Snapshot, ms ...api.Module) error {
	if s == nil {
		return errors.New("snapshot is nil")
	}
	d := s.Data()
	if len(ms) > len(d) {
		return errors.New("incompatible module")
	}
	targets := make([]api.Module, len(d))
	if x, ok := s.(*snapshot); ok {
		for _, m := range ms {
			for j, captured := range x.modules {
				if m == captured {
					targets[j] = m
				}
			}
		}
	}
	if len(ms) == len(d) {
		for i := range targets {
			if targets[i] == nil {
				targets[i] = ms[i]
			}
		}
	}
	for i, m := range targets {
		if m == nil {
			continue
		}
		if m.IsClosed() {
			return errors.New("module closed")
		}
		mem := m.Memory()
		if mem == nil || uint64(mem.Size()) < uint64(len(d[i])) {
			return &Error{code: "insufficient_memory"}
		}
		if !mem.Write(0, d[i]) {
			return &Error{code: "insufficient_memory"}
		}
	}
	return nil
}

var registry struct {
	sync.RWMutex
	m map[string]*Coordinator
}

func Register(n string, c *Coordinator) {
	registry.Lock()
	defer registry.Unlock()
	if registry.m == nil {
		registry.m = map[string]*Coordinator{}
	}
	registry.m[n] = c
}
func Get(n string) (*Coordinator, bool) {
	registry.RLock()
	defer registry.RUnlock()
	c, ok := registry.m[n]
	return c, ok
}
func Unregister(n string) { registry.Lock(); defer registry.Unlock(); delete(registry.m, n) }

type ctxKey struct{}

func WithCoordinator(ctx context.Context, c *Coordinator) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}
func GetCoordinator(ctx context.Context) *Coordinator {
	c, _ := ctx.Value(ctxKey{}).(*Coordinator)
	return c
}

type SnapshotSummary struct {
	TotalModules                       int
	TotalBytes, ModifiedBytes, Version uint64
}

func Summarize(s Snapshot) SnapshotSummary {
	r := SnapshotSummary{Version: s.Version()}
	for _, d := range s.Data() {
		r.TotalModules++
		r.TotalBytes += uint64(len(d))
	}
	if x, ok := s.(*snapshot); ok && x.incremental {
		r.ModifiedBytes = x.modified
	}
	return r
}

type Chain struct {
	mu sync.RWMutex
	s  []Snapshot
}

func NewChain() *Chain           { return &Chain{} }
func (c *Chain) Push(s Snapshot) { c.mu.Lock(); defer c.mu.Unlock(); c.s = append(c.s, s) }
func (c *Chain) Head() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.s) == 0 {
		return nil
	}
	return c.s[len(c.s)-1]
}
func (c *Chain) Len() int { c.mu.RLock(); defer c.mu.RUnlock(); return len(c.s) }
func (c *Chain) Snapshots() []Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Snapshot(nil), c.s...)
}
func MarshalSnapshot(s Snapshot) ([]byte, error) {
	if s == nil {
		return nil, errors.New("nil snapshot")
	}
	var b bytes.Buffer
	d := s.Data()
	binary.Write(&b, binary.LittleEndian, uint32(len(d)))
	for _, x := range d {
		binary.Write(&b, binary.LittleEndian, uint64(len(x)))
		b.Write(x)
	}
	binary.Write(&b, binary.LittleEndian, s.Version())
	t := s.Tags()
	binary.Write(&b, binary.LittleEndian, uint32(len(t)))
	for k, v := range t {
		binary.Write(&b, binary.LittleEndian, uint32(len(k)))
		b.WriteString(k)
		binary.Write(&b, binary.LittleEndian, uint32(len(v)))
		b.WriteString(v)
	}
	return b.Bytes(), nil
}
func UnmarshalSnapshot(data []byte) (Snapshot, error) {
	r := bytes.NewReader(data)
	var n uint32
	if binary.Read(r, binary.LittleEndian, &n) != nil {
		return nil, errors.New("invalid snapshot")
	}
	d := make([][]byte, n)
	for i := range d {
		var z uint64
		if binary.Read(r, binary.LittleEndian, &z) != nil || z > uint64(r.Len()) {
			return nil, errors.New("invalid snapshot")
		}
		d[i] = make([]byte, z)
		if _, e := io.ReadFull(r, d[i]); e != nil {
			return nil, e
		}
	}
	var v uint64
	if binary.Read(r, binary.LittleEndian, &v) != nil {
		return nil, errors.New("invalid snapshot")
	}
	var nt uint32
	if binary.Read(r, binary.LittleEndian, &nt) != nil {
		return nil, errors.New("invalid snapshot")
	}
	t := map[string]string{}
	for i := uint32(0); i < nt; i++ {
		var a, b uint32
		if binary.Read(r, binary.LittleEndian, &a) != nil || a > uint32(r.Len()) {
			return nil, errors.New("invalid snapshot")
		}
		k := make([]byte, a)
		io.ReadFull(r, k)
		if binary.Read(r, binary.LittleEndian, &b) != nil || b > uint32(r.Len()) {
			return nil, errors.New("invalid snapshot")
		}
		x := make([]byte, b)
		io.ReadFull(r, x)
		t[string(k)] = string(x)
	}
	return &snapshot{data: d, compressed: compress(d), version: v, tags: t}, nil
}
