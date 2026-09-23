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
	"fmt"
	"io"
	"math"
	"sort"
	"sync/atomic"
)

const shapeIndexEncodingVersion = uint8(1)

// shapeKind identifies the concrete Shape type of an encoded shape within a
// ShapeIndex encoding. These are distinct from typeTag because Loop and
// LaxLoop have no typeTag of their own.
type shapeKind uint8

const (
	shapeKindPointVector shapeKind = iota + 1
	shapeKindPolyline
	shapeKindLaxPolyline
	shapeKindLaxLoop
	shapeKindLaxPolygon
	shapeKindLoop
	shapeKindPolygon
)

// maxDecodeChunk bounds the up-front capacity of slices sized from encoded
// counts. Slices grow as elements are actually read, so a bogus count fails
// on truncated input instead of triggering a huge allocation.
const maxDecodeChunk = 1024

// Encode encodes the ShapeIndex, including all of its shapes and the full
// cell structure. Any pending updates are applied first, so the decoded index
// is immediately usable without calling Build. Shape IDs are preserved.
func (s *ShapeIndex) Encode(w io.Writer) error {
	s.maybeApplyUpdates()
	s.mu.RLock()
	defer s.mu.RUnlock()

	e := &encoder{w: w}
	s.encode(e)
	return e.err
}

func (s *ShapeIndex) encode(e *encoder) {
	e.writeUint8(shapeIndexEncodingVersion)
	e.writeUvarint(uint64(s.maxEdgesPerCell))
	e.writeUvarint(uint64(s.nextID))

	ids := make([]int32, 0, len(s.shapes))
	for id := range s.shapes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	e.writeUvarint(uint64(len(ids)))
	for _, id := range ids {
		e.writeUvarint(uint64(id))
		encodeIndexShape(e, s.shapes[id])
		if e.err != nil {
			return
		}
	}

	e.writeUvarint(uint64(len(s.cells)))
	for _, id := range s.cells {
		e.writeUint64(uint64(id))
		cell, ok := s.cellMap[id]
		e.writeBool(ok)
		if !ok {
			continue
		}
		e.writeUvarint(uint64(len(cell.shapes)))
		for _, c := range cell.shapes {
			e.writeUvarint(uint64(c.shapeID))
			e.writeBool(c.containsCenter)
			e.writeUvarint(uint64(len(c.edges)))
			for _, edge := range c.edges {
				e.writeUvarint(uint64(edge))
			}
		}
	}
}

// Decode decodes a ShapeIndex previously written by Encode, replacing the
// current contents of s. On error, s is left unchanged.
func (s *ShapeIndex) Decode(r io.Reader) error {
	d := &decoder{r: asByteReader(r)}
	dec := decodeShapeIndex(d)
	if d.err != nil {
		if d.err == io.EOF {
			return io.ErrUnexpectedEOF
		}
		return d.err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.shapes = dec.shapes
	s.maxEdgesPerCell = dec.maxEdgesPerCell
	s.nextID = dec.nextID
	s.cellMap = dec.cellMap
	s.cells = dec.cells
	s.pendingAdditionsPos = int32(len(dec.shapes))
	s.pendingRemovals = nil
	atomic.StoreInt32(&s.status, fresh)
	return nil
}

func decodeShapeIndex(d *decoder) *ShapeIndex {
	version := d.readUint8()
	if d.err != nil {
		return nil
	}
	if version != shapeIndexEncodingVersion {
		d.err = fmt.Errorf("s2: unsupported ShapeIndex encoding version %d", version)
		return nil
	}

	s := &ShapeIndex{
		shapes:  make(map[int32]Shape),
		cellMap: make(map[CellID]*ShapeIndexCell),
	}

	maxEdges := d.readUvarint()
	if d.err == nil && maxEdges > math.MaxInt32 {
		d.err = fmt.Errorf("s2: invalid maxEdgesPerCell %d", maxEdges)
	}
	s.maxEdgesPerCell = int(maxEdges)

	nextID := d.readUvarint()
	if d.err == nil && nextID > math.MaxInt32 {
		d.err = fmt.Errorf("s2: invalid next shape ID %d", nextID)
	}
	s.nextID = int32(nextID)

	numShapes := readCount(d, uint64(s.nextID), "shapes")
	lastID := int64(-1)
	for i := uint64(0); i < numShapes && d.err == nil; i++ {
		id := d.readUvarint()
		if d.err != nil {
			break
		}
		if int64(id) <= lastID || id >= uint64(s.nextID) {
			d.err = fmt.Errorf("s2: invalid or out of order shape ID %d", id)
			break
		}
		lastID = int64(id)
		shape := decodeIndexShape(d)
		if d.err == nil {
			s.shapes[int32(id)] = shape
		}
	}

	numCells := readCount(d, math.MaxInt32, "cells")
	if numCells > 0 {
		s.cells = make([]CellID, 0, min(numCells, maxDecodeChunk))
	}
	for i := uint64(0); i < numCells && d.err == nil; i++ {
		id := CellID(d.readUint64())
		present := readStrictBool(d)
		if d.err != nil {
			break
		}
		if !id.IsValid() {
			d.err = fmt.Errorf("s2: invalid cell ID %v", id)
			break
		}
		s.cells = append(s.cells, id)
		if !present {
			continue
		}
		cell := decodeIndexCell(d, s)
		if d.err == nil {
			s.cellMap[id] = cell
		}
	}

	if d.err != nil {
		return nil
	}
	return s
}

func decodeIndexCell(d *decoder, s *ShapeIndex) *ShapeIndexCell {
	numClipped := readCount(d, uint64(len(s.shapes)), "clipped shapes")
	cell := &ShapeIndexCell{shapes: make([]*clippedShape, 0, min(numClipped, maxDecodeChunk))}
	for j := uint64(0); j < numClipped && d.err == nil; j++ {
		shapeID := d.readUvarint()
		containsCenter := readStrictBool(d)
		if d.err != nil {
			break
		}
		var shape Shape
		if shapeID <= math.MaxInt32 {
			shape = s.shapes[int32(shapeID)]
		}
		if shape == nil {
			d.err = fmt.Errorf("s2: cell references unknown shape ID %d", shapeID)
			break
		}
		numEdges := uint64(shape.NumEdges())
		n := readCount(d, numEdges, "clipped edges")
		c := &clippedShape{
			shapeID:        int32(shapeID),
			containsCenter: containsCenter,
			edges:          make([]int, 0, min(n, maxDecodeChunk)),
		}
		for k := uint64(0); k < n && d.err == nil; k++ {
			edge := d.readUvarint()
			if d.err == nil && edge >= numEdges {
				d.err = fmt.Errorf("s2: edge ID %d out of range for shape %d", edge, shapeID)
			}
			c.edges = append(c.edges, int(edge))
		}
		cell.shapes = append(cell.shapes, c)
	}
	return cell
}

func encodeIndexShape(e *encoder, shape Shape) {
	switch sh := shape.(type) {
	case *PointVector:
		e.writeUint8(uint8(shapeKindPointVector))
		encodeIndexPoints(e, *sh)
	case *Polyline:
		e.writeUint8(uint8(shapeKindPolyline))
		encodeIndexPoints(e, *sh)
	case *LaxPolyline:
		e.writeUint8(uint8(shapeKindLaxPolyline))
		encodeIndexPoints(e, sh.vertices)
	case *LaxLoop:
		e.writeUint8(uint8(shapeKindLaxLoop))
		encodeIndexPoints(e, sh.vertices[:sh.numVertices])
	case *LaxPolygon:
		e.writeUint8(uint8(shapeKindLaxPolygon))
		e.writeUvarint(uint64(sh.numLoops))
		for i := 0; i < sh.numLoops; i++ {
			start := 0
			if sh.numLoops > 1 {
				start = sh.cumulativeVertices[i]
			}
			encodeIndexPoints(e, sh.vertices[start:start+sh.numLoopVertices(i)])
		}
	case *Loop:
		e.writeUint8(uint8(shapeKindLoop))
		encodeIndexLoop(e, sh)
	case *Polygon:
		e.writeUint8(uint8(shapeKindPolygon))
		e.writeUvarint(uint64(len(sh.loops)))
		for _, l := range sh.loops {
			encodeIndexLoop(e, l)
		}
		e.writeBool(sh.hasHoles)
		sh.bound.encode(e)
		sh.subregionBound.encode(e)
	default:
		if e.err == nil {
			e.err = fmt.Errorf("s2: cannot encode shape of type %T in ShapeIndex", shape)
		}
	}
}

func decodeIndexShape(d *decoder) Shape {
	kind := shapeKind(d.readUint8())
	if d.err != nil {
		return nil
	}
	switch kind {
	case shapeKindPointVector:
		pv := PointVector(decodeIndexPoints(d))
		return &pv
	case shapeKindPolyline:
		pl := Polyline(decodeIndexPoints(d))
		return &pl
	case shapeKindLaxPolyline:
		return &LaxPolyline{vertices: decodeIndexPoints(d)}
	case shapeKindLaxLoop:
		vs := decodeIndexPoints(d)
		return &LaxLoop{numVertices: len(vs), vertices: vs}
	case shapeKindLaxPolygon:
		numLoops := readCount(d, maxEncodedLoops, "loops")
		loops := make([][]Point, 0, min(numLoops, maxDecodeChunk))
		total := 0
		for i := uint64(0); i < numLoops && d.err == nil; i++ {
			loop := decodeIndexPoints(d)
			total += len(loop)
			if total > maxEncodedVertices && d.err == nil {
				d.err = fmt.Errorf("s2: too many vertices (%d; max is %d)", total, maxEncodedVertices)
			}
			loops = append(loops, loop)
		}
		if d.err != nil {
			return nil
		}
		return LaxPolygonFromPoints(loops)
	case shapeKindLoop:
		return decodeIndexLoop(d)
	case shapeKindPolygon:
		numLoops := readCount(d, maxEncodedLoops, "loops")
		p := &Polygon{loops: make([]*Loop, 0, min(numLoops, maxDecodeChunk))}
		for i := uint64(0); i < numLoops && d.err == nil; i++ {
			l := decodeIndexLoop(d)
			if d.err != nil {
				break
			}
			p.numVertices += len(l.vertices)
			if p.numVertices > maxEncodedVertices {
				d.err = fmt.Errorf("s2: too many vertices (%d; max is %d)", p.numVertices, maxEncodedVertices)
				break
			}
			p.loops = append(p.loops, l)
		}
		p.hasHoles = readStrictBool(d)
		p.bound.decode(d)
		p.subregionBound.decode(d)
		if d.err != nil {
			return nil
		}
		p.initEdgesAndIndex()
		return p
	default:
		d.err = fmt.Errorf("s2: unknown shape kind %d", kind)
		return nil
	}
}

func encodeIndexLoop(e *encoder, l *Loop) {
	encodeIndexPoints(e, l.vertices)
	e.writeBool(l.originInside)
	e.writeInt32(int32(l.depth))
	l.bound.encode(e)
	l.subregionBound.encode(e)
}

func decodeIndexLoop(d *decoder) *Loop {
	l := &Loop{vertices: decodeIndexPoints(d)}
	l.originInside = readStrictBool(d)
	l.depth = int(int32(d.readUint32()))
	l.bound.decode(d)
	l.subregionBound.decode(d)
	if d.err != nil {
		return nil
	}
	l.index = NewShapeIndex()
	l.index.Add(l)
	return l
}

func encodeIndexPoints(e *encoder, pts []Point) {
	if len(pts) > maxEncodedVertices {
		if e.err == nil {
			e.err = fmt.Errorf("s2: too many vertices (%d; max is %d)", len(pts), maxEncodedVertices)
		}
		return
	}
	e.writeUvarint(uint64(len(pts)))
	for _, p := range pts {
		e.writeFloat64(p.X)
		e.writeFloat64(p.Y)
		e.writeFloat64(p.Z)
	}
}

func decodeIndexPoints(d *decoder) []Point {
	n := readCount(d, maxEncodedVertices, "vertices")
	if d.err != nil {
		return nil
	}
	pts := make([]Point, 0, min(n, maxDecodeChunk))
	for i := uint64(0); i < n && d.err == nil; i++ {
		var p Point
		p.X = d.readFloat64()
		p.Y = d.readFloat64()
		p.Z = d.readFloat64()
		pts = append(pts, p)
	}
	return pts
}

// readCount reads a uvarint element count and fails if it exceeds limit.
func readCount(d *decoder, limit uint64, what string) uint64 {
	n := d.readUvarint()
	if d.err == nil && n > limit {
		d.err = fmt.Errorf("s2: too many %s (%d; max is %d)", what, n, limit)
	}
	if d.err != nil {
		return 0
	}
	return n
}

// readStrictBool reads a bool encoded as a single 0 or 1 byte, rejecting
// any other value as corruption.
func readStrictBool(d *decoder) bool {
	b := d.readUint8()
	if d.err == nil && b > 1 {
		d.err = fmt.Errorf("s2: invalid bool value %d", b)
	}
	return b == 1
}
