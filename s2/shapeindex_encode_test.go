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
	"math/rand"
	"slices"
	"testing"

	"github.com/golang/geo/s1"
)

func shapeIndexRoundTrip(t *testing.T, index *ShapeIndex) *ShapeIndex {
	t.Helper()
	var buf bytes.Buffer
	if err := index.Encode(&buf); err != nil {
		t.Fatalf("Encode() = %v", err)
	}
	if buf.Len() == 0 {
		t.Fatalf("Encode() wrote no bytes")
	}
	got := NewShapeIndex()
	if err := got.Decode(&buf); err != nil {
		t.Fatalf("Decode() = %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("Decode() left %d unread bytes", buf.Len())
	}
	return got
}

func checkShapesEqual(t *testing.T, id int32, want, got Shape) {
	t.Helper()
	if fmt.Sprintf("%T", want) != fmt.Sprintf("%T", got) {
		t.Fatalf("shape %d: type = %T, want %T", id, got, want)
	}
	if got.Dimension() != want.Dimension() {
		t.Errorf("shape %d: Dimension() = %d, want %d", id, got.Dimension(), want.Dimension())
	}
	if got.NumEdges() != want.NumEdges() {
		t.Fatalf("shape %d: NumEdges() = %d, want %d", id, got.NumEdges(), want.NumEdges())
	}
	for e := 0; e < want.NumEdges(); e++ {
		if got.Edge(e) != want.Edge(e) {
			t.Errorf("shape %d: Edge(%d) = %v, want %v", id, e, got.Edge(e), want.Edge(e))
		}
	}
	if got.NumChains() != want.NumChains() {
		t.Fatalf("shape %d: NumChains() = %d, want %d", id, got.NumChains(), want.NumChains())
	}
	for c := 0; c < want.NumChains(); c++ {
		if got.Chain(c) != want.Chain(c) {
			t.Errorf("shape %d: Chain(%d) = %v, want %v", id, c, got.Chain(c), want.Chain(c))
		}
	}
	if got.ReferencePoint() != want.ReferencePoint() {
		t.Errorf("shape %d: ReferencePoint() = %v, want %v", id, got.ReferencePoint(), want.ReferencePoint())
	}
	if got.IsEmpty() != want.IsEmpty() || got.IsFull() != want.IsFull() {
		t.Errorf("shape %d: IsEmpty/IsFull = %v/%v, want %v/%v", id,
			got.IsEmpty(), got.IsFull(), want.IsEmpty(), want.IsFull())
	}
}

func checkShapeIndexEqual(t *testing.T, want, got *ShapeIndex) {
	t.Helper()
	if got.Len() != want.Len() {
		t.Fatalf("Len() = %d, want %d", got.Len(), want.Len())
	}
	if got.nextID != want.nextID {
		t.Errorf("nextID = %d, want %d", got.nextID, want.nextID)
	}
	for id, shape := range want.shapes {
		gotShape := got.Shape(id)
		if gotShape == nil {
			t.Fatalf("Shape(%d) missing after decode", id)
		}
		checkShapesEqual(t, id, shape, gotShape)
	}
	if !got.IsFresh() {
		t.Errorf("decoded index is not fresh")
	}
	if !slices.Equal(got.cells, want.cells) {
		t.Fatalf("cells = %v, want %v", got.cells, want.cells)
	}
	for _, id := range want.cells {
		wc, gc := want.cellMap[id], got.cellMap[id]
		if len(gc.shapes) != len(wc.shapes) {
			t.Fatalf("cell %v: %d clipped shapes, want %d", id, len(gc.shapes), len(wc.shapes))
		}
		for i, w := range wc.shapes {
			g := gc.shapes[i]
			if g.shapeID != w.shapeID || g.containsCenter != w.containsCenter || !slices.Equal(g.edges, w.edges) {
				t.Errorf("cell %v clipped %d = %+v, want %+v", id, i, *g, *w)
			}
		}
	}
}

func encodeTestShapes() []Shape {
	pv := PointVector(parsePoints("1:1, 2:2, 3:3"))
	emptyPV := PointVector{}
	emptyPolyline := Polyline{}
	return []Shape{
		&pv,
		&emptyPV,
		makePolyline("0:0, 0:10, 10:10"),
		&emptyPolyline,
		makeLaxPolyline("5:5, 5:6, 5:6, 6:6"),
		makeLaxPolyline("7:7"),
		LaxLoopFromPoints(parsePoints("20:20, 20:21, 21:21")),
		LaxLoopFromPoints(nil),
		makeLaxPolygon("30:30, 30:31, 31:31; 32:32; full; 33:33, 34:34"),
		makeLaxPolygon(""),
		makeLaxPolygon("40:40, 40:41, 41:41"),
		makeLoop("-10:-10, -10:-5, -5:-5"),
		EmptyLoop(),
		FullLoop(),
		RegularLoop(PointFromLatLng(LatLngFromDegrees(-40, 60)), s1.Degree*5, 200),
		makePolygon("50:50, 50:60, 60:60, 60:50; 52:52, 58:52, 58:58, 52:58", true),
		makePolygon("", false),
		FullPolygon(),
	}
}

func TestShapeIndexEncodeDecodeAllShapeTypes(t *testing.T) {
	for _, build := range []bool{true, false} {
		t.Run(fmt.Sprintf("build=%v", build), func(t *testing.T) {
			index := NewShapeIndex()
			for _, s := range encodeTestShapes() {
				index.Add(s)
			}
			if build {
				index.Build()
			}
			got := shapeIndexRoundTrip(t, index)
			checkShapeIndexEqual(t, index, got)
		})
	}
}

func TestShapeIndexEncodeDecodeEachShapeType(t *testing.T) {
	for _, s := range encodeTestShapes() {
		t.Run(fmt.Sprintf("%T", s), func(t *testing.T) {
			index := NewShapeIndex()
			index.Add(s)
			got := shapeIndexRoundTrip(t, index)
			checkShapeIndexEqual(t, index, got)
		})
	}
}

func TestShapeIndexEncodeDecodeEmpty(t *testing.T) {
	index := NewShapeIndex()
	var buf bytes.Buffer
	if err := index.Encode(&buf); err != nil {
		t.Fatalf("Encode() = %v", err)
	}
	if buf.Len() == 0 {
		t.Fatalf("empty index encoded to zero bytes")
	}
	got := NewShapeIndex()
	got.Add(makeLoop("0:0, 0:1, 1:1"))
	if err := got.Decode(&buf); err != nil {
		t.Fatalf("Decode() = %v", err)
	}
	checkShapeIndexEqual(t, index, got)
	if !got.Iterator().Done() {
		t.Errorf("decoded empty index has cells")
	}
}

func TestShapeIndexEncodeDecodePreservesShapeIDs(t *testing.T) {
	index := NewShapeIndex()
	shapes := encodeTestShapes()
	ids := make([]int32, len(shapes))
	for i, s := range shapes {
		ids[i] = index.Add(s)
	}
	got := shapeIndexRoundTrip(t, index)
	for i, id := range ids {
		checkShapesEqual(t, id, shapes[i], got.Shape(id))
	}
	if next := got.Add(makeLoop("0:0, 0:1, 1:1")); next != int32(len(shapes)) {
		t.Errorf("Add() after decode = %d, want %d", next, len(shapes))
	}
}

func TestShapeIndexEncodeDecodeQueries(t *testing.T) {
	index := NewShapeIndex()
	polygon := makePolygon("0:0, 0:10, 10:10, 10:0; 2:2, 8:2, 8:8, 2:8", true)
	index.Add(polygon)
	index.Add(makePolyline("20:20, 25:25, 30:20"))
	index.Add(RegularLoop(PointFromLatLng(LatLngFromDegrees(40, 40)), s1.Degree*3, 500))

	got := shapeIndexRoundTrip(t, index)
	checkShapeIndexEqual(t, index, got)

	wantQuery := NewContainsPointQuery(index, VertexModelSemiOpen)
	gotQuery := NewContainsPointQuery(got, VertexModelSemiOpen)
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		p := PointFromLatLng(LatLngFromDegrees(rng.Float64()*60-10, rng.Float64()*60-10))
		if w, g := wantQuery.Contains(p), gotQuery.Contains(p); w != g {
			t.Fatalf("Contains(%v) = %v, want %v", p, g, w)
		}
	}

	if !gotQuery.Contains(PointFromLatLng(LatLngFromDegrees(1, 1))) {
		t.Errorf("decoded index does not contain a point inside the polygon shell")
	}
	if gotQuery.Contains(PointFromLatLng(LatLngFromDegrees(5, 5))) {
		t.Errorf("decoded index contains a point inside the polygon hole")
	}

	n := 0
	for it := got.Iterator(); !it.Done(); it.Next() {
		n++
	}
	if n != len(index.cells) || n < 2 {
		t.Errorf("iterated %d cells, want %d (> 1)", n, len(index.cells))
	}
}

func TestShapeIndexEncodeUnsupportedShape(t *testing.T) {
	index := NewShapeIndex()
	index.Add(&edgeVectorShape{})
	if err := index.Encode(&bytes.Buffer{}); err == nil {
		t.Errorf("Encode() of unsupported shape type succeeded, want error")
	}
}

func encodedTestIndex(t *testing.T) []byte {
	t.Helper()
	index := NewShapeIndex()
	for _, s := range encodeTestShapes() {
		index.Add(s)
	}
	var buf bytes.Buffer
	if err := index.Encode(&buf); err != nil {
		t.Fatalf("Encode() = %v", err)
	}
	return buf.Bytes()
}

func TestShapeIndexDecodeTruncated(t *testing.T) {
	data := encodedTestIndex(t)
	for n := 0; n < len(data); n++ {
		index := NewShapeIndex()
		if err := index.Decode(bytes.NewReader(data[:n])); err == nil {
			t.Fatalf("Decode() of %d/%d bytes succeeded, want error", n, len(data))
		}
	}
}

func TestShapeIndexDecodeCorrupted(t *testing.T) {
	data := encodedTestIndex(t)
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 5000; i++ {
		corrupt := slices.Clone(data)
		for j := 0; j <= rng.Intn(4); j++ {
			corrupt[rng.Intn(len(corrupt))] = byte(rng.Intn(256))
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Decode() panicked on corrupted input: %v", r)
				}
			}()
			NewShapeIndex().Decode(bytes.NewReader(corrupt))
		}()
	}
	for i := 0; i < 2000; i++ {
		garbage := make([]byte, rng.Intn(64))
		rng.Read(garbage)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Decode() panicked on random input %x: %v", garbage, r)
				}
			}()
			NewShapeIndex().Decode(bytes.NewReader(garbage))
		}()
	}
}

func TestShapeIndexDecodeErrorLeavesIndexUnchanged(t *testing.T) {
	index := NewShapeIndex()
	loop := makeLoop("0:0, 0:1, 1:1")
	index.Add(loop)
	index.Build()
	if err := index.Decode(bytes.NewReader([]byte{99})); err == nil {
		t.Fatalf("Decode() of bad version succeeded, want error")
	}
	if index.Len() != 1 || index.Shape(0) != loop {
		t.Errorf("failed Decode() modified the index")
	}
}

func TestShapeIndexDecodeOversizedCounts(t *testing.T) {
	uv := func(x uint64) []byte { return binary.AppendUvarint(nil, x) }
	cat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	header := cat([]byte{shapeIndexEncodingVersion}, uv(10), uv(1), uv(1), uv(0))

	tests := []struct {
		name string
		data []byte
	}{
		{"huge nextID", cat([]byte{shapeIndexEncodingVersion}, uv(10), uv(1<<40))},
		{"shapes exceed nextID", cat([]byte{shapeIndexEncodingVersion}, uv(10), uv(2), uv(3))},
		{"huge vertex count", cat(header, []byte{byte(shapeKindPointVector)}, uv(1<<62))},
		{"vertex count below limit but no data", cat(header, []byte{byte(shapeKindPolyline)}, uv(maxEncodedVertices))},
		{"huge loop count", cat(header, []byte{byte(shapeKindPolygon)}, uv(1<<50))},
		{"loop count below limit but no data", cat(header, []byte{byte(shapeKindLaxPolygon)}, uv(maxEncodedLoops))},
		{"huge cell count", cat([]byte{shapeIndexEncodingVersion}, uv(10), uv(0), uv(0), uv(1<<40))},
		{"unknown shape kind", cat(header, []byte{200})},
		{"varint overflow", cat([]byte{shapeIndexEncodingVersion}, bytes.Repeat([]byte{0xff}, 11))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := NewShapeIndex().Decode(bytes.NewReader(test.data)); err == nil {
				t.Errorf("Decode() succeeded, want error")
			}
		})
	}
}

func TestShapeIndexDecodeInvalidCellReferences(t *testing.T) {
	uv := func(x uint64) []byte { return binary.AppendUvarint(nil, x) }
	cat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	cellID := func(id CellID) []byte { return binary.LittleEndian.AppendUint64(nil, uint64(id)) }
	// One PointVector with a single point, then one cell.
	prefix := cat([]byte{shapeIndexEncodingVersion}, uv(10), uv(1), uv(1),
		uv(0), []byte{byte(shapeKindPointVector)}, uv(1), make([]byte, 24), uv(1))
	valid := CellIDFromFace(0)

	tests := []struct {
		name string
		data []byte
		ok   bool
	}{
		{"valid", cat(prefix, cellID(valid), []byte{1}, uv(1), uv(0), []byte{0}, uv(1), uv(0)), true},
		{"invalid cell id", cat(prefix, cellID(0), []byte{1}, uv(1), uv(0), []byte{0}, uv(1), uv(0)), false},
		{"unknown shape", cat(prefix, cellID(valid), []byte{1}, uv(1), uv(5), []byte{0}, uv(1), uv(0)), false},
		{"edge out of range", cat(prefix, cellID(valid), []byte{1}, uv(1), uv(0), []byte{0}, uv(1), uv(1)), false},
		{"too many edges", cat(prefix, cellID(valid), []byte{1}, uv(1), uv(0), []byte{0}, uv(2), uv(0), uv(0)), false},
		{"too many clipped shapes", cat(prefix, cellID(valid), []byte{1}, uv(2)), false},
		{"bad bool", cat(prefix, cellID(valid), []byte{7}), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := NewShapeIndex().Decode(bytes.NewReader(test.data))
			if (err == nil) != test.ok {
				t.Errorf("Decode() = %v, want ok=%v", err, test.ok)
			}
		})
	}
}
