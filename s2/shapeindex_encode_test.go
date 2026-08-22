package s2

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestShapeIndexEncodeDecode(t *testing.T) {
	loop := LoopFromPoints(parsePoints("0:0, 0:2, 2:0"))
	polygon := PolygonFromLoops([]*Loop{
		LoopFromPoints(parsePoints("10:0, 10:2, 12:0")),
		LoopFromPoints(parsePoints("10:0.5, 10:1, 11:0.5")),
	})
	polyline := Polyline(parsePoints("20:0, 20:1, 21:1"))
	pointVector := PointVector(parsePoints("30:0, 30:1"))
	laxPolyline := LaxPolylineFromPoints(parsePoints("40:0, 40:1, 41:1"))
	laxLoop := LaxLoopFromPoints(parsePoints("50:0, 50:1, 51:0"))
	laxPolygon := LaxPolygonFromPoints([][]Point{
		nil,
		parsePoints("60:0, 60:1, 61:0"),
		{},
		parsePoints("62:0"),
	})
	generic := edgeVectorShapeFromPoints(PointFromCoords(1, 0, 0), PointFromCoords(0, 1, 0))

	index := NewShapeIndex()
	shapes := []Shape{
		loop, polygon, &polyline, &pointVector, laxPolyline, laxLoop, laxPolygon, generic,
	}
	for _, shape := range shapes {
		index.Add(shape)
	}

	var encoded bytes.Buffer
	if err := index.Encode(&encoded); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if encoded.Len() == 0 {
		t.Fatal("Encode produced an empty stream")
	}

	decoded := NewShapeIndex()
	if err := decoded.Decode(bytes.NewReader(encoded.Bytes())); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !decoded.IsFresh() {
		t.Fatal("decoded index is not fresh")
	}
	if got, want := decoded.Len(), len(shapes); got != want {
		t.Fatalf("decoded Len() = %d, want %d", got, want)
	}

	for id, want := range shapes {
		got := decoded.Shape(int32(id))
		if got == nil {
			t.Fatalf("decoded shape %d is nil", id)
		}
		if got.NumEdges() != want.NumEdges() || got.NumChains() != want.NumChains() {
			t.Errorf("shape %d counts = (%d, %d), want (%d, %d)",
				id, got.NumEdges(), got.NumChains(), want.NumEdges(), want.NumChains())
		}
		for edgeID := 0; edgeID < want.NumEdges(); edgeID++ {
			if got.Edge(edgeID) != want.Edge(edgeID) {
				t.Errorf("shape %d edge %d = %v, want %v", id, edgeID, got.Edge(edgeID), want.Edge(edgeID))
			}
		}
		for chainID := 0; chainID < want.NumChains(); chainID++ {
			if got.Chain(chainID) != want.Chain(chainID) {
				t.Errorf("shape %d chain %d = %v, want %v", id, chainID, got.Chain(chainID), want.Chain(chainID))
			}
		}
	}

	quadraticValidate(t, decoded)
	testIteratorMethods(t, decoded)
}

func TestShapeIndexEncodeDecodeEmpty(t *testing.T) {
	index := NewShapeIndex()
	var encoded bytes.Buffer
	if err := index.Encode(&encoded); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if encoded.Len() == 0 {
		t.Fatal("empty index encoded to an empty stream")
	}

	decoded := NewShapeIndex()
	if err := decoded.Decode(&encoded); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.Len() != 0 || !decoded.Iterator().Done() {
		t.Fatalf("decoded empty index is not empty: %#v", decoded)
	}
}

func TestShapeIndexEncodeDecodeZeroEdgeShapes(t *testing.T) {
	emptyPolyline := Polyline{}
	emptyPoints := PointVector{}
	emptyLaxPolyline := LaxPolylineFromPoints(nil)
	emptyLaxLoop := LaxLoopFromPoints(nil)
	emptyLaxPolygon := LaxPolygonFromPoints(nil)
	fullLaxPolygon := LaxPolygonFromPoints([][]Point{nil})
	emptyPolygon := &Polygon{}

	index := NewShapeIndex()
	shapes := []Shape{
		EmptyLoop(), FullLoop(), emptyPolygon, FullPolygon(),
		&emptyPolyline, &emptyPoints, emptyLaxPolyline, emptyLaxLoop,
		emptyLaxPolygon, fullLaxPolygon,
	}
	for _, shape := range shapes {
		index.Add(shape)
	}

	var encoded bytes.Buffer
	if err := index.Encode(&encoded); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded := NewShapeIndex()
	if err := decoded.Decode(&encoded); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	for id, want := range shapes {
		got := decoded.Shape(int32(id))
		if got == nil {
			t.Fatalf("decoded shape %d is nil", id)
		}
		if got.NumEdges() != want.NumEdges() || got.NumChains() != want.NumChains() ||
			got.IsEmpty() != want.IsEmpty() || got.IsFull() != want.IsFull() {
			t.Errorf("shape %d properties did not round trip", id)
		}
	}
	quadraticValidate(t, decoded)
	testIteratorMethods(t, decoded)
}

func TestShapeIndexDecodeRejectsMalformedInput(t *testing.T) {
	index := NewShapeIndex()
	pointVector := PointVector(parsePoints("0:0"))
	index.Add(&pointVector)
	var encoded bytes.Buffer
	if err := index.Encode(&encoded); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	data := encoded.Bytes()

	for length := 0; length < len(data); length++ {
		if err := new(ShapeIndex).Decode(bytes.NewReader(data[:length])); err == nil {
			t.Errorf("Decode accepted truncated input of length %d", length)
		}
	}

	corrupt := bytes.Clone(data)
	corrupt[0] ^= 1
	if err := new(ShapeIndex).Decode(bytes.NewReader(corrupt)); err == nil {
		t.Error("Decode accepted a corrupt header")
	}

	var emptyEncoded bytes.Buffer
	if err := NewShapeIndex().Encode(&emptyEncoded); err != nil {
		t.Fatalf("Encode empty index: %v", err)
	}
	emptyData := bytes.Clone(emptyEncoded.Bytes())
	binary.LittleEndian.PutUint32(emptyData[13:], maxEncodedShapeIndexShapes+1)
	if err := new(ShapeIndex).Decode(bytes.NewReader(emptyData)); err == nil {
		t.Error("Decode accepted an oversized shape allocation request")
	}

	binary.LittleEndian.PutUint32(emptyData[13:], 0)
	binary.LittleEndian.PutUint32(emptyData[17:], maxEncodedShapeIndexCells+1)
	if err := new(ShapeIndex).Decode(bytes.NewReader(emptyData)); err == nil {
		t.Error("Decode accepted an oversized cell allocation request")
	}
}
