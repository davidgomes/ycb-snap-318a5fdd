package s2

import (
	"fmt"
	"io"
	"math"
	"sort"
)

const (
	shapeIndexEncodingMagic   uint32 = 0x53495831 // "SIX1"
	shapeIndexEncodingVersion uint8  = 1

	maxEncodedShapeIndexShapes      = 10000000
	maxEncodedShapeIndexCells       = 50000000
	maxEncodedShapeIndexCellShapes  = 10000000
	maxEncodedShapeIndexCellEdges   = 50000000
	maxEncodedShapeIndexShapeChains = 10000000
)

const (
	shapeIndexShapeLoop uint8 = iota + 1
	shapeIndexShapePolygon
	shapeIndexShapePolyline
	shapeIndexShapePointVector
	shapeIndexShapeLaxPolyline
	shapeIndexShapeLaxLoop
	shapeIndexShapeLaxPolygon
	shapeIndexShapeGeneric
)

const maxEncodedShapeID = uint32(1<<31 - 1)

// Encode encodes the ShapeIndex, including its shapes and spatial cells.
func (s *ShapeIndex) Encode(w io.Writer) error {
	if s == nil {
		return fmt.Errorf("cannot encode a nil ShapeIndex")
	}
	if w == nil {
		return fmt.Errorf("cannot encode ShapeIndex to a nil writer")
	}

	// ShapeIndex updates are lazy. The encoded representation must include the
	// complete cell map even when the caller has not explicitly called Build.
	s.maybeApplyUpdates()

	e := &encoder{w: w}
	e.writeUint32(shapeIndexEncodingMagic)
	e.writeUint8(shapeIndexEncodingVersion)

	if s.maxEdgesPerCell < 0 {
		e.err = fmt.Errorf("invalid max edges per cell: %d", s.maxEdgesPerCell)
		return e.err
	}
	e.writeUint32(uint32(s.maxEdgesPerCell))

	if s.nextID < 0 {
		e.err = fmt.Errorf("invalid next shape ID: %d", s.nextID)
		return e.err
	}
	e.writeUint32(uint32(s.nextID))

	if len(s.shapes) > maxEncodedShapeIndexShapes {
		e.err = fmt.Errorf("too many shapes (%d; max is %d)", len(s.shapes), maxEncodedShapeIndexShapes)
		return e.err
	}
	e.writeUint32(uint32(len(s.shapes)))

	shapeIDs := make([]int32, 0, len(s.shapes))
	for id := range s.shapes {
		shapeIDs = append(shapeIDs, id)
	}
	sort.Slice(shapeIDs, func(i, j int) bool {
		return shapeIDs[i] < shapeIDs[j]
	})
	for _, id := range shapeIDs {
		if id < 0 || id >= s.nextID {
			e.err = fmt.Errorf("invalid shape ID %d", id)
			return e.err
		}
		shape := s.shapes[id]
		if shape == nil {
			e.err = fmt.Errorf("nil shape for shape ID %d", id)
			return e.err
		}
		e.writeUint32(uint32(id))
		encodeShape(e, shape)
		if e.err != nil {
			return e.err
		}
	}

	if len(s.cells) > maxEncodedShapeIndexCells {
		e.err = fmt.Errorf("too many cells (%d; max is %d)", len(s.cells), maxEncodedShapeIndexCells)
		return e.err
	}
	e.writeUint32(uint32(len(s.cells)))
	var previous CellID
	for i, id := range s.cells {
		if !id.IsValid() {
			e.err = fmt.Errorf("invalid cell ID %v", id)
			return e.err
		}
		if i > 0 && id <= previous {
			e.err = fmt.Errorf("cell IDs are not strictly increasing")
			return e.err
		}
		previous = id

		cell, ok := s.cellMap[id]
		if !ok || cell == nil {
			e.err = fmt.Errorf("missing cell map entry for cell ID %v", id)
			return e.err
		}
		encodeIndexCell(e, cell, s.shapes)
		if e.err != nil {
			return e.err
		}
	}
	return e.err
}

// Decode decodes a ShapeIndex from r. The receiver is unchanged if decoding
// fails.
func (s *ShapeIndex) Decode(r io.Reader) (err error) {
	if s == nil {
		return fmt.Errorf("cannot decode into a nil ShapeIndex")
	}
	if r == nil {
		return fmt.Errorf("cannot decode ShapeIndex from a nil reader")
	}

	// A malformed stream must never turn an unchecked internal invariant into a
	// panic visible to callers.
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("invalid ShapeIndex encoding: %v", recovered)
		}
	}()

	d := &decoder{r: asByteReader(r)}
	if got := d.readUint32(); got != shapeIndexEncodingMagic {
		if d.err == nil {
			d.err = fmt.Errorf("invalid ShapeIndex encoding magic 0x%08x", got)
		}
		return d.err
	}
	if version := d.readUint8(); version != shapeIndexEncodingVersion {
		if d.err == nil {
			d.err = fmt.Errorf("unsupported ShapeIndex encoding version %d", version)
		}
		return d.err
	}

	decoded := NewShapeIndex()
	maxEdgesPerCell := d.readUint32()
	if d.err != nil {
		return d.err
	}
	if uint64(maxEdgesPerCell) > uint64(^uint(0)>>1) {
		d.err = fmt.Errorf("max edges per cell is too large: %d", maxEdgesPerCell)
		return d.err
	}
	decoded.maxEdgesPerCell = int(maxEdgesPerCell)

	nextID := d.readUint32()
	if d.err != nil {
		return d.err
	}
	if nextID > maxEncodedShapeID {
		d.err = fmt.Errorf("next shape ID is too large: %d", nextID)
		return d.err
	}
	decoded.nextID = int32(nextID)

	numShapes := d.readUint32()
	if d.err != nil {
		return d.err
	}
	if numShapes > maxEncodedShapeIndexShapes {
		d.err = fmt.Errorf("too many shapes (%d; max is %d)", numShapes, maxEncodedShapeIndexShapes)
		return d.err
	}
	decoded.shapes = make(map[int32]Shape, int(numShapes))

	var previousShapeID int32 = -1
	for range numShapes {
		rawID := d.readUint32()
		if d.err != nil {
			return d.err
		}
		if rawID > maxEncodedShapeID {
			d.err = fmt.Errorf("shape ID is too large: %d", rawID)
			return d.err
		}
		id := int32(rawID)
		if id <= previousShapeID {
			d.err = fmt.Errorf("shape IDs are not strictly increasing")
			return d.err
		}
		if id >= decoded.nextID {
			d.err = fmt.Errorf("shape ID %d is not below next shape ID %d", id, decoded.nextID)
			return d.err
		}
		shapeTag := d.readUint8()
		shape := decodeShape(d, shapeTag)
		if d.err != nil {
			return d.err
		}
		decoded.shapes[id] = shape
		previousShapeID = id
	}

	numCells := d.readUint32()
	if d.err != nil {
		return d.err
	}
	if numCells > maxEncodedShapeIndexCells {
		d.err = fmt.Errorf("too many cells (%d; max is %d)", numCells, maxEncodedShapeIndexCells)
		return d.err
	}
	decoded.cellMap = make(map[CellID]*ShapeIndexCell, int(numCells))
	decoded.cells = make([]CellID, 0, int(numCells))

	var previousCellID CellID
	for i := uint32(0); i < numCells; i++ {
		id := CellID(d.readUint64())
		if d.err != nil {
			return d.err
		}
		if !id.IsValid() {
			d.err = fmt.Errorf("invalid cell ID %v", id)
			return d.err
		}
		if i > 0 && id <= previousCellID {
			d.err = fmt.Errorf("cell IDs are not strictly increasing")
			return d.err
		}
		previousCellID = id

		cell := decodeIndexCell(d, decoded.shapes)
		if d.err != nil {
			return d.err
		}
		decoded.cellMap[id] = cell
		decoded.cells = append(decoded.cells, id)
	}

	// ShapeIndex encodings are self-contained. Reject bytes left over after
	// the final cell rather than silently accepting a concatenated/corrupt
	// stream.
	if _, readErr := d.r.ReadByte(); readErr == nil {
		d.err = fmt.Errorf("trailing data after ShapeIndex encoding")
	} else if readErr != io.EOF {
		d.err = readErr
	}
	if d.err != nil {
		return d.err
	}

	decoded.pendingAdditionsPos = decoded.nextID
	decoded.pendingRemovals = nil

	s.mu.Lock()
	s.shapes = decoded.shapes
	s.maxEdgesPerCell = decoded.maxEdgesPerCell
	s.nextID = decoded.nextID
	s.cellMap = decoded.cellMap
	s.cells = decoded.cells
	s.pendingAdditionsPos = decoded.pendingAdditionsPos
	s.pendingRemovals = nil
	s.status = fresh
	s.mu.Unlock()
	return nil
}

func encodeShape(e *encoder, shape Shape) {
	switch shape := shape.(type) {
	case *Loop:
		if shape == nil {
			e.err = fmt.Errorf("cannot encode a nil Loop")
			return
		}
		e.writeUint8(shapeIndexShapeLoop)
		encodeLoopData(e, shape)
	case *Polygon:
		if shape == nil {
			e.err = fmt.Errorf("cannot encode a nil Polygon")
			return
		}
		e.writeUint8(shapeIndexShapePolygon)
		encodePolygonData(e, shape)
	case *Polyline:
		if shape == nil {
			e.err = fmt.Errorf("cannot encode a nil Polyline")
			return
		}
		e.writeUint8(shapeIndexShapePolyline)
		encodePointSlice(e, []Point(*shape))
	case *PointVector:
		if shape == nil {
			e.err = fmt.Errorf("cannot encode a nil PointVector")
			return
		}
		e.writeUint8(shapeIndexShapePointVector)
		encodePointSlice(e, []Point(*shape))
	case *LaxPolyline:
		if shape == nil {
			e.err = fmt.Errorf("cannot encode a nil LaxPolyline")
			return
		}
		e.writeUint8(shapeIndexShapeLaxPolyline)
		encodePointSlice(e, shape.vertices)
	case *LaxLoop:
		if shape == nil {
			e.err = fmt.Errorf("cannot encode a nil LaxLoop")
			return
		}
		e.writeUint8(shapeIndexShapeLaxLoop)
		encodePointSlice(e, shape.vertices)
	case *LaxPolygon:
		if shape == nil {
			e.err = fmt.Errorf("cannot encode a nil LaxPolygon")
			return
		}
		e.writeUint8(shapeIndexShapeLaxPolygon)
		encodeLaxPolygonData(e, shape)
	default:
		// Shape has a package-private method, so this is only reachable for
		// package-local implementations. Preserve those shapes as a generic
		// edge/chain shape instead of making ShapeIndex serialization unusable
		// for them.
		e.writeUint8(shapeIndexShapeGeneric)
		encodeGenericShapeData(e, shape)
	}
}

func decodeShape(d *decoder, shapeTag uint8) Shape {
	switch shapeTag {
	case shapeIndexShapeLoop:
		return decodeLoopData(d)
	case shapeIndexShapePolygon:
		return decodePolygonData(d)
	case shapeIndexShapePolyline:
		points := decodePointSlice(d)
		if d.err != nil {
			return nil
		}
		p := Polyline(points)
		return &p
	case shapeIndexShapePointVector:
		points := decodePointSlice(d)
		if d.err != nil {
			return nil
		}
		p := PointVector(points)
		return &p
	case shapeIndexShapeLaxPolyline:
		return decodeLaxPolylineData(d)
	case shapeIndexShapeLaxLoop:
		points := decodePointSlice(d)
		if d.err != nil {
			return nil
		}
		return LaxLoopFromPoints(points)
	case shapeIndexShapeLaxPolygon:
		return decodeLaxPolygonData(d)
	case shapeIndexShapeGeneric:
		return decodeGenericShapeData(d)
	default:
		d.err = fmt.Errorf("unknown ShapeIndex shape type %d", shapeTag)
		return nil
	}
}

func encodePointSlice(e *encoder, points []Point) {
	if len(points) > maxEncodedVertices {
		e.err = fmt.Errorf("too many vertices (%d; max is %d)", len(points), maxEncodedVertices)
		return
	}
	e.writeUint32(uint32(len(points)))
	for _, point := range points {
		encodePoint(e, point)
	}
}

func decodePointSlice(d *decoder) []Point {
	numPoints := d.readUint32()
	if d.err != nil {
		return nil
	}
	if numPoints > maxEncodedVertices {
		d.err = fmt.Errorf("too many vertices (%d; max is %d)", numPoints, maxEncodedVertices)
		return nil
	}
	points := make([]Point, int(numPoints))
	for i := range points {
		points[i] = decodePoint(d)
		if d.err != nil {
			return nil
		}
	}
	return points
}

func encodePoint(e *encoder, point Point) {
	e.writeFloat64(point.X)
	e.writeFloat64(point.Y)
	e.writeFloat64(point.Z)
}

func decodePoint(d *decoder) Point {
	var point Point
	point.X = d.readFloat64()
	point.Y = d.readFloat64()
	point.Z = d.readFloat64()
	if d.err == nil && (!finite(point.X) || !finite(point.Y) || !finite(point.Z)) {
		d.err = fmt.Errorf("point contains a non-finite coordinate")
	}
	return point
}

func finite(x float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0)
}

func encodeLoopData(e *encoder, loop *Loop) {
	encodePointSlice(e, loop.vertices)
	if e.err != nil {
		return
	}
	if loop.depth < 0 || uint64(loop.depth) > uint64(maxEncodedShapeID) {
		e.err = fmt.Errorf("invalid loop depth %d", loop.depth)
		return
	}
	e.writeUint8(boolByte(loop.originInside))
	e.writeUint32(uint32(loop.depth))
	loop.bound.encode(e)
}

func decodeLoopData(d *decoder) *Loop {
	points := decodePointSlice(d)
	if d.err != nil {
		return nil
	}
	originInside := decodeBool(d)
	depth := d.readUint32()
	if d.err != nil {
		return nil
	}
	if depth > maxEncodedShapeID {
		d.err = fmt.Errorf("loop depth is too large: %d", depth)
		return nil
	}
	var bound Rect
	bound.decode(d)
	if d.err != nil {
		return nil
	}
	if !validRect(bound) {
		d.err = fmt.Errorf("loop bound contains a non-finite value")
		return nil
	}

	loop := &Loop{
		vertices:       points,
		originInside:   originInside,
		depth:          int(depth),
		bound:          bound,
		subregionBound: ExpandForSubregions(bound),
		index:          NewShapeIndex(),
	}
	loop.index.Add(loop)
	return loop
}

func encodePolygonData(e *encoder, polygon *Polygon) {
	if len(polygon.loops) > maxEncodedLoops {
		e.err = fmt.Errorf("too many loops (%d; max is %d)", len(polygon.loops), maxEncodedLoops)
		return
	}
	e.writeUint32(uint32(len(polygon.loops)))
	for _, loop := range polygon.loops {
		if loop == nil {
			e.err = fmt.Errorf("cannot encode a nil polygon loop")
			return
		}
		encodeLoopData(e, loop)
		if e.err != nil {
			return
		}
	}
}

func decodePolygonData(d *decoder) Shape {
	numLoops := d.readUint32()
	if d.err != nil {
		return nil
	}
	if numLoops > maxEncodedLoops {
		d.err = fmt.Errorf("too many loops (%d; max is %d)", numLoops, maxEncodedLoops)
		return nil
	}
	loops := make([]*Loop, int(numLoops))
	for i := range loops {
		loops[i] = decodeLoopData(d)
		if d.err != nil {
			return nil
		}
	}

	polygon := &Polygon{loops: loops}
	polygon.initLoopProperties()
	return polygon
}

func encodeLaxPolylineData(e *encoder, polyline *LaxPolyline) {
	encodePointSlice(e, polyline.vertices)
}

func decodeLaxPolylineData(d *decoder) Shape {
	points := decodePointSlice(d)
	if d.err != nil {
		return nil
	}
	return LaxPolylineFromPoints(points)
}

func encodeLaxPolygonData(e *encoder, polygon *LaxPolygon) {
	if polygon.numLoops < 0 || polygon.numLoops > maxEncodedLoops {
		e.err = fmt.Errorf("invalid number of loops %d", polygon.numLoops)
		return
	}
	e.writeUint32(uint32(polygon.numLoops))
	totalVertices := 0
	for i := 0; i < polygon.numLoops; i++ {
		numVertices := polygon.numLoopVertices(i)
		if numVertices < 0 || numVertices > maxEncodedVertices {
			e.err = fmt.Errorf("invalid number of vertices in loop %d: %d", i, numVertices)
			return
		}
		if totalVertices > maxEncodedVertices-numVertices {
			e.err = fmt.Errorf("too many vertices (%d; max is %d)", totalVertices+numVertices, maxEncodedVertices)
			return
		}
		totalVertices += numVertices
		e.writeUint32(uint32(numVertices))
		for j := 0; j < numVertices; j++ {
			encodePoint(e, polygon.loopVertex(i, j))
		}
	}
}

func decodeLaxPolygonData(d *decoder) Shape {
	numLoops := d.readUint32()
	if d.err != nil {
		return nil
	}
	if numLoops > maxEncodedLoops {
		d.err = fmt.Errorf("too many loops (%d; max is %d)", numLoops, maxEncodedLoops)
		return nil
	}
	loops := make([][]Point, int(numLoops))
	totalVertices := uint32(0)
	for i := range loops {
		numVertices := d.readUint32()
		if d.err != nil {
			return nil
		}
		if numVertices > maxEncodedVertices || totalVertices > maxEncodedVertices-numVertices {
			d.err = fmt.Errorf("too many vertices in LaxPolygon")
			return nil
		}
		totalVertices += numVertices
		loops[i] = make([]Point, int(numVertices))
		for j := range loops[i] {
			loops[i][j] = decodePoint(d)
			if d.err != nil {
				return nil
			}
		}
	}
	return LaxPolygonFromPoints(loops)
}

func encodeIndexCell(e *encoder, cell *ShapeIndexCell, shapes map[int32]Shape) {
	if len(cell.shapes) > maxEncodedShapeIndexCellShapes {
		e.err = fmt.Errorf("too many shapes in cell (%d; max is %d)", len(cell.shapes), maxEncodedShapeIndexCellShapes)
		return
	}
	e.writeUint32(uint32(len(cell.shapes)))
	var previousShapeID int32 = -1
	totalEdges := 0
	for _, clipped := range cell.shapes {
		if clipped == nil {
			e.err = fmt.Errorf("nil clipped shape")
			return
		}
		if clipped.shapeID <= previousShapeID {
			e.err = fmt.Errorf("clipped shape IDs are not strictly increasing")
			return
		}
		shape, ok := shapes[clipped.shapeID]
		if !ok || shape == nil {
			e.err = fmt.Errorf("cell references missing shape ID %d", clipped.shapeID)
			return
		}
		if len(clipped.edges) > maxEncodedShapeIndexCellEdges ||
			totalEdges > maxEncodedShapeIndexCellEdges-len(clipped.edges) {
			e.err = fmt.Errorf("too many edges in cell")
			return
		}
		totalEdges += len(clipped.edges)
		e.writeUint32(uint32(clipped.shapeID))
		e.writeUint8(boolByte(clipped.containsCenter))
		e.writeUint32(uint32(len(clipped.edges)))
		var previousEdgeID int = -1
		for _, edgeID := range clipped.edges {
			if edgeID < 0 || edgeID >= shape.NumEdges() || edgeID <= previousEdgeID {
				e.err = fmt.Errorf("invalid edge ID %d for shape ID %d", edgeID, clipped.shapeID)
				return
			}
			previousEdgeID = edgeID
			e.writeUint32(uint32(edgeID))
		}
		previousShapeID = clipped.shapeID
	}
}

func decodeIndexCell(d *decoder, shapes map[int32]Shape) *ShapeIndexCell {
	numShapes := d.readUint32()
	if d.err != nil {
		return nil
	}
	if numShapes > maxEncodedShapeIndexCellShapes {
		d.err = fmt.Errorf("too many shapes in cell (%d; max is %d)", numShapes, maxEncodedShapeIndexCellShapes)
		return nil
	}
	cell := &ShapeIndexCell{shapes: make([]*clippedShape, int(numShapes))}
	var previousShapeID int32 = -1
	totalEdges := uint32(0)
	for i := range cell.shapes {
		rawShapeID := d.readUint32()
		if d.err != nil {
			return nil
		}
		if rawShapeID > maxEncodedShapeID {
			d.err = fmt.Errorf("cell shape ID is too large: %d", rawShapeID)
			return nil
		}
		shapeID := int32(rawShapeID)
		if shapeID <= previousShapeID {
			d.err = fmt.Errorf("clipped shape IDs are not strictly increasing")
			return nil
		}
		shape, ok := shapes[shapeID]
		if !ok || shape == nil {
			d.err = fmt.Errorf("cell references missing shape ID %d", shapeID)
			return nil
		}
		containsCenter := decodeBool(d)
		numEdges := d.readUint32()
		if d.err != nil {
			return nil
		}
		if numEdges > maxEncodedShapeIndexCellEdges ||
			totalEdges > maxEncodedShapeIndexCellEdges-numEdges {
			d.err = fmt.Errorf("too many edges in cell")
			return nil
		}
		totalEdges += numEdges
		clipped := &clippedShape{
			shapeID:        shapeID,
			containsCenter: containsCenter,
			edges:          make([]int, int(numEdges)),
		}
		var previousEdgeID int = -1
		for j := range clipped.edges {
			rawEdgeID := d.readUint32()
			if d.err != nil {
				return nil
			}
			if uint64(rawEdgeID) > uint64(^uint(0)>>1) || rawEdgeID >= uint32(shape.NumEdges()) ||
				int(rawEdgeID) <= previousEdgeID {
				d.err = fmt.Errorf("invalid edge ID %d for shape ID %d", rawEdgeID, shapeID)
				return nil
			}
			clipped.edges[j] = int(rawEdgeID)
			previousEdgeID = clipped.edges[j]
		}
		cell.shapes[i] = clipped
		previousShapeID = shapeID
	}
	return cell
}

func boolByte(value bool) uint8 {
	if value {
		return 1
	}
	return 0
}

func decodeBool(d *decoder) bool {
	value := d.readUint8()
	if d.err != nil {
		return false
	}
	if value > 1 {
		d.err = fmt.Errorf("invalid boolean value %d", value)
		return false
	}
	return value == 1
}

func validRect(rect Rect) bool {
	return finite(rect.Lat.Lo) && finite(rect.Lat.Hi) &&
		finite(rect.Lng.Lo) && finite(rect.Lng.Hi)
}

// decodedShape is a fallback for package-local Shape implementations that do
// not have a concrete type known to ShapeIndex serialization.
type decodedShape struct {
	edges       []Edge
	chains      []Chain
	reference   ReferencePoint
	dimension   int
	empty, full bool
}

func (s *decodedShape) NumEdges() int                  { return len(s.edges) }
func (s *decodedShape) Edge(i int) Edge                { return s.edges[i] }
func (s *decodedShape) ReferencePoint() ReferencePoint { return s.reference }
func (s *decodedShape) NumChains() int                 { return len(s.chains) }
func (s *decodedShape) Chain(i int) Chain              { return s.chains[i] }
func (s *decodedShape) ChainEdge(i, j int) Edge        { return s.edges[s.chains[i].Start+j] }
func (s *decodedShape) ChainPosition(edgeID int) ChainPosition {
	for i, chain := range s.chains {
		if edgeID < chain.Start+chain.Length {
			return ChainPosition{ChainID: i, Offset: edgeID - chain.Start}
		}
	}
	return ChainPosition{}
}
func (s *decodedShape) Dimension() int    { return s.dimension }
func (s *decodedShape) IsEmpty() bool     { return s.empty }
func (s *decodedShape) IsFull() bool      { return s.full }
func (s *decodedShape) typeTag() typeTag  { return typeTagNone }
func (s *decodedShape) privateInterface() {}

func encodeGenericShapeData(e *encoder, shape Shape) {
	dimension := shape.Dimension()
	if dimension < 0 || dimension > 2 {
		e.err = fmt.Errorf("invalid shape dimension %d", dimension)
		return
	}
	numEdges := shape.NumEdges()
	numChains := shape.NumChains()
	if numEdges < 0 || numEdges > maxEncodedVertices {
		e.err = fmt.Errorf("invalid number of shape edges %d", numEdges)
		return
	}
	if numChains < 0 || numChains > maxEncodedShapeIndexShapeChains {
		e.err = fmt.Errorf("invalid number of shape chains %d", numChains)
		return
	}
	e.writeUint8(uint8(dimension))
	e.writeUint8(boolByte(shape.IsEmpty()))
	e.writeUint8(boolByte(shape.IsFull()))
	reference := shape.ReferencePoint()
	encodePoint(e, reference.Point)
	e.writeUint8(boolByte(reference.Contained))
	e.writeUint32(uint32(numEdges))
	for i := range numEdges {
		edge := shape.Edge(i)
		encodePoint(e, edge.V0)
		encodePoint(e, edge.V1)
	}
	e.writeUint32(uint32(numChains))
	previousEnd := 0
	for i := range numChains {
		chain := shape.Chain(i)
		if chain.Start != previousEnd || chain.Length <= 0 || chain.Length > numEdges-chain.Start {
			e.err = fmt.Errorf("invalid chain %d", i)
			return
		}
		e.writeUint32(uint32(chain.Start))
		e.writeUint32(uint32(chain.Length))
		previousEnd += chain.Length
	}
	if previousEnd != numEdges {
		e.err = fmt.Errorf("shape chains do not cover all edges")
	}
}

func decodeGenericShapeData(d *decoder) Shape {
	dimension := d.readUint8()
	empty := decodeBool(d)
	full := decodeBool(d)
	referencePoint := decodePoint(d)
	referenceContained := decodeBool(d)
	if d.err != nil {
		return nil
	}
	if dimension > 2 {
		d.err = fmt.Errorf("invalid shape dimension %d", dimension)
		return nil
	}
	numEdges := d.readUint32()
	if d.err != nil {
		return nil
	}
	if numEdges > maxEncodedVertices {
		d.err = fmt.Errorf("too many shape edges (%d; max is %d)", numEdges, maxEncodedVertices)
		return nil
	}
	edges := make([]Edge, int(numEdges))
	for i := range edges {
		edges[i] = Edge{V0: decodePoint(d), V1: decodePoint(d)}
		if d.err != nil {
			return nil
		}
	}
	numChains := d.readUint32()
	if d.err != nil {
		return nil
	}
	if numChains > maxEncodedShapeIndexShapeChains {
		d.err = fmt.Errorf("too many shape chains (%d; max is %d)", numChains, maxEncodedShapeIndexShapeChains)
		return nil
	}
	chains := make([]Chain, int(numChains))
	previousEnd := uint32(0)
	for i := range chains {
		start := d.readUint32()
		length := d.readUint32()
		if d.err != nil {
			return nil
		}
		if length == 0 || start != previousEnd || start > numEdges || length > numEdges-start {
			d.err = fmt.Errorf("invalid chain %d", i)
			return nil
		}
		chains[i] = Chain{Start: int(start), Length: int(length)}
		previousEnd = start + length
	}
	if previousEnd != numEdges {
		d.err = fmt.Errorf("shape chains do not cover all edges")
		return nil
	}
	return &decodedShape{
		edges:     edges,
		chains:    chains,
		reference: ReferencePoint{Point: referencePoint, Contained: referenceContained},
		dimension: int(dimension),
		empty:     empty,
		full:      full,
	}
}
