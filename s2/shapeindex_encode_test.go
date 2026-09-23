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
	"hash/crc32"
	"math/rand"
	"testing"

	"github.com/golang/geo/s1"
)

func TestShapeIndexEncodeEmpty(t *testing.T) {
	index := NewShapeIndex()
	var buf bytes.Buffer
	if err := index.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("empty index encoded to an empty byte stream")
	}

	decoded := NewShapeIndex()
	if err := decoded.Decode(&buf); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.Len() != 0 || decoded.nextID != 0 {
		t.Fatalf("decoded Len=%d nextID=%d, want 0", decoded.Len(), decoded.nextID)
	}
	if !decoded.IsFresh() {
		t.Fatal("decoded empty index is not fresh")
	}
	if got := decoded.Iterator(); !got.Done() {
		t.Fatal("decoded empty index iterator is not done")
	}
	if decoded.maxEdgesPerCell != index.maxEdgesPerCell {
		t.Fatalf("maxEdgesPerCell = %d, want %d", decoded.maxEdgesPerCell, index.maxEdgesPerCell)
	}
}

func TestShapeIndexEncodeDecodeWithoutBuild(t *testing.T) {
	index := NewShapeIndex()
	polyline := Polyline(parsePoints("0:0, 0:1, 0:2"))
	index.Add(&polyline)
	if index.IsFresh() {
		t.Fatal("index was fresh before Encode")
	}

	var buf bytes.Buffer
	if err := index.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !index.IsFresh() || len(index.cells) == 0 {
		t.Fatal("Encode did not capture a built index")
	}

	decoded := NewShapeIndex()
	if err := decoded.Decode(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !decoded.IsFresh() {
		t.Fatal("decoded index is not fresh")
	}
	if decoded.Shape(0) == nil || decoded.Shape(0).NumEdges() != 2 {
		t.Fatalf("decoded shape = %v", decoded.Shape(0))
	}
	assertIndexEqual(t, index, decoded)
	// Queries and iteration use the restored cells and must not rebuild.
	quadraticValidate(t, decoded)
	if !decoded.IsFresh() {
		t.Fatal("validation rebuilt the decoded index")
	}
}

func TestShapeIndexEncodePreservesShapeIDs(t *testing.T) {
	index := NewShapeIndex()
	keepA := Polyline(parsePoints("0:0, 0:2"))
	drop := Polyline(parsePoints("1:0, 1:2"))
	keepB := Polyline(parsePoints("2:0, 2:2"))
	idA := index.Add(&keepA)
	idDrop := index.Add(&drop)
	idB := index.Add(&keepB)
	// Remove before the index is built so the id stays reserved and the
	// shapes on either side are what the cells refer to.
	index.Remove(&drop)

	var buf bytes.Buffer
	if err := index.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded := NewShapeIndex()
	if err := decoded.Decode(&buf); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.nextID != index.nextID {
		t.Fatalf("nextID = %d, want %d", decoded.nextID, index.nextID)
	}
	if decoded.Shape(idA) == nil || decoded.Shape(idB) == nil {
		t.Fatal("kept shape ids were not preserved")
	}
	if decoded.Shape(idDrop) != nil {
		t.Fatal("removed shape id was restored")
	}
	for _, id := range decoded.cells {
		for _, clipped := range decoded.cellMap[id].shapes {
			if clipped.shapeID == idDrop {
				t.Fatalf("cell %v still references removed shape %d", id, idDrop)
			}
		}
	}
	assertIndexEqual(t, index, decoded)
	quadraticValidate(t, index)
	quadraticValidate(t, decoded)
}

func TestShapeIndexEncodeDecodeBuiltinShapes(t *testing.T) {
	square := makePolygon("0:0, 0:2, 2:2, 2:0", false)
	polyline := Polyline(parsePoints("0:0, 0:1, 1:1"))
	points := PointVector(parsePoints("0:0, 1:1, 2:2"))
	laxPolyline := LaxPolylineFromPoints(parsePoints("3:0, 4:0, 5:1, 6:1"))
	laxPolygon := LaxPolygonFromPoints([][]Point{
		parsePoints("0:0, 0:3, 3:3, 3:0"),
		parsePoints("1:1, 1:2, 2:1"),
		parsePoints("4:4"),
	})
	loop := LoopFromPoints(parsePoints("10:10, 10:12, 12:12, 12:10"))
	laxLoop := LaxLoopFromPoints(parsePoints("5:5, 5:8, 8:5"))

	shapes := []Shape{square, &polyline, &points, laxPolyline, laxPolygon, loop, laxLoop}
	for i, shape := range shapes {
		index := NewShapeIndex()
		index.maxEdgesPerCell = 4
		id := index.Add(shape)
		var buf bytes.Buffer
		if err := index.Encode(&buf); err != nil {
			t.Fatalf("shape %d (%T) Encode: %v", i, shape, err)
		}
		decoded := NewShapeIndex()
		if err := decoded.Decode(&buf); err != nil {
			t.Fatalf("shape %d (%T) Decode: %v", i, shape, err)
		}
		if decoded.maxEdgesPerCell != 4 {
			t.Fatalf("shape %d maxEdgesPerCell = %d", i, decoded.maxEdgesPerCell)
		}
		got := decoded.Shape(id)
		if got == nil {
			t.Fatalf("shape %d missing", i)
		}
		if err := shapesEquivalent(shape, got); err != nil {
			t.Fatalf("shape %d (%T): %v", i, shape, err)
		}
		assertIndexEqual(t, index, decoded)
		quadraticValidate(t, decoded)
	}
}

func TestShapeIndexEncodeZeroEdgeAndMixedChains(t *testing.T) {
	emptyPolyline := Polyline{}
	oneVertex := Polyline(parsePoints("0:0"))
	emptyPoints := PointVector{}
	emptyPolygon := &Polygon{}
	fullPolygon := FullPolygon()
	emptyLaxPolyline := LaxPolylineFromPoints(nil)
	oneVertexLax := LaxPolylineFromPoints(parsePoints("1:1"))
	emptyLaxPolygon := LaxPolygonFromPoints(nil)
	fullLaxPolygon := LaxPolygonFromPoints([][]Point{{}})
	emptyLoop := EmptyLoop()
	fullLoop := FullLoop()
	emptyLaxLoop := LaxLoopFromPoints(nil)
	mixed := LaxPolygonFromPoints([][]Point{
		{},
		parsePoints("0:0"),
		parsePoints("0:0, 1:0"),
		parsePoints("0:0, 0:2, 2:0"),
		parsePoints("3:3, 3:4, 4:4, 4:3, 3.5:3.2"),
	})

	shapes := []Shape{
		&emptyPolyline,
		&oneVertex,
		&emptyPoints,
		emptyPolygon,
		fullPolygon,
		emptyLaxPolyline,
		oneVertexLax,
		emptyLaxPolygon,
		fullLaxPolygon,
		emptyLoop,
		fullLoop,
		emptyLaxLoop,
		mixed,
	}
	for i, shape := range shapes {
		index := NewShapeIndex()
		index.Add(shape)
		var buf bytes.Buffer
		if err := index.Encode(&buf); err != nil {
			t.Fatalf("shape %d (%T) Encode: %v", i, shape, err)
		}
		decoded := NewShapeIndex()
		if err := decoded.Decode(bytes.NewReader(buf.Bytes())); err != nil {
			t.Fatalf("shape %d (%T) Decode: %v", i, shape, err)
		}
		got := decoded.Shape(0)
		if err := shapesEquivalent(shape, got); err != nil {
			t.Fatalf("shape %d (%T): %v", i, shape, err)
		}
		if got.NumChains() != shape.NumChains() {
			t.Fatalf("shape %d NumChains = %d, want %d", i, got.NumChains(), shape.NumChains())
		}
		for c := 0; c < shape.NumChains(); c++ {
			if got.Chain(c) != shape.Chain(c) {
				t.Fatalf("shape %d chain %d = %+v, want %+v", i, c, got.Chain(c), shape.Chain(c))
			}
		}
		assertIndexEqual(t, index, decoded)
		quadraticValidate(t, decoded)
	}
}

func TestShapeIndexEncodeMixedIndex(t *testing.T) {
	index := NewShapeIndex()
	poly := makePolygon("0:0, 0:4, 4:0", false)
	line := Polyline(parsePoints("-1:0, -1:2"))
	pts := PointVector(parsePoints("8:8, 9:9"))
	index.Add(poly)
	index.Add(&line)
	index.Add(&pts)
	var buf bytes.Buffer
	if err := index.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// A second encode of the built index is stable.
	var again bytes.Buffer
	if err := index.Encode(&again); err != nil {
		t.Fatalf("Encode again: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), again.Bytes()) {
		t.Fatal("encoding the same index twice differed")
	}

	decoded := NewShapeIndex()
	if err := decoded.Decode(&buf); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertIndexEqual(t, index, decoded)
	quadraticValidate(t, decoded)

	inside := parsePoint("1:1")
	outside := parsePoint("20:20")
	qSrc := NewContainsPointQuery(index, VertexModelSemiOpen)
	qDec := NewContainsPointQuery(decoded, VertexModelSemiOpen)
	if qSrc.Contains(inside) != qDec.Contains(inside) || !qDec.Contains(inside) {
		t.Fatalf("inside containment src=%v decoded=%v", qSrc.Contains(inside), qDec.Contains(inside))
	}
	if qSrc.Contains(outside) || qDec.Contains(outside) {
		t.Fatal("outside point reported as contained")
	}
	if !decoded.IsFresh() {
		t.Fatal("query rebuilt the decoded index")
	}
}

func TestShapeIndexEncodeComplexGeometry(t *testing.T) {
	index := NewShapeIndex()
	index.Add(concentricLoopsPolygon(PointFromCoords(1, -1, -1), 3, 50))
	index.Add(RegularLoop(PointFromCoords(1, 0.5, 0.5), s1.Degree*80, 40))
	var buf bytes.Buffer
	if err := index.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded := NewShapeIndex()
	if err := decoded.Decode(&buf); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertIndexEqual(t, index, decoded)
	quadraticValidate(t, decoded)
	if !decoded.IsFresh() {
		t.Fatal("validation rebuilt the decoded index")
	}
}

func TestShapeIndexEncodeUnsupportedShape(t *testing.T) {
	index := NewShapeIndex()
	index.Add(edgeVectorShapeFromPoints(parsePoint("0:0"), parsePoint("1:1")))
	var buf bytes.Buffer
	if err := index.Encode(&buf); err == nil {
		t.Fatal("Encode succeeded for a shape with no type tag")
	}
	if buf.Len() != 0 {
		t.Fatal("failed Encode wrote bytes")
	}
}

func TestShapeIndexDecodeFailurePreservesIndex(t *testing.T) {
	line := Polyline(parsePoints("0:0, 1:1"))
	index := NewShapeIndex()
	index.Add(&line)
	index.Build()
	cells := append([]CellID(nil), index.cells...)

	if err := index.Decode(bytes.NewReader([]byte{1, 2, 3})); err == nil {
		t.Fatal("expected decode error")
	}
	if index.Len() != 1 || index.Shape(0).NumEdges() != 1 {
		t.Fatal("failed Decode mutated shapes")
	}
	if !index.IsFresh() || len(index.cells) != len(cells) {
		t.Fatal("failed Decode mutated the cell index")
	}
}

func TestShapeIndexDecodeMalformed(t *testing.T) {
	line := Polyline(parsePoints("0:0, 0:1, 1:1, 2:2"))
	poly := makePolygon("0:0, 0:3, 3:0", false)
	index := NewShapeIndex()
	index.Add(&line)
	index.Add(poly)
	var buf bytes.Buffer
	if err := index.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	encoded := buf.Bytes()

	for i := 0; i < len(encoded); i++ {
		dec := NewShapeIndex()
		if err := dec.Decode(bytes.NewReader(encoded[:i])); err == nil {
			t.Fatalf("prefix of %d bytes decoded", i)
		}
	}
	for i := range encoded {
		corrupted := append([]byte(nil), encoded...)
		corrupted[i] ^= 0x5a
		dec := NewShapeIndex()
		if err := dec.Decode(bytes.NewReader(corrupted)); err == nil {
			t.Fatalf("flipped byte %d decoded", i)
		}
	}

	rng := rand.New(rand.NewSource(1))
	for n := 0; n < 40; n++ {
		buf := make([]byte, rng.Intn(80))
		rng.Read(buf)
		dec := NewShapeIndex()
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on random input %x: %v", buf, r)
				}
			}()
			_ = dec.Decode(bytes.NewReader(buf))
		}()
	}
}

func TestShapeIndexDecodeOversizedAllocation(t *testing.T) {
	// Payload length far beyond the allowed maximum, with no following bytes.
	var huge bytes.Buffer
	e := &encoder{w: &huge}
	e.writeUint8(shapeIndexEncodingVersion)
	e.writeUint32(0)
	e.writeUvarint(uint64(maxShapeIndexBytes) + 1)
	if err := NewShapeIndex().Decode(&huge); err == nil {
		t.Fatal("huge payload length was accepted")
	}

	// A checksum-valid payload whose shape-id count cannot be allocated.
	rejectPayload(t, "next id", func(e *encoder) {
		e.writeInt32(int32(maxEncodedShapes + 1))
		e.writeInt32(10)
	})
	// Checksum-valid payload with a cell count the remaining bytes cannot hold.
	rejectPayload(t, "cell count", func(e *encoder) {
		e.writeInt32(0)
		e.writeInt32(10)
		e.writeUvarint(uint64(maxEncodedIndexCells))
	})
	rejectPayload(t, "shape byte count", func(e *encoder) {
		e.writeInt32(1)
		e.writeInt32(10)
		e.writeUint8(1)
		e.writeUvarint(uint64(typeTagPolyline))
		e.writeUvarint(uint64(maxShapeIndexBytes) + 1)
	})
	// A short polyline body whose vertex count would allocate a huge slice.
	rejectPayload(t, "polyline vertices", func(e *encoder) {
		var shape bytes.Buffer
		se := &encoder{w: &shape}
		se.writeInt8(encodingVersion)
		se.writeUint32(maxEncodedVertices)
		e.writeInt32(1)
		e.writeInt32(10)
		e.writeUint8(1)
		e.writeUvarint(uint64(typeTagPolyline))
		e.writeUvarint(uint64(shape.Len()))
		e.w.Write(shape.Bytes())
		e.writeUvarint(0)
	})
}

func TestShapeIndexEncodeNil(t *testing.T) {
	var index *ShapeIndex
	var buf bytes.Buffer
	if err := index.Encode(&buf); err == nil {
		t.Fatal("nil Encode succeeded")
	}
	if err := index.Decode(bytes.NewReader(nil)); err == nil {
		t.Fatal("nil Decode succeeded")
	}
	if err := NewShapeIndex().Encode(nil); err == nil {
		t.Fatal("Encode to a nil writer succeeded")
	}
	if err := NewShapeIndex().Decode(nil); err == nil {
		t.Fatal("Decode from a nil reader succeeded")
	}
}

func rejectPayload(t *testing.T, name string, write func(*encoder)) {
	t.Helper()
	var payload bytes.Buffer
	write(&encoder{w: &payload})
	var buf bytes.Buffer
	out := &encoder{w: &buf}
	out.writeUint8(shapeIndexEncodingVersion)
	out.writeUint32(crc32.ChecksumIEEE(payload.Bytes()))
	out.writeUvarint(uint64(payload.Len()))
	buf.Write(payload.Bytes())
	if err := NewShapeIndex().Decode(&buf); err == nil {
		t.Fatalf("%s: expected error", name)
	}
}

func assertIndexEqual(t *testing.T, a, b *ShapeIndex) {
	t.Helper()
	if a.nextID != b.nextID || a.maxEdgesPerCell != b.maxEdgesPerCell || a.Len() != b.Len() {
		t.Fatalf("index header a=(next %d edges %d len %d) b=(next %d edges %d len %d)",
			a.nextID, a.maxEdgesPerCell, a.Len(), b.nextID, b.maxEdgesPerCell, b.Len())
	}
	for id := int32(0); id < a.nextID; id++ {
		as, bs := a.Shape(id), b.Shape(id)
		if shapeIsNil(as) || shapeIsNil(bs) {
			if shapeIsNil(as) != shapeIsNil(bs) {
				t.Fatalf("shape %d presence = %v, want %v", id, !shapeIsNil(bs), !shapeIsNil(as))
			}
			continue
		}
		if err := shapesEquivalent(as, bs); err != nil {
			t.Fatalf("shape %d: %v", id, err)
		}
	}
	if len(a.cells) != len(b.cells) {
		t.Fatalf("cell count = %d, want %d", len(b.cells), len(a.cells))
	}
	for i, id := range a.cells {
		if b.cells[i] != id {
			t.Fatalf("cell[%d] = %v, want %v", i, b.cells[i], id)
		}
		ac, bc := a.cellMap[id], b.cellMap[id]
		if ac == nil || bc == nil {
			t.Fatalf("cell %v missing from a map", id)
		}
		if len(ac.shapes) != len(bc.shapes) {
			t.Fatalf("cell %v clipped shapes = %d, want %d", id, len(bc.shapes), len(ac.shapes))
		}
		for j := range ac.shapes {
			got, want := bc.shapes[j], ac.shapes[j]
			if got.shapeID != want.shapeID || got.containsCenter != want.containsCenter || !edgeIDsEqual(got.edges, want.edges) {
				t.Fatalf("cell %v clipped[%d] = {%d %v %v}, want {%d %v %v}",
					id, j, got.shapeID, got.containsCenter, got.edges, want.shapeID, want.containsCenter, want.edges)
			}
		}
	}
}

func shapesEquivalent(a, b Shape) error {
	if a.Dimension() != b.Dimension() || a.NumEdges() != b.NumEdges() || a.NumChains() != b.NumChains() ||
		a.IsEmpty() != b.IsEmpty() || a.IsFull() != b.IsFull() || a.ReferencePoint() != b.ReferencePoint() {
		return errShape(a, b, "summary")
	}
	for i := 0; i < a.NumEdges(); i++ {
		if a.Edge(i) != b.Edge(i) || a.ChainPosition(i) != b.ChainPosition(i) {
			return errShape(a, b, "edge")
		}
	}
	for c := 0; c < a.NumChains(); c++ {
		if a.Chain(c) != b.Chain(c) {
			return errShape(a, b, "chain")
		}
		chain := a.Chain(c)
		for offset := 0; offset < chain.Length; offset++ {
			if a.ChainEdge(c, offset) != b.ChainEdge(c, offset) {
				return errShape(a, b, "chain edge")
			}
		}
	}
	return nil
}

func errShape(a, b Shape, what string) error {
	return &shapeMismatch{what: what, a: a, b: b}
}

type shapeMismatch struct {
	what string
	a, b Shape
}

func (e *shapeMismatch) Error() string {
	return e.what + " mismatch"
}

func edgeIDsEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
