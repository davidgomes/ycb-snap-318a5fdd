package snapshot

import (
	"bytes"
	"encoding/binary"
	"errors"
	"maps"
	"slices"
)

// The encoding is the magic, the format version, then unsigned varints and
// length-prefixed byte strings: the snapshot version, the tag count and each
// tag's key and value sorted by key, and the module count and each module's
// memory.
const (
	encodingMagic   = "wzsnap"
	encodingVersion = 1
)

// MarshalSnapshot encodes the memory, version, and tags of snap in a
// platform-independent form that UnmarshalSnapshot decodes. An incremental
// snapshot is encoded with its memory reconstructed, so it no longer depends
// on its baseline.
func MarshalSnapshot(snap Snapshot) ([]byte, error) {
	if isNil(snap) {
		return nil, errorf(CodeNilSnapshot, "snapshot is nil")
	}
	data := internalSnapshot(snap).reconstruct()
	tags := snap.Tags()

	size := len(encodingMagic) + 1 + 3*binary.MaxVarintLen64
	for k, v := range tags {
		size += len(k) + len(v) + 2*binary.MaxVarintLen64
	}
	for _, d := range data {
		size += len(d) + binary.MaxVarintLen64
	}
	buf := make([]byte, 0, size)
	buf = append(buf, encodingMagic...)
	buf = append(buf, encodingVersion)
	buf = binary.AppendUvarint(buf, snap.Version())
	buf = binary.AppendUvarint(buf, uint64(len(tags)))
	for _, k := range slices.Sorted(maps.Keys(tags)) {
		buf = appendBytes(buf, []byte(k))
		buf = appendBytes(buf, []byte(tags[k]))
	}
	buf = binary.AppendUvarint(buf, uint64(len(data)))
	for _, d := range data {
		buf = appendBytes(buf, d)
	}
	return buf, nil
}

func appendBytes(buf, b []byte) []byte {
	return append(binary.AppendUvarint(buf, uint64(len(b))), b...)
}

// UnmarshalSnapshot decodes data encoded by MarshalSnapshot into a full
// snapshot. The result has no captured modules, so RestoreSnapshot matches
// modules to it by position only.
func UnmarshalSnapshot(data []byte) (Snapshot, error) {
	rest, ok := bytes.CutPrefix(data, []byte(encodingMagic))
	if !ok || len(rest) == 0 || rest[0] != encodingVersion {
		return nil, errorf(CodeInvalidEncoding, "invalid snapshot encoding: unrecognized header")
	}
	d := decoder{b: rest[1:]}
	version := d.uvarint()
	tags := map[string]string{}
	for range d.count() {
		k := d.bytes()
		tags[string(k)] = string(d.bytes())
	}
	mems := make([][]byte, d.count())
	for i := range mems {
		mems[i] = cloneBytes(d.bytes())
	}
	if d.err == nil && len(d.b) > 0 {
		d.err = errors.New("trailing data")
	}
	if d.err != nil {
		return nil, errorf(CodeInvalidEncoding, "invalid snapshot encoding: %v", d.err)
	}
	s := newFullSnapshot(version, nil, mems)
	s.tags = tags
	return s, nil
}

// decoder reads the encoding from b, recording the first error in err, after
// which reads return zero values.
type decoder struct {
	b   []byte
	err error
}

func (d *decoder) uvarint() uint64 {
	if d.err != nil {
		return 0
	}
	v, n := binary.Uvarint(d.b)
	if n <= 0 {
		d.err = errors.New("truncated or malformed varint")
		return 0
	}
	d.b = d.b[n:]
	return v
}

// count reads the number of items that follow, each at least one byte long.
func (d *decoder) count() int {
	n := d.uvarint()
	if n > uint64(len(d.b)) {
		d.err = errors.New("count exceeds remaining data")
		return 0
	}
	return int(n)
}

func (d *decoder) bytes() []byte {
	n := d.uvarint()
	if n > uint64(len(d.b)) {
		d.err = errors.New("length exceeds remaining data")
		return nil
	}
	b := d.b[:n]
	d.b = d.b[n:]
	return b
}
