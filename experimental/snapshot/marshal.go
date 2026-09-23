package snapshot

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
)

var snapshotMagic = []byte{'W', 'S', 'N', '1'}

// MarshalSnapshot encodes fully reconstructed memory, version, and tags.
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

	var buf bytes.Buffer
	buf.Write(snapshotMagic)
	var num [8]byte
	binary.BigEndian.PutUint64(num[:], snap.Version())
	buf.Write(num[:])
	if err := writeU32(&buf, uint32(len(data))); err != nil {
		return nil, err
	}
	for i, m := range data {
		if uint64(len(m)) > uint64(^uint32(0)) {
			return nil, fmt.Errorf("module %d memory exceeds uint32 length", i)
		}
		if err := writeU32(&buf, uint32(len(m))); err != nil {
			return nil, err
		}
		buf.Write(m)
	}
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
// The result is a full snapshot: incremental compression is not preserved.
func UnmarshalSnapshot(data []byte) (Snapshot, error) {
	if len(data) < len(snapshotMagic)+8+4 {
		return nil, errors.New("snapshot truncated")
	}
	if !bytes.Equal(data[:len(snapshotMagic)], snapshotMagic) {
		return nil, errors.New("snapshot magic mismatch")
	}
	r := bytes.NewReader(data[len(snapshotMagic):])
	var verBuf [8]byte
	if _, err := r.Read(verBuf[:]); err != nil {
		return nil, err
	}
	version := binary.BigEndian.Uint64(verBuf[:])
	nmod, err := readU32(r)
	if err != nil {
		return nil, err
	}
	mems := make([][]byte, nmod)
	for i := uint32(0); i < nmod; i++ {
		n, err := readU32(r)
		if err != nil {
			return nil, err
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, fmt.Errorf("module %d: %w", i, err)
		}
		mems[i] = buf
	}
	ntag, err := readU32(r)
	if err != nil {
		return nil, err
	}
	tags := make(map[string]string, ntag)
	for i := uint32(0); i < ntag; i++ {
		k, err := readString(r)
		if err != nil {
			return nil, err
		}
		v, err := readString(r)
		if err != nil {
			return nil, err
		}
		tags[k] = v
	}
	if r.Len() != 0 {
		return nil, errors.New("snapshot trailing bytes")
	}
	snap := newFullSnapshot(version, mems, nil)
	snap.tags = tags
	return snap, nil
}

func writeU32(buf *bytes.Buffer, v uint32) error {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	_, err := buf.Write(b[:])
	return err
}

func writeString(buf *bytes.Buffer, s string) error {
	if uint64(len(s)) > uint64(^uint32(0)) {
		return errors.New("string exceeds uint32 length")
	}
	if err := writeU32(buf, uint32(len(s))); err != nil {
		return err
	}
	_, err := buf.WriteString(s)
	return err
}

func readU32(r *bytes.Reader) (uint32, error) {
	var b [4]byte
	if _, err := r.Read(b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b[:]), nil
}

func readString(r *bytes.Reader) (string, error) {
	n, err := readU32(r)
	if err != nil {
		return "", err
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}
