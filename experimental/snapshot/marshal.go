package snapshot

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
)

const snapshotMagic = "WZS1"

// MarshalSnapshot encodes snap's fully reconstructed Data, Version, and Tags
// in a portable little-endian format. Incremental snapshots are encoded as
// their reconstructed full state.
func MarshalSnapshot(snap Snapshot) ([]byte, error) {
	if snap == nil {
		return nil, errors.New("snapshot is nil")
	}
	data := snap.Data()
	tags := snap.Tags()
	if tags == nil {
		tags = map[string]string{}
	}

	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf []byte
	buf = append(buf, snapshotMagic...)
	buf = binary.LittleEndian.AppendUint64(buf, snap.Version())
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(keys)))
	for _, k := range keys {
		buf = appendPrefixedString(buf, k)
		buf = appendPrefixedString(buf, tags[k])
	}
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(data)))
	for _, d := range data {
		if uint64(len(d)) > uint64(^uint32(0)) {
			return nil, errors.New("module memory too large to marshal")
		}
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(d)))
		buf = append(buf, d...)
	}
	return buf, nil
}

// UnmarshalSnapshot decodes data produced by MarshalSnapshot.
// The result is always a full snapshot, not an incremental one.
func UnmarshalSnapshot(data []byte) (Snapshot, error) {
	if len(data) < 4+8+4 {
		return nil, errors.New("invalid snapshot encoding")
	}
	if string(data[:4]) != snapshotMagic {
		return nil, errors.New("invalid snapshot magic")
	}
	r := &byteReader{b: data[4:]}

	version, err := r.u64()
	if err != nil {
		return nil, err
	}
	nTags, err := r.u32()
	if err != nil {
		return nil, err
	}
	tags := make(map[string]string, nTags)
	for i := uint32(0); i < nTags; i++ {
		k, err := r.str()
		if err != nil {
			return nil, err
		}
		v, err := r.str()
		if err != nil {
			return nil, err
		}
		tags[k] = v
	}
	nMods, err := r.u32()
	if err != nil {
		return nil, err
	}
	mems := make([][]byte, nMods)
	for i := uint32(0); i < nMods; i++ {
		n, err := r.u32()
		if err != nil {
			return nil, err
		}
		mems[i], err = r.bytes(int(n))
		if err != nil {
			return nil, err
		}
	}
	if r.remaining() != 0 {
		return nil, fmt.Errorf("invalid snapshot encoding: trailing %d bytes", r.remaining())
	}
	return newUnmarshaledSnapshot(version, mems, tags), nil
}

func appendPrefixedString(buf []byte, s string) []byte {
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(s)))
	return append(buf, s...)
}

type byteReader struct {
	b []byte
}

func (r *byteReader) remaining() int { return len(r.b) }

func (r *byteReader) u32() (uint32, error) {
	if len(r.b) < 4 {
		return 0, io.ErrUnexpectedEOF
	}
	v := binary.LittleEndian.Uint32(r.b)
	r.b = r.b[4:]
	return v, nil
}

func (r *byteReader) u64() (uint64, error) {
	if len(r.b) < 8 {
		return 0, io.ErrUnexpectedEOF
	}
	v := binary.LittleEndian.Uint64(r.b)
	r.b = r.b[8:]
	return v, nil
}

func (r *byteReader) bytes(n int) ([]byte, error) {
	if n < 0 || len(r.b) < n {
		return nil, io.ErrUnexpectedEOF
	}
	out := append([]byte{}, r.b[:n]...)
	r.b = r.b[n:]
	return out, nil
}

func (r *byteReader) str() (string, error) {
	n, err := r.u32()
	if err != nil {
		return "", err
	}
	b, err := r.bytes(int(n))
	if err != nil {
		return "", err
	}
	return string(b), nil
}
