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
	"hash/crc32"
	"io"
	"math"
	"reflect"
	"sync/atomic"
)

// shapeIndexEncodingVersion is the version written by ShapeIndex.Encode.
const shapeIndexEncodingVersion uint8 = 1

const (
	// maxEncodedShapes bounds shape ID slots in one index, including gaps
	// left by removed shapes.
	maxEncodedShapes = 10000000

	// maxEncodedIndexCells bounds the number of spatial cells in one index.
	maxEncodedIndexCells = 50000000

	// maxEncodedClippedShapes bounds the clipped shapes stored in one cell.
	maxEncodedClippedShapes = 10000000

	// maxShapeIndexBytes bounds the encoded payload so a hostile length
	// cannot force a multi-gigabyte allocation.
	maxShapeIndexBytes = 1 << 28
)

// typeTagLoop and typeTagLaxLoop identify Shape implementations that report
// typeTagNone. They are still built-in shapes and must round-trip through
// ShapeIndex. Values sit in the user range so they do not collide with the
// C++ tagged-shape assignments.
const (
	typeTagLoop    typeTag = typeTagMinUser
	typeTagLaxLoop typeTag = typeTagMinUser + 1
)

// Encode writes this index to w. Pending updates are applied first, so an
// index that has never had Build called is still written in full. Shape IDs
// are preserved, including gaps from removed shapes, and every spatial cell
// is stored so the decoded index can be queried without Build.
//
// An empty index still produces a non-empty byte stream. Shapes that do not
// support encoding cause Encode to return an error without writing.
func (s *ShapeIndex) Encode(w io.Writer) error {
	if s == nil {
		return fmt.Errorf("nil ShapeIndex")
	}
	if w == nil {
		return fmt.Errorf("nil writer")
	}
	// Build before reading cells. Queries normally do this lazily; encoding
	// has to capture the finished index.
	s.Build()

	if s.nextID < 0 || int(s.nextID) > maxEncodedShapes {
		return fmt.Errorf("shape id count %d exceeds maximum %d", s.nextID, maxEncodedShapes)
	}
	if s.maxEdgesPerCell < math.MinInt32 || s.maxEdgesPerCell > math.MaxInt32 {
		return fmt.Errorf("maxEdgesPerCell %d out of range", s.maxEdgesPerCell)
	}

	var payload bytes.Buffer
	e := &encoder{w: &payload}
	e.writeInt32(s.nextID)
	e.writeInt32(int32(s.maxEdgesPerCell))
	for id := int32(0); id < s.nextID; id++ {
		shape := s.shapes[id]
		if shapeIsNil(shape) {
			e.writeUint8(0)
			continue
		}
		tag, shapeBytes, err := encodeShapeBytes(shape)
		if err != nil {
			return err
		}
		if len(shapeBytes) > maxShapeIndexBytes {
			return fmt.Errorf("encoded shape %d is too large (%d bytes)", id, len(shapeBytes))
		}
		e.writeUint8(1)
		e.writeUvarint(uint64(tag))
		e.writeUvarint(uint64(len(shapeBytes)))
		if e.err != nil {
			return e.err
		}
		if _, err := payload.Write(shapeBytes); err != nil {
			return err
		}
	}
	if e.err != nil {
		return e.err
	}

	if len(s.cells) > maxEncodedIndexCells {
		return fmt.Errorf("too many index cells (%d; max is %d)", len(s.cells), maxEncodedIndexCells)
	}
	e.writeUvarint(uint64(len(s.cells)))
	var prev CellID
	for i, id := range s.cells {
		cell := s.cellMap[id]
		if cell == nil {
			return fmt.Errorf("index cell %v is missing", id)
		}
		if i > 0 && id <= prev {
			return fmt.Errorf("index cells are not strictly increasing at %v", id)
		}
		prev = id
		if err := encodeClippedShapes(e, id, cell); err != nil {
			return err
		}
	}
	if e.err != nil {
		return e.err
	}
	if payload.Len() > maxShapeIndexBytes {
		return fmt.Errorf("encoded ShapeIndex is too large (%d bytes)", payload.Len())
	}

	sum := crc32.ChecksumIEEE(payload.Bytes())
	out := &encoder{w: w}
	out.writeUint8(shapeIndexEncodingVersion)
	out.writeUint32(sum)
	out.writeUvarint(uint64(payload.Len()))
	if out.err != nil {
		return out.err
	}
	_, err := w.Write(payload.Bytes())
	return err
}

// Decode restores an index written by Encode. On success the receiver is
// fresh: shape IDs, shapes, and the spatial cells match the encoded index,
// and queries do not need Build. On failure the receiver is left unchanged.
//
// Truncated input, corrupted bytes, unknown shape types, and counts that
// would allocate more memory than the input can justify return an error.
func (s *ShapeIndex) Decode(r io.Reader) error {
	if s == nil {
		return fmt.Errorf("nil ShapeIndex")
	}
	if r == nil {
		return fmt.Errorf("nil reader")
	}
	decoded, err := decodeShapeIndex(r)
	if err != nil {
		return err
	}
	s.shapes = decoded.shapes
	s.maxEdgesPerCell = decoded.maxEdgesPerCell
	s.nextID = decoded.nextID
	s.cellMap = decoded.cellMap
	s.cells = decoded.cells
	s.pendingAdditionsPos = decoded.pendingAdditionsPos
	s.pendingRemovals = nil
	atomic.StoreInt32(&s.status, fresh)
	return nil
}

func decodeShapeIndex(r io.Reader) (*ShapeIndex, error) {
	d := &decoder{r: asByteReader(r)}
	version := d.readUint8()
	if d.err != nil {
		return nil, d.err
	}
	if version != shapeIndexEncodingVersion {
		return nil, fmt.Errorf("unsupported ShapeIndex encoding version %d", version)
	}
	wantCRC := d.readUint32()
	payloadLen, ok := readCount(d, maxShapeIndexBytes, "shape index bytes")
	if !ok {
		return nil, d.err
	}
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(d.r, payload); err != nil {
		return nil, err
	}
	if crc32.ChecksumIEEE(payload) != wantCRC {
		return nil, fmt.Errorf("corrupted ShapeIndex encoding")
	}

	pd := &decoder{r: bytes.NewReader(payload)}
	nextID := pd.readInt32()
	maxEdges := pd.readInt32()
	if pd.err != nil {
		return nil, pd.err
	}
	if nextID < 0 || int(nextID) > maxEncodedShapes {
		return nil, fmt.Errorf("shape id count %d exceeds maximum %d", nextID, maxEncodedShapes)
	}
	// Each shape slot occupies at least one flag byte.
	if rem, ok := decoderRemaining(pd); ok && int(nextID) > rem {
		return nil, fmt.Errorf("shape id count %d exceeds remaining input (%d bytes)", nextID, rem)
	}

	shapes := make(map[int32]Shape)
	for id := int32(0); id < nextID; id++ {
		flag := pd.readUint8()
		if pd.err != nil {
			return nil, pd.err
		}
		switch flag {
		case 0:
			continue
		case 1:
			tag := typeTag(pd.readUvarint())
			if pd.err != nil {
				return nil, pd.err
			}
			n, ok := readCount(pd, maxShapeIndexBytes, "shape bytes")
			if !ok {
				return nil, pd.err
			}
			buf := make([]byte, n)
			if _, err := io.ReadFull(pd.r, buf); err != nil {
				pd.err = err
				return nil, err
			}
			shape, err := decodeShapeBytes(tag, buf)
			if err != nil {
				return nil, err
			}
			shapes[id] = shape
		default:
			return nil, fmt.Errorf("invalid shape presence flag %d", flag)
		}
	}

	numCells, ok := readCount(pd, maxEncodedIndexCells, "index cells")
	if !ok {
		return nil, pd.err
	}
	cells := make([]CellID, 0, min(numCells, 16))
	cellMap := make(map[CellID]*ShapeIndexCell, min(numCells, 16))
	var prev CellID
	for i := 0; i < numCells; i++ {
		raw := pd.readUint64()
		if pd.err != nil {
			return nil, pd.err
		}
		id := CellID(raw)
		if !id.IsValid() {
			return nil, fmt.Errorf("invalid index cell id %v", id)
		}
		if i > 0 && id <= prev {
			return nil, fmt.Errorf("index cells are not strictly increasing at %v", id)
		}
		prev = id
		cell, err := decodeShapeIndexCell(pd, nextID, shapes)
		if err != nil {
			return nil, err
		}
		cells = append(cells, id)
		cellMap[id] = cell
	}
	if pd.err != nil {
		return nil, pd.err
	}
	if rem, ok := decoderRemaining(pd); !ok || rem != 0 {
		return nil, fmt.Errorf("ShapeIndex encoding has trailing bytes")
	}

	return &ShapeIndex{
		shapes:              shapes,
		maxEdgesPerCell:     int(maxEdges),
		nextID:              nextID,
		cellMap:             cellMap,
		cells:               cells,
		pendingAdditionsPos: nextID,
		status:              fresh,
	}, nil
}

func encodeClippedShapes(e *encoder, id CellID, cell *ShapeIndexCell) error {
	if cell == nil {
		return fmt.Errorf("index cell %v is missing", id)
	}
	if len(cell.shapes) > maxEncodedClippedShapes {
		return fmt.Errorf("too many clipped shapes (%d; max is %d)", len(cell.shapes), maxEncodedClippedShapes)
	}
	e.writeUint64(uint64(id))
	e.writeUvarint(uint64(len(cell.shapes)))
	var prev int32 = -1
	for _, clipped := range cell.shapes {
		if clipped == nil {
			return fmt.Errorf("nil clipped shape in cell %v", id)
		}
		if clipped.shapeID <= prev {
			return fmt.Errorf("clipped shapes in cell %v are not strictly increasing", id)
		}
		prev = clipped.shapeID
		e.writeUvarint(uint64(clipped.shapeID))
		if clipped.containsCenter {
			e.writeUint8(1)
		} else {
			e.writeUint8(0)
		}
		if len(clipped.edges) > maxEncodedVertices {
			return fmt.Errorf("too many clipped edges (%d; max is %d)", len(clipped.edges), maxEncodedVertices)
		}
		e.writeUvarint(uint64(len(clipped.edges)))
		for _, edgeID := range clipped.edges {
			if edgeID < 0 {
				return fmt.Errorf("negative edge id %d", edgeID)
			}
			e.writeUvarint(uint64(edgeID))
		}
	}
	return e.err
}

func decodeShapeIndexCell(d *decoder, nextID int32, shapes map[int32]Shape) (*ShapeIndexCell, error) {
	numClipped, ok := readCount(d, maxEncodedClippedShapes, "clipped shapes")
	if !ok {
		return nil, d.err
	}
	if nextID >= 0 && numClipped > int(nextID) {
		return nil, fmt.Errorf("clipped shape count %d exceeds shape id count %d", numClipped, nextID)
	}
	cell := &ShapeIndexCell{}
	if numClipped > 0 {
		cell.shapes = make([]*clippedShape, 0, numClipped)
	}
	var prev int32 = -1
	for range numClipped {
		sid64 := d.readUvarint()
		if d.err != nil {
			return nil, d.err
		}
		if sid64 > uint64(math.MaxInt32) {
			return nil, fmt.Errorf("shape id %d out of range", sid64)
		}
		shapeID := int32(sid64)
		if shapeID <= prev {
			return nil, fmt.Errorf("clipped shapes are not strictly increasing")
		}
		prev = shapeID
		if shapeID < 0 || shapeID >= nextID {
			return nil, fmt.Errorf("clipped shape references missing shape %d", shapeID)
		}
		flag := d.readUint8()
		if d.err != nil {
			return nil, d.err
		}
		var containsCenter bool
		switch flag {
		case 0:
		case 1:
			containsCenter = true
		default:
			return nil, fmt.Errorf("invalid containsCenter flag %d", flag)
		}
		// A removed shape can still be mentioned by a cell when an update
		// has not dropped it yet. Keep the reference so IDs in the cell stay
		// stable, and only range-check edges when the shape is present.
		edgeLimit := maxEncodedVertices
		if shape := shapes[shapeID]; !shapeIsNil(shape) {
			edgeLimit = shape.NumEdges()
			if edgeLimit < 0 {
				return nil, fmt.Errorf("shape %d has negative edge count", shapeID)
			}
		}
		numEdges, ok := readCount(d, uint64(edgeLimit), "clipped edges")
		if !ok {
			return nil, d.err
		}
		clipped := &clippedShape{
			shapeID:        shapeID,
			containsCenter: containsCenter,
		}
		if numEdges > 0 {
			clipped.edges = make([]int, numEdges)
		}
		var prevEdge = -1
		for i := range numEdges {
			edge64 := d.readUvarint()
			if d.err != nil {
				return nil, d.err
			}
			if edge64 > uint64(math.MaxInt) || int(edge64) >= edgeLimit || int(edge64) <= prevEdge {
				return nil, fmt.Errorf("clipped edge id %d is invalid for shape %d", edge64, shapeID)
			}
			prevEdge = int(edge64)
			clipped.edges[i] = prevEdge
		}
		cell.shapes = append(cell.shapes, clipped)
	}
	return cell, nil
}

func encodeShapeBytes(shape Shape) (typeTag, []byte, error) {
	tag, err := shapeEncodingTag(shape)
	if err != nil {
		return 0, nil, err
	}
	var buf bytes.Buffer
	e := &encoder{w: &buf}
	switch s := shape.(type) {
	case *Polygon:
		s.encode(e)
	case *Polyline:
		s.encode(e)
	case *PointVector:
		encodePointList(e, []Point(*s))
	case *LaxPolyline:
		encodePointList(e, s.vertices)
	case *LaxPolygon:
		encodeLaxPolygonShape(e, s)
	case *Loop:
		s.encode(e)
	case *LaxLoop:
		e.writeUint8(uint8(encodingVersion))
		encodePointList(e, s.vertices)
	default:
		return 0, nil, fmt.Errorf("cannot encode shape type %T", shape)
	}
	if e.err != nil {
		return 0, nil, e.err
	}
	return tag, buf.Bytes(), nil
}

func decodeShapeBytes(tag typeTag, data []byte) (Shape, error) {
	d := &decoder{r: bytes.NewReader(data)}
	var shape Shape
	switch tag {
	case typeTagPolygon:
		p, err := decodePolygonShape(d)
		if err != nil {
			return nil, err
		}
		shape = p
	case typeTagPolyline:
		p := &Polyline{}
		p.decode(d)
		if d.err != nil {
			return nil, d.err
		}
		shape = p
	case typeTagPointVector:
		pts, err := decodePointList(d)
		if err != nil {
			return nil, err
		}
		pv := PointVector(pts)
		shape = &pv
	case typeTagLaxPolyline:
		pts, err := decodePointList(d)
		if err != nil {
			return nil, err
		}
		shape = LaxPolylineFromPoints(pts)
	case typeTagLaxPolygon:
		p, err := decodeLaxPolygonShape(d)
		if err != nil {
			return nil, err
		}
		shape = p
	case typeTagLoop:
		l := &Loop{}
		l.decode(d)
		if d.err != nil {
			return nil, d.err
		}
		shape = l
	case typeTagLaxLoop:
		version := d.readUint8()
		if d.err != nil {
			return nil, d.err
		}
		if version != uint8(encodingVersion) {
			return nil, fmt.Errorf("unsupported LaxLoop encoding version %d", version)
		}
		pts, err := decodePointList(d)
		if err != nil {
			return nil, err
		}
		shape = LaxLoopFromPoints(pts)
	default:
		return nil, fmt.Errorf("unsupported shape type tag %d", tag)
	}
	if d.err != nil {
		return nil, d.err
	}
	if rem, ok := decoderRemaining(d); !ok || rem != 0 {
		return nil, fmt.Errorf("shape encoding has trailing bytes")
	}
	return shape, nil
}

func shapeEncodingTag(shape Shape) (typeTag, error) {
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
		return typeTagLoop, nil
	case *LaxLoop:
		return typeTagLaxLoop, nil
	default:
		return typeTagNone, fmt.Errorf("cannot encode shape type %T", shape)
	}
}

func decodePolygonShape(d *decoder) (*Polygon, error) {
	p := &Polygon{}
	version := int8(d.readUint8())
	if d.err != nil {
		return nil, d.err
	}
	switch version {
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
}

func encodeLaxPolygonShape(e *encoder, p *LaxPolygon) {
	e.writeUint8(uint8(encodingVersion))
	if p.numLoops < 0 || p.numLoops > maxEncodedLoops {
		e.err = fmt.Errorf("too many loops (%d; max is %d)", p.numLoops, maxEncodedLoops)
		return
	}
	e.writeUvarint(uint64(p.numLoops))
	for i := 0; i < p.numLoops; i++ {
		n := p.numLoopVertices(i)
		pts := make([]Point, n)
		for j := range n {
			pts[j] = p.loopVertex(i, j)
		}
		encodePointList(e, pts)
	}
}

func decodeLaxPolygonShape(d *decoder) (*LaxPolygon, error) {
	version := d.readUint8()
	if d.err != nil {
		return nil, d.err
	}
	if version != uint8(encodingVersion) {
		return nil, fmt.Errorf("unsupported LaxPolygon encoding version %d", version)
	}
	n, ok := readCount(d, maxEncodedLoops, "lax polygon loops")
	if !ok {
		return nil, d.err
	}
	loops := make([][]Point, n)
	for i := range loops {
		pts, err := decodePointList(d)
		if err != nil {
			return nil, err
		}
		loops[i] = pts
	}
	return LaxPolygonFromPoints(loops), nil
}

func encodePointList(e *encoder, pts []Point) {
	if uint64(len(pts)) > maxEncodedVertices {
		e.err = fmt.Errorf("too many vertices (%d; max is %d)", len(pts), maxEncodedVertices)
		return
	}
	e.writeUvarint(uint64(len(pts)))
	for _, p := range pts {
		e.writeFloat64(p.X)
		e.writeFloat64(p.Y)
		e.writeFloat64(p.Z)
	}
}

func decodePointList(d *decoder) ([]Point, error) {
	n, ok := readCount(d, maxEncodedVertices, "vertices")
	if !ok {
		return nil, d.err
	}
	// Each point is three float64s, 24 bytes. Reject counts the remaining
	// input cannot hold before allocating.
	if rem, ok := decoderRemaining(d); ok && n > 0 && rem/24 < n {
		return nil, fmt.Errorf("vertex count %d exceeds remaining input (%d bytes)", n, rem)
	}
	pts := make([]Point, n)
	for i := range pts {
		pts[i].X = d.readFloat64()
		pts[i].Y = d.readFloat64()
		pts[i].Z = d.readFloat64()
		if d.err != nil {
			return nil, d.err
		}
	}
	return pts, nil
}

// readCount reads a uvarint count and rejects values above max or larger
// than the bytes still buffered in d. The remaining-byte check stops a short
// input from requesting a huge slice.
func readCount(d *decoder, max uint64, what string) (int, bool) {
	n := d.readUvarint()
	if d.err != nil {
		return 0, false
	}
	if n > max {
		d.err = fmt.Errorf("too many %s (%d; max is %d)", what, n, max)
		return 0, false
	}
	if rem, ok := decoderRemaining(d); ok && n > uint64(rem) {
		d.err = fmt.Errorf("%s count %d exceeds remaining input (%d bytes)", what, n, rem)
		return 0, false
	}
	return int(n), true
}

func decoderRemaining(d *decoder) (int, bool) {
	type lener interface{ Len() int }
	if l, ok := d.r.(lener); ok {
		return l.Len(), true
	}
	return 0, false
}

func shapeIsNil(shape Shape) bool {
	if shape == nil {
		return true
	}
	v := reflect.ValueOf(shape)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
