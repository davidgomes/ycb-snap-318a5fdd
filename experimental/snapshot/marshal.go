package snapshot

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"sort"
)

// marshalMagic prefixes encoded snapshots. The trailing byte is the format
// version.
var marshalMagic = []byte("wzsnap\x00\x01")

// MarshalSnapshot encodes the fully reconstructed memory, version and tags of
// snap in a portable format that UnmarshalSnapshot decodes.
//
// The format is marshalMagic, then as uvarints the version and module count,
// then each module's length and bytes, then the tag count and each tag's key
// and value length-prefixed and sorted by key, then a little-endian CRC-32
// (IEEE) of everything before it.
func MarshalSnapshot(snap Snapshot) ([]byte, error) {
	if isNil(snap) {
		return nil, newError(CodeNilSnapshot, "snapshot is nil")
	}
	data := dataOf(snap)
	tags := snap.Tags()

	out := append([]byte{}, marshalMagic...)
	out = binary.AppendUvarint(out, snap.Version())
	out = binary.AppendUvarint(out, uint64(len(data)))
	for _, d := range data {
		out = binary.AppendUvarint(out, uint64(len(d)))
		out = append(out, d...)
	}

	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out = binary.AppendUvarint(out, uint64(len(keys)))
	for _, k := range keys {
		out = binary.AppendUvarint(out, uint64(len(k)))
		out = append(out, k...)
		out = binary.AppendUvarint(out, uint64(len(tags[k])))
		out = append(out, tags[k]...)
	}
	return binary.LittleEndian.AppendUint32(out, crc32.ChecksumIEEE(out)), nil
}

// UnmarshalSnapshot decodes data produced by MarshalSnapshot into a full
// snapshot. The result has no captured modules, so RestoreSnapshot matches
// restore targets by position.
func UnmarshalSnapshot(data []byte) (Snapshot, error) {
	if len(data) < len(marshalMagic)+4 || !bytes.Equal(data[:len(marshalMagic)], marshalMagic) {
		return nil, newError(CodeInvalidData, "invalid snapshot data: missing header")
	}
	body, sum := data[:len(data)-4], binary.LittleEndian.Uint32(data[len(data)-4:])
	if crc32.ChecksumIEEE(body) != sum {
		return nil, newError(CodeInvalidData, "invalid snapshot data: checksum mismatch")
	}

	d := decoder{buf: body[len(marshalMagic):]}
	s := &snapshot{version: d.uvarint()}
	moduleCount := d.count()
	s.data = make([][]byte, 0, moduleCount)
	for i := uint64(0); i < moduleCount && d.err == nil; i++ {
		s.data = append(s.data, append([]byte{}, d.bytes()...))
	}
	tagCount := d.count()
	if tagCount > 0 {
		s.tags = make(map[string]string, tagCount)
	}
	for i := uint64(0); i < tagCount && d.err == nil; i++ {
		k := string(d.bytes())
		s.tags[k] = string(d.bytes())
	}
	if d.err == nil && len(d.buf) != 0 {
		d.fail("trailing bytes")
	}
	if d.err != nil {
		return nil, d.err
	}
	return s, nil
}

type decoder struct {
	buf []byte
	err error
}

func (d *decoder) fail(reason string) {
	if d.err == nil {
		d.err = newError(CodeInvalidData, "invalid snapshot data: %s", reason)
	}
	d.buf = nil
}

func (d *decoder) uvarint() uint64 {
	if d.err != nil {
		return 0
	}
	v, n := binary.Uvarint(d.buf)
	if n <= 0 {
		d.fail("malformed integer")
		return 0
	}
	d.buf = d.buf[n:]
	return v
}

// count reads an element count, rejecting counts that cannot fit in the
// remaining bytes since each element takes at least one byte.
func (d *decoder) count() uint64 {
	n := d.uvarint()
	if n > uint64(len(d.buf)) {
		d.fail("count exceeds data")
		return 0
	}
	return n
}

func (d *decoder) bytes() []byte {
	n := d.count()
	if d.err != nil {
		return nil
	}
	b := d.buf[:n]
	d.buf = d.buf[n:]
	return b
}
