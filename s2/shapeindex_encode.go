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
	"slices"
	"sync/atomic"
)

// shapeIndexEncodingVersion is the version of the ShapeIndex byte stream.
const shapeIndexEncodingVersion = uint8(1)

// Shape tags written by ShapeIndex. Loop and LaxLoop report typeTagNone
// because they are not tagged C++ shapes, but both implement Shape and must
// round-trip, so the index stream uses private tags for them.
const (
	shapeIndexLoopTag    typeTag = 6
	shapeIndexLaxLoopTag typeTag = 7
)

// Caps refuse to allocate from a hostile count. Valid indexes stay far below
// these limits; the vectors still are not pre-sized past the cap.
const (
	maxEncodedShapes        = 10000000
	maxEncodedIndexCells    = 50000000
	maxEncodedClippedShapes = 10000000
	maxEncodedClippedEdges  = maxEncodedVertices
)

// Encode writes the index, including shape identities and the spatial cell
// structure, so the result can be queried without Build. Shapes that have
// not yet been indexed are built first. An empty index still writes a
// non-empty header.
func (s *ShapeIndex) Encode(w io.Writer) error {
	s.Build()

	ids := make([]int32, 0, len(s.shapes))
	for id, shape := range s.shapes {
		if shape == nil {
			return fmt.Errorf("shape %d is nil", id)
		}
		if _, err := shapeIndexTag(shape); err != nil {
			return err
		}
		if id < 0 || id >= s.nextID {
			return fmt.Errorf("shape id %d is outside [0, %d)", id, s.nextID)
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)

	if len(s.cells) > maxEncodedIndexCells {
		return fmt.Errorf("too many cells (%d; max is %d)", len(s.cells), maxEncodedIndexCells)
	}

	e := &encoder{w: w}
	e.writeUint8(shapeIndexEncodingVersion)
	e.writeInt32(int32(s.maxEdgesPerCell))
	e.writeInt32(s.nextID)
	e.writeInt32(s.pendingAdditionsPos)
	e.writeUint32(uint32(len(ids)))
	for _, id := range ids {
		e.writeInt32(id)
		encodeShapeIndexShape(e, s.shapes[id])
		if e.err != nil {
			return e.err
		}
	}

	e.writeUint32(uint32(len(s.cells)))
	var prev CellID
	for i, id := range s.cells {
		cell := s.cellMap[id]
		if cell == nil {
			return fmt.Errorf("missing cell %v", id)
		}
		if !id.IsValid() || (i > 0 && id <= prev) {
			return fmt.Errorf("cell ids must be valid and strictly increasing")
		}
		prev = id
		e.writeUint64(uint64(id))
		e.writeUint32(uint32(len(cell.shapes)))
		var prevShape int32 = -1
		for _, clipped := range cell.shapes {
			if clipped == nil {
				return fmt.Errorf("nil clipped shape in cell %v", id)
			}
			if clipped.shapeID <= prevShape {
				return fmt.Errorf("clipped shape ids must be strictly increasing")
			}
			prevShape = clipped.shapeID
			shape := s.shapes[clipped.shapeID]
			if shape == nil {
				return fmt.Errorf("cell %v references missing shape %d", id, clipped.shapeID)
			}
			e.writeInt32(clipped.shapeID)
			if clipped.containsCenter {
				e.writeUint8(1)
			} else {
				e.writeUint8(0)
			}
			e.writeUint32(uint32(len(clipped.edges)))
			for _, edge := range clipped.edges {
				if edge < 0 || edge >= shape.NumEdges() {
					return fmt.Errorf("cell %v edge %d out of range for shape %d", id, edge, clipped.shapeID)
				}
				e.writeInt32(int32(edge))
			}
		}
	}
	return e.err
}

// Decode restores an index written by Encode. Malformed input, including
// truncated streams and counts that would allocate too much memory, returns
// an error. On success the index is fresh: its cells are the ones that were
// encoded, and queries do not need Build.
func (s *ShapeIndex) Decode(r io.Reader) error {
	d := &decoder{r: asByteReader(r)}
	idx, err := decodeShapeIndex(d)
	if err != nil {
		return err
	}
	s.shapes = idx.shapes
	s.maxEdgesPerCell = idx.maxEdgesPerCell
	s.nextID = idx.nextID
	s.cellMap = idx.cellMap
	s.cells = idx.cells
	s.pendingAdditionsPos = idx.pendingAdditionsPos
	s.pendingRemovals = nil
	atomic.StoreInt32(&s.status, fresh)
	return nil
}

func decodeShapeIndex(d *decoder) (*ShapeIndex, error) {
	version := d.readUint8()
	if d.err != nil {
		return nil, d.err
	}
	if version != shapeIndexEncodingVersion {
		return nil, fmt.Errorf("unsupported ShapeIndex version %d", version)
	}

	maxEdges := d.readInt32()
	nextID := d.readInt32()
	pending := d.readInt32()
	numShapes := d.readUint32()
	if d.err != nil {
		return nil, d.err
	}
	if maxEdges < 0 {
		return nil, fmt.Errorf("negative maxEdgesPerCell %d", maxEdges)
	}
	if nextID < 0 {
		return nil, fmt.Errorf("negative next shape id %d", nextID)
	}
	if pending < 0 {
		return nil, fmt.Errorf("negative pendingAdditionsPos %d", pending)
	}
	if numShapes > maxEncodedShapes {
		return nil, fmt.Errorf("too many shapes (%d; max is %d)", numShapes, maxEncodedShapes)
	}

	idx := &ShapeIndex{
		maxEdgesPerCell:     int(maxEdges),
		nextID:              nextID,
		pendingAdditionsPos: pending,
		shapes:              make(map[int32]Shape, numShapes),
		cellMap:             make(map[CellID]*ShapeIndexCell),
		status:              fresh,
	}

	var prevID int32 = -1
	for range numShapes {
		id := d.readInt32()
		if d.err != nil {
			return nil, d.err
		}
		if id < 0 || id >= nextID || id <= prevID {
			return nil, fmt.Errorf("shape id %d is invalid", id)
		}
		prevID = id
		shape, err := decodeShapeIndexShape(d)
		if err != nil {
			return nil, err
		}
		idx.shapes[id] = shape
	}

	numCells := d.readUint32()
	if d.err != nil {
		return nil, d.err
	}
	if numCells > maxEncodedIndexCells {
		return nil, fmt.Errorf("too many cells (%d; max is %d)", numCells, maxEncodedIndexCells)
	}
	if numCells > 0 {
		idx.cells = make([]CellID, 0, numCells)
	}

	var prevCell CellID
	for i := uint32(0); i < numCells; i++ {
		cellID := CellID(d.readUint64())
		numClipped := d.readUint32()
		if d.err != nil {
			return nil, d.err
		}
		if !cellID.IsValid() || (i > 0 && cellID <= prevCell) {
			return nil, fmt.Errorf("cell ids must be valid and strictly increasing")
		}
		prevCell = cellID
		if numClipped > maxEncodedClippedShapes {
			return nil, fmt.Errorf("too many clipped shapes (%d; max is %d)", numClipped, maxEncodedClippedShapes)
		}

		cell := NewShapeIndexCell(0)
		var prevShape int32 = -1
		for range numClipped {
			shapeID := d.readInt32()
			flag := d.readUint8()
			numEdges := d.readUint32()
			if d.err != nil {
				return nil, d.err
			}
			if flag > 1 {
				return nil, fmt.Errorf("invalid containsCenter %d", flag)
			}
			if shapeID < 0 || shapeID >= nextID || shapeID <= prevShape {
				return nil, fmt.Errorf("clipped shape id %d is invalid", shapeID)
			}
			prevShape = shapeID
			shape := idx.shapes[shapeID]
			if shape == nil {
				return nil, fmt.Errorf("cell references missing shape %d", shapeID)
			}
			if numEdges > maxEncodedClippedEdges {
				return nil, fmt.Errorf("too many clipped edges (%d; max is %d)", numEdges, maxEncodedClippedEdges)
			}
			clipped := newClippedShape(shapeID, int(numEdges))
			clipped.containsCenter = flag == 1
			limit := shape.NumEdges()
			for e := range clipped.edges {
				edgeID := int(d.readInt32())
				if d.err != nil {
					return nil, d.err
				}
				if edgeID < 0 || edgeID >= limit {
					return nil, fmt.Errorf("clipped edge %d out of range for shape %d", edgeID, shapeID)
				}
				clipped.edges[e] = edgeID
			}
			cell.shapes = append(cell.shapes, clipped)
		}
		idx.cellMap[cellID] = cell
		idx.cells = append(idx.cells, cellID)
	}
	if d.err != nil {
		return nil, d.err
	}
	return idx, nil
}

func shapeIndexTag(shape Shape) (typeTag, error) {
	switch shape.(type) {
	case *Polygon:
		return typeTagPolygon, nil
	case *Polyline:
		return typeTagPolyline, nil
	case *PointVector:
		return typeTagPointVector, nil
	case *LaxPolyline:
		return typeTagLaxPolyline, nil
	case *LaxPolygon:
		return typeTagLaxPolygon, nil
	case *Loop:
		return shapeIndexLoopTag, nil
	case *LaxLoop:
		return shapeIndexLaxLoopTag, nil
	default:
		return typeTagNone, fmt.Errorf("shape type %T cannot be encoded", shape)
	}
}

func encodeShapeIndexShape(e *encoder, shape Shape) {
	tag, err := shapeIndexTag(shape)
	if err != nil {
		e.err = err
		return
	}
	e.writeUint32(uint32(tag))
	switch sh := shape.(type) {
	case *Polygon:
		sh.encodeLossless(e)
	case *Polyline:
		sh.encode(e)
	case *PointVector:
		encodePointList(e, []Point(*sh))
	case *LaxPolyline:
		encodePointList(e, sh.vertices)
	case *LaxPolygon:
		encodeLaxPolygon(e, sh)
	case *Loop:
		sh.encode(e)
	case *LaxLoop:
		encodePointList(e, sh.vertices)
	default:
		e.err = fmt.Errorf("shape type %T cannot be encoded", shape)
	}
}

func decodeShapeIndexShape(d *decoder) (Shape, error) {
	tag := typeTag(d.readUint32())
	if d.err != nil {
		return nil, d.err
	}
	switch tag {
	case typeTagPolygon:
		p := &Polygon{}
		version := int8(d.readUint8())
		if d.err != nil {
			return nil, d.err
		}
		if version != encodingVersion {
			return nil, fmt.Errorf("unsupported polygon version %d", version)
		}
		p.decode(d)
		if d.err != nil {
			return nil, d.err
		}
		return p, nil
	case typeTagPolyline:
		p := &Polyline{}
		p.decode(d)
		if d.err != nil {
			return nil, d.err
		}
		return p, nil
	case typeTagPointVector:
		pts := decodePointList(d)
		if d.err != nil {
			return nil, d.err
		}
		pv := PointVector(pts)
		return &pv, nil
	case typeTagLaxPolyline:
		pts := decodePointList(d)
		if d.err != nil {
			return nil, d.err
		}
		return LaxPolylineFromPoints(pts), nil
	case typeTagLaxPolygon:
		p := decodeLaxPolygon(d)
		if d.err != nil {
			return nil, d.err
		}
		return p, nil
	case shapeIndexLoopTag:
		l := &Loop{}
		l.decode(d)
		if d.err != nil {
			return nil, d.err
		}
		return l, nil
	case shapeIndexLaxLoopTag:
		pts := decodePointList(d)
		if d.err != nil {
			return nil, d.err
		}
		return LaxLoopFromPoints(pts), nil
	default:
		return nil, fmt.Errorf("unsupported shape type tag %d", tag)
	}
}

func encodePointList(e *encoder, pts []Point) {
	if len(pts) > maxEncodedVertices {
		e.err = fmt.Errorf("too many vertices (%d; max is %d)", len(pts), maxEncodedVertices)
		return
	}
	e.writeUint32(uint32(len(pts)))
	for _, p := range pts {
		e.writeFloat64(p.X)
		e.writeFloat64(p.Y)
		e.writeFloat64(p.Z)
	}
}

func decodePointList(d *decoder) []Point {
	n := d.readUint32()
	if d.err != nil {
		return nil
	}
	if n > maxEncodedVertices {
		d.err = fmt.Errorf("too many vertices (%d; max is %d)", n, maxEncodedVertices)
		return nil
	}
	pts := make([]Point, n)
	for i := range pts {
		pts[i].X = d.readFloat64()
		pts[i].Y = d.readFloat64()
		pts[i].Z = d.readFloat64()
	}
	return pts
}

func encodeLaxPolygon(e *encoder, p *LaxPolygon) {
	if p.numLoops > maxEncodedLoops {
		e.err = fmt.Errorf("too many loops (%d; max is %d)", p.numLoops, maxEncodedLoops)
		return
	}
	e.writeUint32(uint32(p.numLoops))
	for i := range p.numLoops {
		n := p.numLoopVertices(i)
		if n > maxEncodedVertices {
			e.err = fmt.Errorf("too many vertices (%d; max is %d)", n, maxEncodedVertices)
			return
		}
		e.writeUint32(uint32(n))
		for j := range n {
			v := p.loopVertex(i, j)
			e.writeFloat64(v.X)
			e.writeFloat64(v.Y)
			e.writeFloat64(v.Z)
		}
	}
}

func decodeLaxPolygon(d *decoder) *LaxPolygon {
	nloops := d.readUint32()
	if d.err != nil {
		return nil
	}
	if nloops > maxEncodedLoops {
		d.err = fmt.Errorf("too many loops (%d; max is %d)", nloops, maxEncodedLoops)
		return nil
	}
	loops := make([][]Point, nloops)
	for i := range loops {
		loops[i] = decodePointList(d)
		if d.err != nil {
			return nil
		}
	}
	return LaxPolygonFromPoints(loops)
}

func (d *decoder) readInt32() int32 {
	return int32(d.readUint32())
}
