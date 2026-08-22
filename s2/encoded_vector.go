// Copyright 2026 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package s2

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math/bits"
)

const (
	maxEncodedUintVectorSize  = 100000000
	maxEncodedStringVectorSize = 10000000
	maxEncodedCellIDs         = 10000000
)

func encodeUintWithLength(value uint64, length int, e *encoder) {
	for i := 0; i < length; i++ {
		e.writeUint8(uint8(value))
		value >>= 8
	}
}

func decodeUintWithLength(length int, d *decoder) (uint64, bool) {
	if length < 0 || length > 8 {
		d.err = fmt.Errorf("invalid uint length %d", length)
		return 0, false
	}
	if length == 0 {
		return 0, true
	}
	var x uint64
	for i := 0; i < length; i++ {
		b := d.readUint8()
		if d.err != nil {
			return 0, false
		}
		x |= uint64(b) << (8 * i)
	}
	return x, true
}

func encodeUint64Vector(v []uint64, e *encoder) {
	var oneBits uint64 = 1
	for _, x := range v {
		oneBits |= x
	}
	lenBytes := (bits.Len64(oneBits) + 7) >> 3
	if lenBytes < 1 {
		lenBytes = 1
	}
	sizeLen := uint64(len(v)*8) | uint64(lenBytes-1)
	e.writeUvarint(sizeLen)
	for _, x := range v {
		encodeUintWithLength(x, lenBytes, e)
	}
}

func decodeUint64Vector(d *decoder) ([]uint64, error) {
	sizeLen := d.readUvarint()
	if d.err != nil {
		return nil, d.err
	}
	size := sizeLen / 8
	lenBytes := int(sizeLen&7) + 1
	if size > maxEncodedUintVectorSize {
		return nil, fmt.Errorf("too many uint64 values (%d; max is %d)", size, maxEncodedUintVectorSize)
	}
	if lenBytes < 1 || lenBytes > 8 {
		return nil, fmt.Errorf("invalid uint64 vector length %d", lenBytes)
	}
	result := make([]uint64, size)
	for i := range result {
		x, ok := decodeUintWithLength(lenBytes, d)
		if !ok {
			return nil, d.err
		}
		result[i] = x
	}
	return result, nil
}

type stringVectorEncoder struct {
	offsets []uint64
	data    bytes.Buffer
}

func newStringVectorEncoder() *stringVectorEncoder {
	return &stringVectorEncoder{offsets: []uint64{0}}
}

func (s *stringVectorEncoder) add(data []byte) {
	s.data.Write(data)
	s.offsets = append(s.offsets, uint64(s.data.Len()))
}

func (s *stringVectorEncoder) addViaEncoder() *encoder {
	buf := &bytes.Buffer{}
	return &encoder{w: buf, err: nil}
}

func (s *stringVectorEncoder) finishSubEncoder(sub *encoder) {
	if sub.err != nil {
		s.data.Write([]byte{0})
		s.offsets = append(s.offsets, uint64(s.data.Len()))
		return
	}
	if w, ok := sub.w.(*bytes.Buffer); ok {
		s.add(w.Bytes())
	}
}

func (s *stringVectorEncoder) encode(e *encoder) {
	if len(s.offsets) > 1 {
		encodeUint64Vector(s.offsets[1:], e)
	} else {
		encodeUint64Vector(nil, e)
	}
	if s.data.Len() > 0 {
		e.w.Write(s.data.Bytes())
	}
}

func decodeStringVector(d *decoder) ([][]byte, error) {
	offsets, err := decodeUint64Vector(d)
	if err != nil {
		return nil, err
	}
	if len(offsets) > maxEncodedStringVectorSize {
		return nil, fmt.Errorf("too many strings (%d; max is %d)", len(offsets), maxEncodedStringVectorSize)
	}
	var data []byte
	if len(offsets) > 0 {
		last := offsets[len(offsets)-1]
		if last > maxEncodedAllocationBytes {
			return nil, fmt.Errorf("encoded string data too large (%d bytes)", last)
		}
		data = make([]byte, last)
		if _, err := io.ReadFull(d.r, data); err != nil {
			d.err = err
			return nil, err
		}
	}
	result := make([][]byte, len(offsets))
	start := uint64(0)
	for i, limit := range offsets {
		if limit < start || limit > uint64(len(data)) {
			return nil, fmt.Errorf("corrupted string vector offset at index %d", i)
		}
		result[i] = data[start:limit]
		start = limit
	}
	return result, nil
}

const maxEncodedAllocationBytes = 1 << 30

func encodeBaseShift(e *encoder, shift int, base uint64, baseLen int) {
	shiftCode := shift >> 1
	if shift&1 != 0 {
		shiftCode = minInt(31, shiftCode+29)
	}
	e.writeUint8(uint8((shiftCode << 3) | baseLen))
	if shiftCode == 31 {
		e.writeUint8(uint8(shift >> 1))
	}
	if baseLen > 0 {
		baseBytes := base >> (64 - 8*baseLen)
		encodeUintWithLength(baseBytes, baseLen, e)
	}
}

func encodeS2CellIDVector(ids []CellID, e *encoder) {
	var vOr, vAnd, vMin, vMax uint64
	vAnd = ^uint64(0)
	vMin = ^uint64(0)
	for _, id := range ids {
		x := uint64(id)
		vOr |= x
		vAnd &= x
		if x < vMin {
			vMin = x
		}
		if x > vMax {
			vMax = x
		}
	}
	eBase := uint64(0)
	eBaseLen := 0
	eShift := 0
	if vOr > 0 {
		eShift = minInt(56, bits.TrailingZeros64(vOr)&^1)
		if vAnd&(1<<eShift) != 0 {
			eShift++
		}
		eBytes := ^uint64(0)
		for baseLen := 0; baseLen < 8; baseLen++ {
			var tBase uint64
			if baseLen > 0 {
				tBase = vMin & ^(^uint64(0) >> (8 * baseLen))
			}
			tMaxDeltaMsb := 0
			if delta := (vMax - tBase) >> eShift; delta > 0 {
				tMaxDeltaMsb = bits.Len64(delta) - 1
			}
			tBytes := uint64(baseLen) + uint64(len(ids))*uint64((tMaxDeltaMsb>>3)+1)
			if tBytes < eBytes {
				eBase = tBase
				eBaseLen = baseLen
				eBytes = tBytes
			}
		}
		if (eShift&1) != 0 && (bits.Len64((vMax-eBase)>>eShift)-1)&7 != 7 {
			eShift--
		}
	}
	encodeBaseShift(e, eShift, eBase, eBaseLen)
	deltas := make([]uint64, len(ids))
	for i, id := range ids {
		deltas[i] = (uint64(id) - eBase) >> eShift
	}
	encodeUint64Vector(deltas, e)
}

func decodeS2CellIDVector(d *decoder) ([]CellID, error) {
	if d.err != nil {
		return nil, d.err
	}
	codePlusLen := d.readUint8()
	if d.err != nil {
		return nil, d.err
	}
	shiftCode := int(codePlusLen >> 3)
	if shiftCode == 31 {
		shiftCode = 29 + int(d.readUint8())
		if d.err != nil {
			return nil, d.err
		}
		if shiftCode > 56 {
			return nil, fmt.Errorf("invalid cell id vector shift code")
		}
	}
	baseLen := int(codePlusLen & 7)
	base, ok := decodeUintWithLength(baseLen, d)
	if !ok {
		return nil, d.err
	}
	if baseLen > 0 {
		base <<= 64 - 8*baseLen
	}
	var shift int
	if shiftCode >= 29 {
		shift = 2 * (shiftCode - 29) + 1
		base |= 1 << (shift - 1)
	} else {
		shift = 2 * shiftCode
	}
	deltas, err := decodeUint64Vector(d)
	if err != nil {
		return nil, err
	}
	if len(deltas) > maxEncodedCellIDs {
		return nil, fmt.Errorf("too many cell ids (%d; max is %d)", len(deltas), maxEncodedCellIDs)
	}
	result := make([]CellID, len(deltas))
	for i, delta := range deltas {
		result[i] = CellID((delta << shift) + base)
	}
	return result, nil
}

const (
	s2PointVectorEncodingFormatBits = 3
	s2PointVectorUncompressed       = 0
)

func encodeS2PointVectorFast(points []Point, e *encoder) {
	sizeFormat := (uint64(len(points)) << s2PointVectorEncodingFormatBits) | s2PointVectorUncompressed
	e.writeUvarint(sizeFormat)
	for _, p := range points {
		e.writeFloat64(p.X)
		e.writeFloat64(p.Y)
		e.writeFloat64(p.Z)
	}
}

func decodeS2PointVector(d *decoder) ([]Point, error) {
	sizeFormat := d.readUvarint()
	if d.err != nil {
		return nil, d.err
	}
	format := sizeFormat & ((1 << s2PointVectorEncodingFormatBits) - 1)
	size := sizeFormat >> s2PointVectorEncodingFormatBits
	if format != s2PointVectorUncompressed {
		return nil, fmt.Errorf("unsupported S2PointVector encoding format %d", format)
	}
	if size > maxEncodedVertices {
		return nil, fmt.Errorf("too many points (%d; max is %d)", size, maxEncodedVertices)
	}
	points := make([]Point, size)
	for i := range points {
		points[i].X = d.readFloat64()
		points[i].Y = d.readFloat64()
		points[i].Z = d.readFloat64()
		if d.err != nil {
			return nil, d.err
		}
	}
	return points, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func putUvarint(buf []byte, x uint64) int {
	return binary.PutUvarint(buf, x)
}
