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
	"testing"
)

func TestShapeIndexEncodeEmpty(t *testing.T) {
	index := NewShapeIndex()
	var buf bytes.Buffer
	if err := index.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("empty index encoded to an empty stream")
	}

	decoded := NewShapeIndex()
	if err := decoded.Decode(&buf); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.Len() != 0 || decoded.nextID != 0 || !decoded.IsFresh() {
		t.Fatalf("decoded empty index = len %d next %d fresh %v", decoded.Len(), decoded.nextID, decoded.IsFresh())
	}
	if it := decoded.Iterator(); !it.Done() {
		t.Fatal("decoded empty index should have no cells")
	}
}

func TestShapeIndexEncodeRoundTrip(t *testing.T) {
	pts := func(lat, lng float64) Point {
		return PointFromLatLng(LatLngFromDegrees(lat, lng))
	}

	poly := makePolygon("0:0, 0:10, 10:0", true)
	line := PolylineFromLatLngs([]LatLng{
		LatLngFromDegrees(1, 1),
		LatLngFromDegrees(2, 3),
		LatLngFromDegrees(4, 1),
	})
	points := PointVector{pts(20, 20), pts(21, 22)}
	laxLine := LaxPolylineFromPoints([]Point{pts(30, 30), pts(31, 30), pts(31, 32)})
	laxPoly := LaxPolygonFromPoints([][]Point{
		{pts(40, 40), pts(40, 41), pts(41, 40)},
		{},
		{pts(50, 50), pts(50, 52), pts(52, 52), pts(52, 50)},
	})
	loop := makeLoop("-10:-10, -10:-8, -8:-10")
	laxLoop := LaxLoopFromPoints([]Point{pts(60, 60), pts(60, 61), pts(61, 60)})

	index := NewShapeIndex()
	index.maxEdgesPerCell = 4
	ids := []int32{
		index.Add(poly),
		index.Add(line),
		index.Add(&points),
		index.Add(laxLine),
		index.Add(laxPoly),
		index.Add(loop),
		index.Add(laxLoop),
	}
	// Leave the index stale so Encode has to build it.
	if index.IsFresh() {
		t.Fatal("index should be stale before Encode")
	}

	var buf bytes.Buffer
	if err := index.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !index.IsFresh() || len(index.cells) == 0 {
		t.Fatal("Encode should build the cell structure")
	}

	decoded := NewShapeIndex()
	if err := decoded.Decode(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !decoded.IsFresh() {
		t.Fatal("decoded index should be fresh")
	}
	if decoded.maxEdgesPerCell != 4 {
		t.Fatalf("maxEdgesPerCell = %d, want 4", decoded.maxEdgesPerCell)
	}
	if decoded.nextID != index.nextID {
		t.Fatalf("nextID = %d, want %d", decoded.nextID, index.nextID)
	}
	for _, id := range ids {
		got := decoded.Shape(id)
		want := index.Shape(id)
		if got == nil || want == nil {
			t.Fatalf("shape %d missing after decode", id)
		}
		if err := shapesEqual(want, got); err != nil {
			t.Fatalf("shape %d: %v", id, err)
		}
	}

	assertSameCells(t, index, decoded)

	// A second Build must not be required for queries.
	before := len(decoded.cells)
	q := NewContainsPointQuery(decoded, VertexModelSemiOpen)
	inside := PointFromLatLng(LatLngFromDegrees(1, 1))
	outside := PointFromLatLng(LatLngFromDegrees(30, 30))
	if !q.Contains(inside) {
		t.Fatal("decoded index should contain a point of the polygon")
	}
	if q.Contains(outside) {
		t.Fatal("decoded index should not contain a far point")
	}
	if len(decoded.cells) != before {
		t.Fatal("query rebuilt the cell structure")
	}
	decoded.Build()
	assertSameCells(t, index, decoded)
}

func TestShapeIndexEncodePreservesShapeIDs(t *testing.T) {
	keep := PointVector{PointFromLatLng(LatLngFromDegrees(5, 5))}
	drop := PointVector{PointFromLatLng(LatLngFromDegrees(6, 6))}
	index := NewShapeIndex()
	dropped := index.Add(&drop)
	kept := index.Add(&keep)
	index.Remove(&drop)
	index.Build()

	var buf bytes.Buffer
	if err := index.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded := NewShapeIndex()
	if err := decoded.Decode(&buf); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.Shape(dropped) != nil {
		t.Fatal("removed shape id was reused")
	}
	if decoded.Shape(kept) == nil {
		t.Fatal("kept shape id was lost")
	}
	for _, id := range decoded.cells {
		for _, clipped := range decoded.cellMap[id].shapes {
			if clipped.shapeID != kept {
				t.Fatalf("cell references shape %d, want %d", clipped.shapeID, kept)
			}
		}
	}
	next := decoded.Add(&PointVector{PointFromLatLng(LatLngFromDegrees(7, 7))})
	if next != kept+1 {
		t.Fatalf("Add returned %d, want %d", next, kept+1)
	}
}

func TestShapeIndexEncodeZeroEdgesAndMixedChains(t *testing.T) {
	emptyPoints := PointVector{}
	emptyLine := Polyline{}
	shortLine := LaxPolylineFromPoints([]Point{PointFromLatLng(LatLngFromDegrees(1, 1))})
	full := FullPolygon()
	emptyPoly := PolygonFromLoops(nil)
	mixed := LaxPolygonFromPoints([][]Point{
		{},
		{
			PointFromLatLng(LatLngFromDegrees(0, 0)),
			PointFromLatLng(LatLngFromDegrees(0, 1)),
			PointFromLatLng(LatLngFromDegrees(1, 0)),
		},
		{
			PointFromLatLng(LatLngFromDegrees(2, 2)),
		},
	})
	emptyLoop := LaxLoopFromPoints(nil)

	index := NewShapeIndex()
	shapes := []Shape{&emptyPoints, &emptyLine, shortLine, full, emptyPoly, mixed, emptyLoop}
	for _, shape := range shapes {
		index.Add(shape)
	}
	// Do not call Build; Encode must still persist the shapes and any cells.
	var buf bytes.Buffer
	if err := index.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded := NewShapeIndex()
	if err := decoded.Decode(&buf); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.Len() != len(shapes) {
		t.Fatalf("len = %d, want %d", decoded.Len(), len(shapes))
	}
	for id, want := range shapes {
		got := decoded.Shape(int32(id))
		if err := shapesEqual(want, got); err != nil {
			t.Fatalf("shape %d: %v", id, err)
		}
	}
	if got, want := decoded.Shape(5).NumChains(), 3; got != want {
		t.Fatalf("mixed chains = %d, want %d", got, want)
	}
	wantLens := []int{0, 3, 1}
	for i, want := range wantLens {
		if got := decoded.Shape(5).Chain(i).Length; got != want {
			t.Fatalf("chain %d length = %d, want %d", i, got, want)
		}
	}
	if got := decoded.Shape(2).(*LaxPolyline); len(got.vertices) != 1 {
		t.Fatalf("zero-edge polyline vertices = %d, want 1", len(got.vertices))
	}
	assertSameCells(t, index, decoded)
}

func TestShapeIndexDecodeMalformed(t *testing.T) {
	valid := NewShapeIndex()
	valid.Add(&PointVector{PointFromLatLng(LatLngFromDegrees(0, 0))})
	var good bytes.Buffer
	if err := valid.Encode(&good); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	payload := good.Bytes()

	truncated := payload[:len(payload)/2]
	corrupted := bytes.Clone(payload)
	corrupted[0] = 99
	badTag := bytes.Clone(payload)
	// Version, maxEdges, nextID, pending are fixed width; the shape tag
	// follows the shape count. Overwrite a mid-stream byte that is a tag
	// or a count so the stream is no longer a valid index.
	if len(badTag) > 20 {
		badTag[20] ^= 0xff
	}

	oversized := encodeOversizedShapeCount()

	cases := [][]byte{
		nil,
		{},
		{shapeIndexEncodingVersion},
		truncated,
		corrupted,
		badTag,
		oversized,
		encodeOversizedVertexCount(),
	}
	for i, in := range cases {
		decoded := NewShapeIndex()
		err := decoded.Decode(bytes.NewReader(in))
		if err == nil {
			t.Errorf("case %d: Decode succeeded", i)
		}
	}
}

func encodeOversizedShapeCount() []byte {
	var buf bytes.Buffer
	e := &encoder{w: &buf}
	e.writeUint8(shapeIndexEncodingVersion)
	e.writeInt32(10)
	e.writeInt32(0)
	e.writeInt32(0)
	e.writeUint32(maxEncodedShapes + 1)
	return buf.Bytes()
}

func encodeOversizedVertexCount() []byte {
	var buf bytes.Buffer
	e := &encoder{w: &buf}
	e.writeUint8(shapeIndexEncodingVersion)
	e.writeInt32(10)
	e.writeInt32(1)
	e.writeInt32(1)
	e.writeUint32(1) // one shape
	e.writeInt32(0)  // id
	e.writeUint32(uint32(typeTagPointVector))
	e.writeUint32(maxEncodedVertices + 1)
	return buf.Bytes()
}

func shapesEqual(a, b Shape) error {
	if a == nil || b == nil {
		return errString("nil shape")
	}
	if a.Dimension() != b.Dimension() || a.NumEdges() != b.NumEdges() || a.NumChains() != b.NumChains() || a.IsEmpty() != b.IsEmpty() || a.IsFull() != b.IsFull() {
		return errString("shape summary mismatch")
	}
	for e := 0; e < a.NumEdges(); e++ {
		ae, be := a.Edge(e), b.Edge(e)
		if ae != be {
			return errString("edge mismatch")
		}
		if a.ChainPosition(e) != b.ChainPosition(e) {
			return errString("chain position mismatch")
		}
	}
	for c := 0; c < a.NumChains(); c++ {
		if a.Chain(c) != b.Chain(c) {
			return errString("chain mismatch")
		}
	}
	ar, br := a.ReferencePoint(), b.ReferencePoint()
	if ar.Contained != br.Contained || ar.Point != br.Point {
		return errString("reference point mismatch")
	}
	return nil
}

func assertSameCells(t *testing.T, a, b *ShapeIndex) {
	t.Helper()
	if len(a.cells) != len(b.cells) {
		t.Fatalf("cell count %d != %d", len(a.cells), len(b.cells))
	}
	for i, id := range a.cells {
		if b.cells[i] != id {
			t.Fatalf("cell %d id %v != %v", i, b.cells[i], id)
		}
		ac, bc := a.cellMap[id], b.cellMap[id]
		if len(ac.shapes) != len(bc.shapes) {
			t.Fatalf("cell %v clipped %d != %d", id, len(ac.shapes), len(bc.shapes))
		}
		for j := range ac.shapes {
			if ac.shapes[j].shapeID != bc.shapes[j].shapeID ||
				ac.shapes[j].containsCenter != bc.shapes[j].containsCenter ||
				!sameInts(ac.shapes[j].edges, bc.shapes[j].edges) {
				t.Fatalf("cell %v clipped shape %d mismatch", id, j)
			}
		}
	}
}

func sameInts(a, b []int) bool {
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

type errString string

func (e errString) Error() string { return string(e) }
