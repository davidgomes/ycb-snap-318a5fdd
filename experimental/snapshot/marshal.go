package snapshot

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
)

const (
	snapshotMagic         = "WZS1"
	snapshotFormatVersion = uint16(1)
)

// MarshalSnapshot encodes snap's reconstructed memory, version, and tags in a
// portable big-endian layout. The snapshot produced by UnmarshalSnapshot is
// always a full snapshot.
func MarshalSnapshot(snap Snapshot) ([]byte, error) {
	if snap == nil {
		return nil, errNilSnapshot
	}
	data := snap.Data()
	tags := snap.Tags()

	var buf bytes.Buffer
	buf.WriteString(snapshotMagic)
	if err := binary.Write(&buf, binary.BigEndian, snapshotFormatVersion); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.BigEndian, snap.Version()); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.BigEndian, uint32(len(data))); err != nil {
		return nil, err
	}
	for _, mod := range data {
		if err := binary.Write(&buf, binary.BigEndian, uint64(len(mod))); err != nil {
			return nil, err
		}
		if _, err := buf.Write(mod); err != nil {
			return nil, err
		}
	}

	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if err := binary.Write(&buf, binary.BigEndian, uint32(len(keys))); err != nil {
		return nil, err
	}
	for _, k := range keys {
		if err := writeBytes(&buf, []byte(k)); err != nil {
			return nil, err
		}
		if err := writeBytes(&buf, []byte(tags[k])); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func writeBytes(buf *bytes.Buffer, b []byte) error {
	if err := binary.Write(buf, binary.BigEndian, uint32(len(b))); err != nil {
		return err
	}
	_, err := buf.Write(b)
	return err
}

// UnmarshalSnapshot decodes a snapshot produced by MarshalSnapshot.
// The result is a full snapshot: incremental inputs are reconstructed.
func UnmarshalSnapshot(data []byte) (Snapshot, error) {
	if len(data) == 0 {
		return nil, errors.New("invalid snapshot encoding")
	}
	r := &sliceReader{b: data}
	magic, err := r.take(len(snapshotMagic))
	if err != nil || string(magic) != snapshotMagic {
		return nil, errors.New("invalid snapshot encoding")
	}
	var format uint16
	if err := binary.Read(r, binary.BigEndian, &format); err != nil {
		return nil, invalidSnapshot(err)
	}
	if format != snapshotFormatVersion {
		return nil, fmt.Errorf("invalid snapshot encoding: unsupported format %d", format)
	}
	var version uint64
	if err := binary.Read(r, binary.BigEndian, &version); err != nil {
		return nil, invalidSnapshot(err)
	}
	var moduleCount uint32
	if err := binary.Read(r, binary.BigEndian, &moduleCount); err != nil {
		return nil, invalidSnapshot(err)
	}
	if moduleCount > uint32(len(r.b)-r.i) {
		return nil, errors.New("invalid snapshot encoding")
	}
	mem := make([][]byte, moduleCount)
	for i := range mem {
		var n uint64
		if err := binary.Read(r, binary.BigEndian, &n); err != nil {
			return nil, invalidSnapshot(err)
		}
		if n > uint64(len(r.b)-r.i) {
			return nil, errors.New("invalid snapshot encoding")
		}
		raw, err := r.take(int(n))
		if err != nil {
			return nil, invalidSnapshot(err)
		}
		mem[i] = cloneBytes(raw)
	}
	var tagCount uint32
	if err := binary.Read(r, binary.BigEndian, &tagCount); err != nil {
		return nil, invalidSnapshot(err)
	}
	if tagCount > uint32(len(r.b)-r.i) {
		return nil, errors.New("invalid snapshot encoding")
	}
	tags := make(map[string]string, tagCount)
	for i := uint32(0); i < tagCount; i++ {
		key, err := readCounted(r)
		if err != nil {
			return nil, invalidSnapshot(err)
		}
		val, err := readCounted(r)
		if err != nil {
			return nil, invalidSnapshot(err)
		}
		tags[string(key)] = string(val)
	}
	if r.i != len(r.b) {
		return nil, errors.New("invalid snapshot encoding")
	}
	compressed, err := gzipCompress(concat(mem))
	if err != nil {
		return nil, err
	}
	snap := newMemSnapshot(version, mem, nil, 0, compressed)
	snap.tags = tags
	return snap, nil
}

func readCounted(r *sliceReader) ([]byte, error) {
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return nil, err
	}
	if uint64(n) > uint64(len(r.b)-r.i) {
		return nil, io.ErrUnexpectedEOF
	}
	return r.take(int(n))
}

func invalidSnapshot(err error) error {
	return fmt.Errorf("invalid snapshot encoding: %w", err)
}

// sliceReader is a bytes.Reader that also exposes the remaining length.
type sliceReader struct {
	b []byte
	i int
}

func (r *sliceReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}

func (r *sliceReader) take(n int) ([]byte, error) {
	if n < 0 || r.i+n > len(r.b) {
		return nil, io.ErrUnexpectedEOF
	}
	out := r.b[r.i : r.i+n]
	r.i += n
	return out, nil
}
