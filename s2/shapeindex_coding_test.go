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
		t.Fatal("empty index should encode to non-empty bytes")
	}

	decoded := NewShapeIndex()
	if err := decoded.Decode(&buf); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.Len() != 0 {
		t.Errorf("decoded Len = %d, want 0", decoded.Len())
	}
	if !decoded.IsFresh() {
		t.Error("decoded index should be fresh")
	}
}

func TestShapeIndexEncodeDecodeWithoutBuild(t *testing.T) {
	index := NewShapeIndex()
	pts := parsePoints("0:0, 0:1, 0:2")
	polyline := Polyline(pts)
	index.Add(&polyline)

	if index.IsFresh() {
		t.Fatal("index should be stale before explicit Build")
	}

	var buf bytes.Buffer
	if err := index.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	decoded := NewShapeIndex()
	if err := decoded.Decode(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !decoded.IsFresh() {
		t.Error("decoded index should be fresh without calling Build")
	}
	shape0 := decoded.Shape(0)
	if shape0 == nil {
		t.Fatal("shape 0 missing after decode")
	}
	if shape0.NumEdges() != 2 {
		t.Errorf("NumEdges = %d, want 2", shape0.NumEdges())
	}
	quadraticValidate(t, decoded)
}

func TestShapeIndexEncodePreservesShapeIDs(t *testing.T) {
	index := NewShapeIndex()
	p1 := Polyline(parsePoints("0:0, 0:1"))
	p2 := Polyline(parsePoints("1:0, 1:1"))
	id0 := index.Add(&p1)
	id1 := index.Add(&p2)
	if id0 != 0 || id1 != 1 {
		t.Fatalf("shape ids = (%d, %d), want (0, 1)", id0, id1)
	}

	var buf bytes.Buffer
	if err := index.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded := NewShapeIndex()
	if err := decoded.Decode(&buf); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.Shape(0) == nil || decoded.Shape(1) == nil {
		t.Fatal("shape ids not preserved")
	}
	if decoded.Shape(2) != nil {
		t.Error("unexpected shape at id 2")
	}
}

func TestShapeIndexEncodeDecodeBuiltinShapes(t *testing.T) {
	cross := makePolygon("0:0, 0:1, 1:1, 1:0", false)
	polyline := Polyline(parsePoints("0:0, 0:1, 0:2"))
	points := PointVector(parsePoints("0:0, 1:0"))
	laxPolyline := LaxPolylineFromPoints(parsePoints("2:0, 3:0, 4:0"))
	laxPolygon := LaxPolygonFromPoints([][]Point{
		parsePoints("0:0, 0:1, 1:1, 1:0"),
		parsePoints("0:0"),
	})

	shapes := []Shape{cross, &polyline, &points, laxPolyline, laxPolygon}
	for i, shape := range shapes {
		index := NewShapeIndex()
		index.Add(shape)
		var buf bytes.Buffer
		if err := index.Encode(&buf); err != nil {
			t.Fatalf("shape %d Encode: %v", i, err)
		}
		decoded := NewShapeIndex()
		if err := decoded.Decode(&buf); err != nil {
			t.Fatalf("shape %d Decode: %v", i, err)
		}
		got := decoded.Shape(0)
		if got.NumEdges() != shape.NumEdges() {
			t.Errorf("shape %d NumEdges = %d, want %d", i, got.NumEdges(), shape.NumEdges())
		}
		if got.NumChains() != shape.NumChains() {
			t.Errorf("shape %d NumChains = %d, want %d", i, got.NumChains(), shape.NumChains())
		}
		if got.Dimension() != shape.Dimension() {
			t.Errorf("shape %d Dimension = %d, want %d", i, got.Dimension(), shape.Dimension())
		}
		quadraticValidate(t, decoded)
	}
}

func TestShapeIndexEncodeZeroEdgeShapes(t *testing.T) {
	emptyPolyline := Polyline{}
	points := PointVector{PointFromCoords(1, 0, 0)}
	fullPolygon := FullPolygon()

	for _, shape := range []Shape{&emptyPolyline, &points, fullPolygon} {
		index := NewShapeIndex()
		index.Add(shape)
		var buf bytes.Buffer
		if err := index.Encode(&buf); err != nil {
			t.Fatalf("Encode(%T): %v", shape, err)
		}
		decoded := NewShapeIndex()
		if err := decoded.Decode(&buf); err != nil {
			t.Fatalf("Decode(%T): %v", shape, err)
		}
		got := decoded.Shape(0)
		if got.NumEdges() != shape.NumEdges() {
			t.Errorf("%T NumEdges = %d, want %d", shape, got.NumEdges(), shape.NumEdges())
		}
	}
}

func TestShapeIndexDecodeErrors(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"truncated", []byte{0x01}},
		{"garbage", []byte{0xff, 0xff, 0xff, 0xff, 0xff}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			index := NewShapeIndex()
			if err := index.Decode(bytes.NewReader(test.data)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestShapeIndexDecodeOversizedAllocation(t *testing.T) {
	// Craft a tagged shape vector claiming a huge number of entries.
	var buf bytes.Buffer
	e := &encoder{w: &buf}
	// String vector with one offset pointing past available data.
	e.writeUvarint(uint64(maxEncodedUintVectorSize*8 + 7))
	index := NewShapeIndex()
	if err := index.Decode(&buf); err == nil {
		t.Fatal("expected error for oversized allocation")
	}
}
