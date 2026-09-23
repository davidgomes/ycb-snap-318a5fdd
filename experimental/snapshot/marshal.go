package snapshot

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
)

const (
	magic         = "WZSN"
	formatVersion = uint16(1)
)

// MarshalSnapshot encodes fully reconstructed memory, version, and tags in a
// portable big-endian layout. The result is not an incremental snapshot.
func MarshalSnapshot(snap Snapshot) ([]byte, error) {
	if snap == nil || isNil(snap) {
		return nil, errors.New("snapshot is nil")
	}
	data := snap.Data()
	tags := snap.Tags()
	if len(data) > math.MaxUint32 {
		return nil, errors.New("too many modules to marshal")
	}
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > math.MaxUint32 {
		return nil, errors.New("too many tags to marshal")
	}

	var buf bytes.Buffer
	if _, err := buf.WriteString(magic); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.BigEndian, formatVersion); err != nil {
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
	if err := binary.Write(&buf, binary.BigEndian, uint32(len(keys))); err != nil {
		return nil, err
	}
	for _, k := range keys {
		if err := writeCountedString(&buf, k); err != nil {
			return nil, err
		}
		if err := writeCountedString(&buf, tags[k]); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// UnmarshalSnapshot decodes a buffer produced by MarshalSnapshot. The result
// is a full snapshot: incremental compression is not preserved.
func UnmarshalSnapshot(data []byte) (Snapshot, error) {
	if len(data) == 0 {
		return nil, errors.New("snapshot data is empty")
	}
	r := bytes.NewReader(data)
	magicBuf := make([]byte, len(magic))
	if _, err := io.ReadFull(r, magicBuf); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}
	if string(magicBuf) != magic {
		return nil, errors.New("unmarshal snapshot: invalid magic")
	}
	var format uint16
	if err := binary.Read(r, binary.BigEndian, &format); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}
	if format != formatVersion {
		return nil, fmt.Errorf("unmarshal snapshot: unsupported format %d", format)
	}
	var version uint64
	if err := binary.Read(r, binary.BigEndian, &version); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}
	var moduleCount uint32
	if err := binary.Read(r, binary.BigEndian, &moduleCount); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}
	if uint64(moduleCount) > uint64(r.Len())/8 {
		return nil, errors.New("unmarshal snapshot: truncated module list")
	}
	mem := make([][]byte, moduleCount)
	for i := uint32(0); i < moduleCount; i++ {
		var n uint64
		if err := binary.Read(r, binary.BigEndian, &n); err != nil {
			return nil, fmt.Errorf("unmarshal snapshot: %w", err)
		}
		if n > uint64(r.Len()) {
			return nil, errors.New("unmarshal snapshot: truncated module")
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, fmt.Errorf("unmarshal snapshot: %w", err)
		}
		mem[i] = buf
	}
	var tagCount uint32
	if err := binary.Read(r, binary.BigEndian, &tagCount); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}
	if uint64(tagCount) > uint64(r.Len())/8 {
		return nil, errors.New("unmarshal snapshot: truncated tags")
	}
	tags := make(map[string]string, tagCount)
	for i := uint32(0); i < tagCount; i++ {
		k, err := readCountedString(r)
		if err != nil {
			return nil, err
		}
		v, err := readCountedString(r)
		if err != nil {
			return nil, err
		}
		tags[k] = v
	}
	if r.Len() != 0 {
		return nil, errors.New("unmarshal snapshot: trailing data")
	}
	compressed, err := gzipBytes(concatData(mem))
	if err != nil {
		return nil, err
	}
	snap := newSnapshot(mem, nil, version, compressed, false, 0)
	snap.tags = tags
	return snap, nil
}

func writeCountedString(buf *bytes.Buffer, s string) error {
	if len(s) > math.MaxUint32 {
		return errors.New("string too long")
	}
	if err := binary.Write(buf, binary.BigEndian, uint32(len(s))); err != nil {
		return err
	}
	_, err := buf.WriteString(s)
	return err
}

func readCountedString(r *bytes.Reader) (string, error) {
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return "", fmt.Errorf("unmarshal snapshot: %w", err)
	}
	if uint64(n) > uint64(r.Len()) {
		return "", errors.New("unmarshal snapshot: truncated string")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", fmt.Errorf("unmarshal snapshot: %w", err)
	}
	return string(buf), nil
}
