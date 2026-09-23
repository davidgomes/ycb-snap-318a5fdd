package snapshot

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

const snapshotMagic = "WSNAP001"

var errInvalidSnapshot = errors.New("invalid snapshot")

// MarshalSnapshot encodes the fully reconstructed memory, version, and tags of snap.
// The result is portable across architectures. A nil snapshot or encode failure
// returns an error.
func MarshalSnapshot(snap Snapshot) ([]byte, error) {
	if snap == nil {
		return nil, errors.New("nil snapshot")
	}
	data := snap.Data()
	tags := snap.Tags()

	var buf bytes.Buffer
	buf.WriteString(snapshotMagic)
	var u64b [8]byte
	binary.LittleEndian.PutUint64(u64b[:], snap.Version())
	buf.Write(u64b[:])
	if err := writeU32(&buf, uint32(len(data))); err != nil {
		return nil, err
	}
	for _, mem := range data {
		if err := writeU64(&buf, uint64(len(mem))); err != nil {
			return nil, err
		}
		if _, err := buf.Write(mem); err != nil {
			return nil, err
		}
	}
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if err := writeU32(&buf, uint32(len(keys))); err != nil {
		return nil, err
	}
	for _, k := range keys {
		if err := writeString(&buf, k); err != nil {
			return nil, err
		}
		if err := writeString(&buf, tags[k]); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// UnmarshalSnapshot decodes a snapshot produced by MarshalSnapshot.
// The result is a full snapshot: incremental inputs are reconstructed, so
// Summarize reports no modified bytes. Invalid input returns an error.
func UnmarshalSnapshot(data []byte) (Snapshot, error) {
	r := reader{b: data}
	magic, err := r.bytes(len(snapshotMagic))
	if err != nil || string(magic) != snapshotMagic {
		return nil, invalidSnapshot("magic")
	}
	version, err := r.u64()
	if err != nil {
		return nil, invalidSnapshot("version")
	}
	count, err := r.u32()
	if err != nil {
		return nil, invalidSnapshot("module count")
	}
	if uint64(count) > uint64(len(r.b)-r.i)/8 {
		return nil, invalidSnapshot("module count")
	}
	memories := make([][]byte, count)
	for i := range memories {
		n, err := r.u64()
		if err != nil {
			return nil, invalidSnapshot("memory length")
		}
		if n > uint64(len(r.b)-r.i) {
			return nil, invalidSnapshot("memory length")
		}
		raw, err := r.bytes(int(n))
		if err != nil {
			return nil, invalidSnapshot("memory")
		}
		memories[i] = cloneBytes(raw)
	}
	tagCount, err := r.u32()
	if err != nil {
		return nil, invalidSnapshot("tag count")
	}
	if uint64(tagCount) > uint64(len(r.b)-r.i)/8 {
		return nil, invalidSnapshot("tag count")
	}
	tags := make(map[string]string, tagCount)
	for i := uint32(0); i < tagCount; i++ {
		key, err := r.str()
		if err != nil {
			return nil, invalidSnapshot("tag key")
		}
		val, err := r.str()
		if err != nil {
			return nil, invalidSnapshot("tag value")
		}
		tags[key] = val
	}
	if r.i != len(r.b) {
		return nil, invalidSnapshot("trailing bytes")
	}
	return &memorySnapshot{
		version:  version,
		memories: memories,
		tags:     tags,
	}, nil
}

func invalidSnapshot(reason string) error {
	return fmt.Errorf("%w: %s", errInvalidSnapshot, reason)
}

func writeU32(buf *bytes.Buffer, v uint32) error {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	_, err := buf.Write(b[:])
	return err
}

func writeU64(buf *bytes.Buffer, v uint64) error {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	_, err := buf.Write(b[:])
	return err
}

func writeString(buf *bytes.Buffer, s string) error {
	if err := writeU32(buf, uint32(len(s))); err != nil {
		return err
	}
	_, err := buf.WriteString(s)
	return err
}

type reader struct {
	b []byte
	i int
}

func (r *reader) u32() (uint32, error) {
	raw, err := r.bytes(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(raw), nil
}

func (r *reader) u64() (uint64, error) {
	raw, err := r.bytes(8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(raw), nil
}

func (r *reader) str() (string, error) {
	n, err := r.u32()
	if err != nil {
		return "", err
	}
	if uint64(n) > uint64(len(r.b)-r.i) {
		return "", errInvalidSnapshot
	}
	raw, err := r.bytes(int(n))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (r *reader) bytes(n int) ([]byte, error) {
	if n < 0 || r.i > len(r.b)-n {
		return nil, errInvalidSnapshot
	}
	out := r.b[r.i : r.i+n]
	r.i += n
	return out, nil
}
