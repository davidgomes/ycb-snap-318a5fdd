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
	"math"
	"math/rand"
	"reflect"
	"runtime"
	"slices"
	"testing"
)

// encodeTestShapes returns one or more shapes of every built-in Shape type,
// including shapes with no edges and shapes with differing numbers of chains.
func encodeTestShapes() []Shape {
	points := PointVector(parsePoints("1:1, 2:2, -3:4, 10:-20, 45:45"))
	emptyPoints := PointVector(nil)
	return []Shape{
		makePolygon("0:0, 0:10, 10:10, 10:0; 2:2, 2:8, 8:8, 8:2", true),
		makePolygon("", false),
		makePolygon("full", false),
		makePolyline("0:20, 5:25, 0:30, 5:35"),
		makePolyline(""),
		&points,
		&emptyPoints,
		makeLaxPolyline("-10:-10, -12:-15, -10:-20"),
		makeLaxPolyline("30:30"),
		makeLaxPolygon("20:0, 20:5, 25:5, 25:0; 21:1; 22:2, 23:3; 24:1, 24:2, 24.5:1.5"),
		makeLaxPolygon(""),
		makeLaxPolygon("full"),
		makeLoop("-20:40, -20:45, -25:45, -25:40"),
		EmptyLoop(),
		FullLoop(),
		LaxLoopFromPoints(parsePoints("40:-40, 40:-35, 35:-35")),
		LaxLoopFromPoints(nil),
	}
}

// encodeTestIndex returns an unbuilt index containing encodeTestShapes, with
// a gap in the shape IDs left by a removed shape.
func encodeTestIndex() *ShapeIndex {
	index := NewShapeIndex()
	shapes := encodeTestShapes()
	for i, shape := range shapes {
		index.Add(shape)
		if i == 2 {
			removed := makeLaxPolyline("60:60, 61:61")
			index.Add(removed)
			index.Remove(removed)
		}
	}
	index.Add(makeLaxPolyline("-30:-30, -31:-35"))
	return index
}

func encodeShapeIndex(t *testing.T, index *ShapeIndex) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := index.Encode(&buf); err != nil {
		t.Fatalf("Encode() = %v", err)
	}
	return buf.Bytes()
}

func decodeShapeIndex(t *testing.T, data []byte) *ShapeIndex {
	t.Helper()
	index := NewShapeIndex()
	if err := index.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("Decode() = %v", err)
	}
	return index
}

func checkShapesEqual(t *testing.T, id int32, got, want Shape) {
	t.Helper()
	if gotType, wantType := fmt.Sprintf("%T", got), fmt.Sprintf("%T", want); gotType != wantType {
		t.Errorf("shape %d type = %s, want %s", id, gotType, wantType)
		return
	}
	if got.Dimension() != want.Dimension() {
		t.Errorf("shape %d Dimension() = %d, want %d", id, got.Dimension(), want.Dimension())
	}
	if got.IsEmpty() != want.IsEmpty() || got.IsFull() != want.IsFull() {
		t.Errorf("shape %d IsEmpty(), IsFull() = %t, %t, want %t, %t", id, got.IsEmpty(), got.IsFull(), want.IsEmpty(), want.IsFull())
	}
	if got.ReferencePoint() != want.ReferencePoint() {
		t.Errorf("shape %d ReferencePoint() = %v, want %v", id, got.ReferencePoint(), want.ReferencePoint())
	}
	if got.NumEdges() != want.NumEdges() {
		t.Errorf("shape %d NumEdges() = %d, want %d", id, got.NumEdges(), want.NumEdges())
		return
	}
	for e := range want.NumEdges() {
		if got.Edge(e) != want.Edge(e) {
			t.Errorf("shape %d Edge(%d) = %v, want %v", id, e, got.Edge(e), want.Edge(e))
		}
	}
	if got.NumChains() != want.NumChains() {
		t.Errorf("shape %d NumChains() = %d, want %d", id, got.NumChains(), want.NumChains())
		return
	}
	for c := range want.NumChains() {
		if got.Chain(c) != want.Chain(c) {
			t.Errorf("shape %d Chain(%d) = %v, want %v", id, c, got.Chain(c), want.Chain(c))
		}
	}
}

func checkShapeIndexesEqual(t *testing.T, got, want *ShapeIndex) {
	t.Helper()
	if got.Len() != want.Len() || got.nextID != want.nextID {
		t.Fatalf("Len(), nextID = %d, %d, want %d, %d", got.Len(), got.nextID, want.Len(), want.nextID)
	}
	for id := range want.nextID {
		gotShape, wantShape := got.Shape(id), want.Shape(id)
		if (gotShape == nil) != (wantShape == nil) {
			t.Errorf("Shape(%d) = %v, want %v", id, gotShape, wantShape)
			continue
		}
		if wantShape != nil {
			checkShapesEqual(t, id, gotShape, wantShape)
		}
	}

	if !got.IsFresh() {
		t.Errorf("decoded index is not fresh")
	}
	// Walk both indexes with iterators, which must not need to rebuild.
	gotIter, wantIter := got.Iterator(), want.Iterator()
	for ; !wantIter.Done(); wantIter.Next() {
		if gotIter.Done() {
			t.Fatalf("decoded index ended before cell %v", wantIter.CellID())
		}
		if gotIter.CellID() != wantIter.CellID() {
			t.Fatalf("cell = %v, want %v", gotIter.CellID(), wantIter.CellID())
		}
		if !reflect.DeepEqual(gotIter.IndexCell(), wantIter.IndexCell()) {
			t.Errorf("cell %v contents = %+v, want %+v", wantIter.CellID(), gotIter.IndexCell(), wantIter.IndexCell())
		}
		gotIter.Next()
	}
	if !gotIter.Done() {
		t.Errorf("decoded index has extra cell %v", gotIter.CellID())
	}
}

func shapeIDs(index *ShapeIndex, shapes []Shape) []int32 {
	var ids []int32
	for _, s := range shapes {
		ids = append(ids, index.idForShape(s))
	}
	slices.Sort(ids)
	return ids
}

func TestShapeIndexEncodeDecodeEmpty(t *testing.T) {
	data := encodeShapeIndex(t, NewShapeIndex())
	if len(data) == 0 {
		t.Fatalf("empty index encoded to no bytes")
	}
	got := decodeShapeIndex(t, data)
	checkShapeIndexesEqual(t, got, NewShapeIndex())
	if !got.Iterator().Done() {
		t.Errorf("decoded empty index has cells")
	}
}

func TestShapeIndexEncodeDecodeShapes(t *testing.T) {
	index := encodeTestIndex()
	if index.IsFresh() {
		t.Fatalf("test index should not have been built yet")
	}
	data := encodeShapeIndex(t, index)
	got := decodeShapeIndex(t, data)
	checkShapeIndexesEqual(t, got, index)

	if again := encodeShapeIndex(t, got); !bytes.Equal(again, data) {
		t.Errorf("re-encoding the decoded index gave different bytes")
	}

	// Every shape with edges must be present in the cells under its own ID.
	indexed := make(map[int32]bool)
	for it := got.Iterator(); !it.Done(); it.Next() {
		for _, c := range it.IndexCell().shapes {
			if c.numEdges() > 0 {
				indexed[c.shapeID] = true
			}
		}
	}
	for id, shape := range got.shapes {
		if shape.NumEdges() > 0 && !indexed[id] {
			t.Errorf("shape %d (%T) has edges but is in no index cell", id, shape)
		}
	}
}

func TestShapeIndexEncodeDecodeQueries(t *testing.T) {
	index := encodeTestIndex()
	got := decodeShapeIndex(t, encodeShapeIndex(t, index))

	var queryPoints []Point
	for _, shape := range index.shapes {
		for e := range shape.NumEdges() {
			queryPoints = append(queryPoints, shape.Edge(e).V0)
		}
	}
	for _, s := range []string{"5:5", "1:1", "22:2", "-22:42", "38:-37", "-11:-15", "0:0", "80:80"} {
		queryPoints = append(queryPoints, parsePoint(s))
	}
	r := rand.New(rand.NewSource(1))
	for range 200 {
		queryPoints = append(queryPoints, randomPoint(r))
	}

	for _, model := range []VertexModel{VertexModelOpen, VertexModelSemiOpen, VertexModelClosed} {
		gotQuery, wantQuery := NewContainsPointQuery(got, model), NewContainsPointQuery(index, model)
		for _, p := range queryPoints {
			gotIDs := shapeIDs(got, gotQuery.ContainingShapes(p))
			wantIDs := shapeIDs(index, wantQuery.ContainingShapes(p))
			if !slices.Equal(gotIDs, wantIDs) {
				t.Errorf("model %v: ContainingShapes(%v) = %v, want %v", model, p, gotIDs, wantIDs)
			}
		}
	}

	crossingsByID := func(index *ShapeIndex, a, b Point) map[int32][]int {
		byID := make(map[int32][]int)
		for shape, edges := range NewCrossingEdgeQuery(index).CrossingsEdgeMap(a, b, CrossingTypeAll) {
			byID[index.idForShape(shape)] = edges
		}
		return byID
	}
	for _, edge := range [][2]string{{"-5:-5", "30:30"}, {"5:-1", "5:11"}, {"2:22", "-40:-40"}} {
		a, b := parsePoint(edge[0]), parsePoint(edge[1])
		gotEdges, wantEdges := crossingsByID(got, a, b), crossingsByID(index, a, b)
		if len(wantEdges) == 0 {
			t.Errorf("test edge %v has no crossings", edge)
		}
		if !reflect.DeepEqual(gotEdges, wantEdges) {
			t.Errorf("CrossingsEdgeMap(%v) = %v, want %v", edge, gotEdges, wantEdges)
		}
	}
}

func TestShapeIndexEncodeDecodeEachShape(t *testing.T) {
	for i, shape := range encodeTestShapes() {
		t.Run(fmt.Sprintf("%d_%T", i, shape), func(t *testing.T) {
			index := NewShapeIndex()
			index.Add(shape)
			got := decodeShapeIndex(t, encodeShapeIndex(t, index))
			checkShapeIndexesEqual(t, got, index)
		})
	}
}

func TestShapeIndexEncodeUnsupportedShape(t *testing.T) {
	index := NewShapeIndex()
	index.Add(&edgeVectorShape{})
	var buf bytes.Buffer
	if err := index.Encode(&buf); err == nil {
		t.Errorf("Encode() of an unsupported shape type succeeded")
	}
}

func TestShapeIndexDecodeReplacesContents(t *testing.T) {
	data := encodeShapeIndex(t, encodeTestIndex())
	index := NewShapeIndex()
	index.Add(PolygonFromLoops([]*Loop{makeLoop("50:50, 50:55, 55:55")}))
	index.Build()
	if err := index.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("Decode() = %v", err)
	}
	checkShapeIndexesEqual(t, index, decodeShapeIndex(t, data))
}

func TestShapeIndexDecodeTruncated(t *testing.T) {
	data := encodeShapeIndex(t, encodeTestIndex())
	target := NewShapeIndex()
	target.Add(makePolyline("0:0, 1:1"))
	target.Build()
	want := target.cells
	for n := range len(data) {
		if err := target.Decode(bytes.NewReader(data[:n])); err == nil {
			t.Fatalf("Decode() of the first %d of %d bytes succeeded", n, len(data))
		}
	}
	if target.Len() != 1 || !slices.Equal(target.cells, want) {
		t.Errorf("failed Decode() modified the index")
	}
}

// checkDecodedIndexUsable touches every shape and edge referenced by the cells
// of a successfully decoded index.
func checkDecodedIndexUsable(t *testing.T, index *ShapeIndex) {
	t.Helper()
	for it := index.Iterator(); !it.Done(); it.Next() {
		if len(it.IndexCell().shapes) == 0 {
			t.Fatalf("decoded cell %v has no shapes", it.CellID())
		}
		for _, c := range it.IndexCell().shapes {
			shape := index.Shape(c.shapeID)
			if shape == nil {
				t.Fatalf("decoded cell %v refers to missing shape %d", it.CellID(), c.shapeID)
			}
			for _, e := range c.edges {
				shape.Edge(e)
			}
		}
	}
}

func TestShapeIndexDecodeCorrupted(t *testing.T) {
	data := encodeShapeIndex(t, encodeTestIndex())
	corrupted := make([]byte, len(data))
	for i := range data {
		for _, mask := range []byte{0x01, 0x02, 0x40, 0x80, 0xff} {
			copy(corrupted, data)
			corrupted[i] ^= mask
			index := NewShapeIndex()
			if err := index.Decode(bytes.NewReader(corrupted)); err == nil {
				checkDecodedIndexUsable(t, index)
			}
		}
	}

	r := rand.New(rand.NewSource(2))
	for range 2000 {
		copy(corrupted, data)
		for range 1 + r.Intn(8) {
			corrupted[r.Intn(len(corrupted))] = byte(r.Intn(256))
		}
		index := NewShapeIndex()
		if err := index.Decode(bytes.NewReader(corrupted[:r.Intn(len(corrupted)+1)])); err == nil {
			checkDecodedIndexUsable(t, index)
		}
	}
}

func TestShapeIndexDecodeOversizedCounts(t *testing.T) {
	const maxUint64 = math.MaxUint64
	header := func(e *encoder, nextID, numShapes uint64) {
		e.writeUint8(shapeIndexEncodingVersion)
		e.writeUvarint(10)
		e.writeUvarint(nextID)
		e.writeUvarint(numShapes)
	}
	oneShape := func(write func(e *encoder)) func(e *encoder) {
		return func(e *encoder) {
			header(e, 1, 1)
			e.writeUvarint(0)
			write(e)
		}
	}
	tests := []struct {
		name  string
		write func(e *encoder)
	}{
		{"max edges per cell", func(e *encoder) {
			e.writeUint8(shapeIndexEncodingVersion)
			e.writeUvarint(maxUint64)
		}},
		{"next shape ID", func(e *encoder) { header(e, maxUint64, 0) }},
		{"shape count", func(e *encoder) { header(e, math.MaxInt32, math.MaxInt32) }},
		{"shape ID", func(e *encoder) {
			header(e, 2, 1)
			e.writeUvarint(maxUint64)
		}},
		{"point vector", oneShape(func(e *encoder) {
			e.writeUint8(encodedShapePointVector)
			e.writeUvarint(maxEncodedVertices)
		})},
		{"lax polyline", oneShape(func(e *encoder) {
			e.writeUint8(encodedShapeLaxPolyline)
			e.writeUvarint(maxUint64)
		})},
		{"lax loop", oneShape(func(e *encoder) {
			e.writeUint8(encodedShapeLaxLoop)
			e.writeUvarint(maxEncodedVertices)
		})},
		{"lax polygon loops", oneShape(func(e *encoder) {
			e.writeUint8(encodedShapeLaxPolygon)
			e.writeUvarint(maxEncodedLoops)
		})},
		{"lax polygon vertices", oneShape(func(e *encoder) {
			e.writeUint8(encodedShapeLaxPolygon)
			e.writeUvarint(2)
			e.writeUvarint(maxEncodedVertices)
			e.writeUvarint(maxEncodedVertices)
		})},
		{"polyline", oneShape(func(e *encoder) {
			e.writeUint8(encodedShapePolyline)
			e.writeInt8(encodingVersion)
			e.writeUint32(maxEncodedVertices)
		})},
		{"loop", oneShape(func(e *encoder) {
			e.writeUint8(encodedShapeLoop)
			e.writeInt8(encodingVersion)
			e.writeUint32(maxEncodedVertices)
		})},
		{"polygon", oneShape(func(e *encoder) {
			e.writeUint8(encodedShapePolygon)
			e.writeInt8(encodingVersion)
			e.writeBool(true)
			e.writeBool(false)
			e.writeUint32(maxEncodedLoops)
		})},
		{"cell count", func(e *encoder) {
			header(e, 0, 0)
			e.writeUvarint(maxUint64)
			e.writeUvarint(uint64(CellIDFromFace(0)))
		}},
		{"clipped shape count", oneShape(func(e *encoder) {
			e.writeUint8(encodedShapePointVector)
			(&PointVector{}).encode(e)
			e.writeUvarint(1)
			e.writeUvarint(uint64(CellIDFromFace(0)))
			e.writeUvarint(maxUint64)
		})},
		{"clipped edge count", func(e *encoder) {
			header(e, 1, 1)
			e.writeUvarint(0)
			e.writeUint8(encodedShapeLaxPolyline)
			(&LaxPolyline{vertices: parsePoints("0:0, 1:1")}).encode(e)
			e.writeUvarint(1)
			e.writeUvarint(uint64(CellIDFromFace(0)))
			e.writeUvarint(1)
			e.writeUvarint(0)
			e.writeUvarint(maxUint64 &^ 1)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var buf bytes.Buffer
			e := &encoder{w: &buf}
			test.write(e)
			if e.err != nil {
				t.Fatalf("writing test input: %v", e.err)
			}

			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			err := NewShapeIndex().Decode(bytes.NewReader(buf.Bytes()))
			runtime.ReadMemStats(&after)
			if err == nil {
				t.Errorf("Decode() succeeded")
			}
			if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 1<<20 {
				t.Errorf("Decode() allocated %d bytes", allocated)
			}
		})
	}
}

func TestShapeIndexDecodeInvalidCells(t *testing.T) {
	face0, face1 := CellIDFromFace(0), CellIDFromFace(1)
	tests := []struct {
		name  string
		cells func(e *encoder)
	}{
		{"invalid cell ID", func(e *encoder) {
			e.writeUvarint(1)
			e.writeUvarint(0)
		}},
		{"duplicate cell", func(e *encoder) {
			e.writeUvarint(2)
			e.writeUvarint(uint64(face0))
			e.writeUvarint(1)
			e.writeUvarint(0)
			e.writeUvarint(1)
			e.writeUvarint(0)
		}},
		{"overlapping cells", func(e *encoder) {
			e.writeUvarint(2)
			e.writeUvarint(uint64(face0))
			e.writeUvarint(1)
			e.writeUvarint(0)
			e.writeUvarint(1)
			e.writeUvarint(uint64(face0.Children()[3] - face0))
		}},
		{"empty cell", func(e *encoder) {
			e.writeUvarint(1)
			e.writeUvarint(uint64(face1))
			e.writeUvarint(0)
		}},
		{"missing shape", func(e *encoder) {
			e.writeUvarint(1)
			e.writeUvarint(uint64(face1))
			e.writeUvarint(1)
			e.writeUvarint(1)
			e.writeUvarint(0)
		}},
		{"edge out of range", func(e *encoder) {
			e.writeUvarint(1)
			e.writeUvarint(uint64(face1))
			e.writeUvarint(1)
			e.writeUvarint(0)
			e.writeUvarint(1 << 1)
			e.writeUvarint(1)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var buf bytes.Buffer
			e := &encoder{w: &buf}
			e.writeUint8(shapeIndexEncodingVersion)
			e.writeUvarint(10)
			e.writeUvarint(2)
			e.writeUvarint(1)
			e.writeUvarint(0)
			e.writeUint8(encodedShapeLaxPolyline)
			(&LaxPolyline{vertices: parsePoints("0:0, 1:1")}).encode(e)
			test.cells(e)
			if err := NewShapeIndex().Decode(&buf); err == nil {
				t.Errorf("Decode() succeeded")
			}
		})
	}
}

func FuzzShapeIndexDecode(f *testing.F) {
	for _, index := range []*ShapeIndex{NewShapeIndex(), encodeTestIndex()} {
		var buf bytes.Buffer
		if err := index.Encode(&buf); err != nil {
			f.Fatal(err)
		}
		f.Add(buf.Bytes())
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		index := NewShapeIndex()
		if err := index.Decode(bytes.NewReader(data)); err == nil {
			checkDecodedIndexUsable(t, index)
		}
	})
}
