package s2

import (
	"fmt"
	"io"

	"github.com/golang/geo/r3"
)

const (
	shapeIndexEncodingMagic   uint32 = 0x53495831 // "SIX1"
	shapeIndexEncodingVersion uint8  = 1

	// These limits are deliberately conservative. Besides documenting the
	// format limits, they prevent malformed input from causing large
	// allocations.
	maxEncodedShapeIndexItems = 10000000
	maxEncodedShapeIndexEdges = 50000000
)

// serializedShape is the package's lossless, representation-independent Shape.
// Keeping the edge and chain data here means that decoding does not need to
// rebuild the index, and also allows every built-in Shape to be encoded.
type serializedShape struct {
	dimension int
	reference ReferencePoint
	edges     []Edge
	chains    []Chain
}

func (s *serializedShape) NumEdges() int                  { return len(s.edges) }
func (s *serializedShape) Edge(i int) Edge                { return s.edges[i] }
func (s *serializedShape) ReferencePoint() ReferencePoint { return s.reference }
func (s *serializedShape) NumChains() int                 { return len(s.chains) }
func (s *serializedShape) Chain(i int) Chain              { return s.chains[i] }
func (s *serializedShape) ChainEdge(i, j int) Edge {
	return s.edges[s.chains[i].Start+j]
}
func (s *serializedShape) ChainPosition(edgeID int) ChainPosition {
	for i, c := range s.chains {
		if edgeID < c.Start+c.Length {
			return ChainPosition{i, edgeID - c.Start}
		}
	}
	return ChainPosition{}
}
func (s *serializedShape) Dimension() int    { return s.dimension }
func (s *serializedShape) IsEmpty() bool     { return defaultShapeIsEmpty(s) }
func (s *serializedShape) IsFull() bool      { return defaultShapeIsFull(s) }
func (s *serializedShape) typeTag() typeTag  { return typeTagNone }
func (s *serializedShape) privateInterface() {}

func encodeShapeIndexPoint(e *encoder, p Point) {
	e.writeFloat64(p.X)
	e.writeFloat64(p.Y)
	e.writeFloat64(p.Z)
}

func readPoint(d *decoder) Point {
	return Point{Vector: r3.Vector{X: d.readFloat64(), Y: d.readFloat64(), Z: d.readFloat64()}}
}

// Encode writes a complete snapshot of the ShapeIndex, including its spatial
// cells. Pending additions are applied so an index can be encoded before an
// explicit Build call.
func (s *ShapeIndex) Encode(w io.Writer) error {
	s.Build()
	s.mu.RLock()
	defer s.mu.RUnlock()

	e := &encoder{w: w}
	e.writeUint32(shapeIndexEncodingMagic)
	e.writeUint8(shapeIndexEncodingVersion)
	e.writeInt32(int32(s.maxEdgesPerCell))
	e.writeUint32(uint32(s.nextID))
	for id := int32(0); id < s.nextID; id++ {
		shape, ok := s.shapes[id]
		e.writeBool(ok)
		if !ok {
			continue
		}
		e.writeUint8(uint8(shape.Dimension()))
		encodeShapeIndexPoint(e, shape.ReferencePoint().Point)
		e.writeBool(shape.ReferencePoint().Contained)
		e.writeUvarint(uint64(shape.NumEdges()))
		for edge := 0; edge < shape.NumEdges(); edge++ {
			edge := shape.Edge(edge)
			encodeShapeIndexPoint(e, edge.V0)
			encodeShapeIndexPoint(e, edge.V1)
		}
		e.writeUvarint(uint64(shape.NumChains()))
		for chain := 0; chain < shape.NumChains(); chain++ {
			c := shape.Chain(chain)
			e.writeUvarint(uint64(c.Start))
			e.writeUvarint(uint64(c.Length))
		}
	}

	e.writeUvarint(uint64(len(s.cells)))
	for _, cellID := range s.cells {
		e.writeUint64(uint64(cellID))
		cell := s.cellMap[cellID]
		e.writeUvarint(uint64(len(cell.shapes)))
		for _, clipped := range cell.shapes {
			e.writeUint32(uint32(clipped.shapeID))
			e.writeBool(clipped.containsCenter)
			e.writeUvarint(uint64(len(clipped.edges)))
			for _, edgeID := range clipped.edges {
				e.writeUvarint(uint64(edgeID))
			}
		}
	}
	return e.err
}

func encodedCount(d *decoder, name string, max uint64) int {
	n := d.readUvarint()
	if d.err != nil {
		return 0
	}
	if n > max {
		d.err = fmt.Errorf("too many %s (%d; max is %d)", name, n, max)
		return 0
	}
	return int(n)
}

// Decode restores both the shapes and the already-built spatial cell
// structure. The receiver is changed only after the complete input is valid.
func (s *ShapeIndex) Decode(r io.Reader) error {
	d := &decoder{r: asByteReader(r)}
	if d.readUint32() != shapeIndexEncodingMagic {
		if d.err != nil {
			return d.err
		}
		return fmt.Errorf("invalid ShapeIndex encoding")
	}
	if version := d.readUint8(); d.err == nil && version != shapeIndexEncodingVersion {
		return fmt.Errorf("unsupported ShapeIndex encoding version %d", version)
	}
	maxEdges := int32(d.readUint32())
	nextID := d.readUint32()
	if d.err != nil {
		return d.err
	}
	if nextID > maxEncodedShapeIndexItems || maxEdges < 0 {
		return fmt.Errorf("invalid ShapeIndex sizes")
	}

	tmp := NewShapeIndex()
	tmp.maxEdgesPerCell = int(maxEdges)
	tmp.nextID = int32(nextID)
	tmp.shapes = make(map[int32]Shape, nextID)
	for id := uint32(0); id < nextID; id++ {
		present := d.readBool()
		if d.err != nil {
			return d.err
		}
		if !present {
			continue
		}
		dimension := int(d.readUint8())
		if d.err != nil {
			return d.err
		}
		if dimension > 2 {
			return fmt.Errorf("invalid shape dimension %d", dimension)
		}
		shape := &serializedShape{
			dimension: dimension,
			reference: ReferencePoint{Point: readPoint(d), Contained: d.readBool()},
		}
		edgeCount := encodedCount(d, "shape edges", maxEncodedShapeIndexEdges)
		shape.edges = make([]Edge, edgeCount)
		for i := range shape.edges {
			shape.edges[i] = Edge{V0: readPoint(d), V1: readPoint(d)}
		}
		chainCount := encodedCount(d, "shape chains", maxEncodedShapeIndexItems)
		shape.chains = make([]Chain, chainCount)
		nextEdge := 0
		for i := range shape.chains {
			start := encodedCount(d, "chain start", maxEncodedShapeIndexEdges)
			length := encodedCount(d, "chain length", maxEncodedShapeIndexEdges)
			shape.chains[i] = Chain{Start: start, Length: length}
			if start != nextEdge || start > edgeCount || length > edgeCount-start {
				return fmt.Errorf("invalid chain %d", i)
			}
			nextEdge = start + length
		}
		if nextEdge != edgeCount {
			return fmt.Errorf("shape chains do not cover all edges")
		}
		if d.err != nil {
			return d.err
		}
		tmp.shapes[int32(id)] = shape
	}

	cellCount := encodedCount(d, "index cells", maxEncodedShapeIndexItems)
	tmp.cells = make([]CellID, cellCount)
	tmp.cellMap = make(map[CellID]*ShapeIndexCell, cellCount)
	for i := range tmp.cells {
		cellID := CellID(d.readUint64())
		if d.err != nil {
			return d.err
		}
		if !cellID.IsValid() || (i > 0 && cellID <= tmp.cells[i-1]) {
			return fmt.Errorf("invalid or unordered cell ID")
		}
		tmp.cells[i] = cellID
		shapeCount := encodedCount(d, "clipped shapes", maxEncodedShapeIndexItems)
		cell := NewShapeIndexCell(shapeCount)
		for j := range cell.shapes {
			shapeID := int32(d.readUint32())
			contains := d.readBool()
			edgeCount := encodedCount(d, "clipped edges", maxEncodedShapeIndexEdges)
			if shapeID < 0 || uint32(shapeID) >= nextID || tmp.shapes[shapeID] == nil {
				return fmt.Errorf("invalid clipped shape ID %d", shapeID)
			}
			clipped := newClippedShape(shapeID, edgeCount)
			clipped.containsCenter = contains
			for k := range clipped.edges {
				clipped.edges[k] = encodedCount(d, "edge ID", maxEncodedShapeIndexEdges)
				if clipped.edges[k] >= tmp.shapes[shapeID].NumEdges() {
					return fmt.Errorf("invalid edge ID %d", clipped.edges[k])
				}
				if k > 0 && clipped.edges[k] <= clipped.edges[k-1] {
					return fmt.Errorf("unordered clipped edge IDs")
				}
			}
			cell.shapes[j] = clipped
		}
		tmp.cellMap[cellID] = cell
	}
	if d.err != nil {
		return d.err
	}
	tmp.pendingAdditionsPos = tmp.nextID
	tmp.status = fresh
	*s = *tmp
	return nil
}
