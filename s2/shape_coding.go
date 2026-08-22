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
	"fmt"
	"io"
)

const maxEncodedShapes = 10000000

func encodeShape(shape Shape, e *encoder) error {
	tag := shape.typeTag()
	if tag == typeTagNone {
		return fmt.Errorf("cannot encode shape with no type tag")
	}
	e.writeUvarint(uint64(tag))
	switch s := shape.(type) {
	case *Polygon:
		s.encode(e)
	case *Polyline:
		s.encode(e)
	case *PointVector:
		encodeS2PointVectorFast([]Point(*s), e)
	case *LaxPolyline:
		encodeS2PointVectorFast(s.vertices, e)
	case *LaxPolygon:
		encodeLaxPolygon(s, e)
	default:
		return fmt.Errorf("unsupported shape type for encoding")
	}
	return e.err
}

func encodeLaxPolygon(p *LaxPolygon, e *encoder) {
	e.writeUint8(1)
	e.writeUvarint(uint64(p.numLoops))
	encodeS2PointVectorFast(p.vertices, e)
	if p.numLoops > 1 {
		starts := make([]uint64, p.numLoops+1)
		for i := range starts {
			starts[i] = uint64(p.cumulativeVertices[i])
		}
		encodeUint64Vector(starts, e)
	}
}

func decodeShape(tag typeTag, d *decoder) (Shape, error) {
	switch tag {
	case typeTagPolygon:
		p := &Polygon{}
		version := d.readUint8()
		if d.err != nil {
			return nil, d.err
		}
		switch int8(version) {
		case encodingVersion:
			p.decode(d)
		case encodingCompressedVersion:
			p.decodeCompressed(d)
		default:
			return nil, fmt.Errorf("unsupported polygon encoding version %d", version)
		}
		if d.err != nil {
			return nil, d.err
		}
		return p, nil
	case typeTagPolyline:
		p := &Polyline{}
		p.decode(*d)
		if d.err != nil {
			return nil, d.err
		}
		return p, nil
	case typeTagPointVector:
		pts, err := decodeS2PointVector(d)
		if err != nil {
			return nil, err
		}
		pv := PointVector(pts)
		return &pv, nil
	case typeTagLaxPolyline:
		pts, err := decodeS2PointVector(d)
		if err != nil {
			return nil, err
		}
		return LaxPolylineFromPoints(pts), nil
	case typeTagLaxPolygon:
		return decodeLaxPolygon(d)
	default:
		return nil, fmt.Errorf("unsupported shape type tag %d", tag)
	}
}

func decodeLaxPolygon(d *decoder) (*LaxPolygon, error) {
	version := d.readUint8()
	if d.err != nil {
		return nil, d.err
	}
	if version != 1 {
		return nil, fmt.Errorf("unsupported LaxPolygon encoding version %d", version)
	}
	numLoops := d.readUvarint()
	if d.err != nil {
		return nil, d.err
	}
	if numLoops > maxEncodedLoops {
		return nil, fmt.Errorf("too many loops (%d; max is %d)", numLoops, maxEncodedLoops)
	}
	vertices, err := decodeS2PointVector(d)
	if err != nil {
		return nil, err
	}
	p := &LaxPolygon{numLoops: int(numLoops), vertices: vertices, numVerts: len(vertices)}
	if numLoops > 1 {
		starts, err := decodeUint64Vector(d)
		if err != nil {
			return nil, err
		}
		if len(starts) != int(numLoops)+1 {
			return nil, fmt.Errorf("loop start count mismatch")
		}
		p.cumulativeVertices = make([]int, len(starts))
		for i, s := range starts {
			if s > uint64(len(vertices)) {
				return nil, fmt.Errorf("invalid loop start offset")
			}
			p.cumulativeVertices[i] = int(s)
		}
	}
	return p, nil
}

func encodeTaggedShapes(index *ShapeIndex, w io.Writer) error {
	sve := newStringVectorEncoder()
	for id := int32(0); id < index.nextID; id++ {
		sub := sve.addViaEncoder()
		if shape := index.shapes[id]; shape != nil {
			if err := encodeShape(shape, sub); err != nil {
				return err
			}
		}
		sve.finishSubEncoder(sub)
	}
	e := &encoder{w: w}
	sve.encode(e)
	return e.err
}

func decodeTaggedShapes(r io.Reader) (map[int32]Shape, int32, error) {
	d := &decoder{r: asByteReader(r)}
	strings, err := decodeStringVector(d)
	if err != nil {
		return nil, 0, err
	}
	if len(strings) > maxEncodedShapes {
		return nil, 0, fmt.Errorf("too many shapes (%d; max is %d)", len(strings), maxEncodedShapes)
	}
	shapes := make(map[int32]Shape, len(strings))
	for id, data := range strings {
		if len(data) == 0 {
			continue
		}
		sub := &decoder{r: asByteReader(bytes.NewReader(data))}
		tag := typeTag(sub.readUvarint())
		if sub.err != nil {
			return nil, 0, sub.err
		}
		shape, err := decodeShape(tag, sub)
		if err != nil {
			return nil, 0, err
		}
		shapes[int32(id)] = shape
	}
	return shapes, int32(len(strings)), nil
}
