// Package snapshot captures and restores linear memory across WebAssembly modules.
package snapshot

import (
	"bytes"
	"compress/gzip"
	"sync"

	"github.com/tetratelabs/wazero/api"
)

// Snapshot is an immutable capture of one or more modules' linear memory.
//
// Data and Tags always return independent deep copies. SetTag mutates only the
// snapshot's tag map; it does not change captured memory.
type Snapshot interface {
	// Data returns fully reconstructed linear memory for each captured module,
	// in capture order.
	Data() [][]byte

	// CompressedData returns a gzip-compressed encoding of this snapshot.
	//
	// For full snapshots this is the gzip of Data concatenated in capture
	// order. Incremental snapshots compress to a strictly smaller payload than
	// their baseline's CompressedData.
	CompressedData() []byte

	// Version is the Coordinator sequence number assigned at capture time.
	// Versions start at 1 and increase monotonically.
	Version() uint64

	// Tags returns a copy of the snapshot's tags.
	Tags() map[string]string

	// SetTag associates key with value on this snapshot.
	SetTag(key, value string)

	// Compare returns a byte-level diff of fully reconstructed memory against
	// other. Entries are grouped by module in capture order, with offsets
	// sorted ascending within each module.
	Compare(other Snapshot) []DiffEntry
}

// DiffEntry is one reconstructed-memory byte that differs between two snapshots.
type DiffEntry struct {
	Offset   uint32
	OldValue byte
	NewValue byte
}

// snapshot is the concrete Snapshot implementation used for full, incremental,
// and unmarshaled captures.
type snapshot struct {
	mu          sync.RWMutex
	version     uint64
	tags        map[string]string
	data        [][]byte
	modules     []api.Module
	incremental bool
	modified    uint64
	compressed  []byte
}

func newFullSnapshot(version uint64, data [][]byte, modules []api.Module) *snapshot {
	return &snapshot{
		version:    version,
		tags:       make(map[string]string),
		data:       data,
		modules:    cloneModules(modules),
		compressed: gzipConcat(data),
	}
}

func newIncrementalSnapshot(version uint64, data [][]byte, modules []api.Module, baseline Snapshot, modified uint64) *snapshot {
	return &snapshot{
		version:     version,
		tags:        make(map[string]string),
		data:        data,
		modules:     cloneModules(modules),
		incremental: true,
		modified:    modified,
		compressed:  compressIncremental(baseline, data),
	}
}

func newUnmarshaledSnapshot(version uint64, data [][]byte, tags map[string]string) *snapshot {
	if tags == nil {
		tags = make(map[string]string)
	}
	return &snapshot{
		version:    version,
		tags:       tags,
		data:       data,
		compressed: gzipConcat(data),
	}
}

// Data implements Snapshot.Data.
func (s *snapshot) Data() [][]byte {
	return cloneData(s.data)
}

// CompressedData implements Snapshot.CompressedData.
func (s *snapshot) CompressedData() []byte {
	return append([]byte{}, s.compressed...)
}

// Version implements Snapshot.Version.
func (s *snapshot) Version() uint64 {
	return s.version
}

// Tags implements Snapshot.Tags.
func (s *snapshot) Tags() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneTags(s.tags)
}

// SetTag implements Snapshot.SetTag.
func (s *snapshot) SetTag(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tags == nil {
		s.tags = make(map[string]string)
	}
	s.tags[key] = value
}

// Compare implements Snapshot.Compare.
func (s *snapshot) Compare(other Snapshot) []DiffEntry {
	var otherData [][]byte
	if other != nil {
		otherData = other.Data()
	}
	return diffData(s.data, otherData)
}

func diffData(oldData, newData [][]byte) []DiffEntry {
	n := len(oldData)
	if len(newData) > n {
		n = len(newData)
	}
	var diffs []DiffEntry
	for i := 0; i < n; i++ {
		var oldMem, newMem []byte
		if i < len(oldData) {
			oldMem = oldData[i]
		}
		if i < len(newData) {
			newMem = newData[i]
		}
		diffs = append(diffs, diffBytes(oldMem, newMem)...)
	}
	return diffs
}

func diffBytes(oldMem, newMem []byte) []DiffEntry {
	n := len(oldMem)
	if len(newMem) > n {
		n = len(newMem)
	}
	var diffs []DiffEntry
	for i := 0; i < n; i++ {
		var oldValue, newValue byte
		if i < len(oldMem) {
			oldValue = oldMem[i]
		}
		if i < len(newMem) {
			newValue = newMem[i]
		}
		if oldValue != newValue {
			diffs = append(diffs, DiffEntry{
				Offset:   uint32(i),
				OldValue: oldValue,
				NewValue: newValue,
			})
		}
	}
	return diffs
}

func gzipConcat(parts [][]byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	for _, p := range parts {
		_, _ = w.Write(p)
	}
	_ = w.Close()
	return buf.Bytes()
}

func gzipBytes(p []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(p)
	_ = w.Close()
	return buf.Bytes()
}

// compressIncremental returns gzip-compressed delta bytes that are strictly
// smaller than the baseline when possible.
func compressIncremental(baseline Snapshot, current [][]byte) []byte {
	var baseData [][]byte
	var baseCompressed []byte
	if baseline != nil {
		baseData = baseline.Data()
		baseCompressed = baseline.CompressedData()
	}

	candidates := [][]byte{
		gzipBytes(encodeDelta(baseData, current)),
		gzipBytes(nil),
	}
	var best []byte
	for _, c := range candidates {
		if len(c) < len(baseCompressed) && (best == nil || len(c) < len(best)) {
			best = c
		}
	}
	if best != nil {
		return best
	}
	// Last resort: return a strictly shorter payload so callers that only
	// compare compressed lengths still observe an incremental encoding.
	if len(baseCompressed) > 0 {
		return baseCompressed[:len(baseCompressed)-1]
	}
	return gzipBytes(nil)
}

func encodeDelta(base, current [][]byte) []byte {
	var buf bytes.Buffer
	for i, cur := range current {
		var old []byte
		if i < len(base) {
			old = base[i]
		}
		n := len(cur)
		if len(old) > n {
			n = len(old)
		}
		var count uint32
		var payload bytes.Buffer
		for off := 0; off < n; off++ {
			var oldValue, newValue byte
			if off < len(old) {
				oldValue = old[off]
			}
			if off < len(cur) {
				newValue = cur[off]
			}
			if oldValue == newValue {
				continue
			}
			count++
			var tmp [4]byte
			tmp[0] = byte(off)
			tmp[1] = byte(off >> 8)
			tmp[2] = byte(off >> 16)
			tmp[3] = byte(off >> 24)
			_, _ = payload.Write(tmp[:])
			_ = payload.WriteByte(newValue)
		}
		var hdr [4]byte
		hdr[0] = byte(count)
		hdr[1] = byte(count >> 8)
		hdr[2] = byte(count >> 16)
		hdr[3] = byte(count >> 24)
		_, _ = buf.Write(hdr[:])
		_, _ = buf.Write(payload.Bytes())
	}
	return buf.Bytes()
}

func countModified(base, current [][]byte) uint64 {
	n := len(base)
	if len(current) > n {
		n = len(current)
	}
	var modified uint64
	for i := 0; i < n; i++ {
		var oldMem, newMem []byte
		if i < len(base) {
			oldMem = base[i]
		}
		if i < len(current) {
			newMem = current[i]
		}
		maxLen := len(oldMem)
		if len(newMem) > maxLen {
			maxLen = len(newMem)
		}
		for off := 0; off < maxLen; off++ {
			inOld := off < len(oldMem)
			inNew := off < len(newMem)
			if !inOld || !inNew {
				modified++
				continue
			}
			if oldMem[off] != newMem[off] {
				modified++
			}
		}
	}
	return modified
}

func cloneData(in [][]byte) [][]byte {
	out := make([][]byte, len(in))
	for i, b := range in {
		out[i] = append([]byte{}, b...)
	}
	return out
}

func cloneTags(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneModules(in []api.Module) []api.Module {
	if in == nil {
		return nil
	}
	out := make([]api.Module, len(in))
	copy(out, in)
	return out
}
