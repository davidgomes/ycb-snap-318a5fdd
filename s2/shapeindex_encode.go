package s2

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
)

const shapeIndexEncodingVersion uint32 = 1
const maxShapeIndexItems = 1 << 26

type encodedShape struct {
	edges     []Edge
	chains    []Chain
	ref       ReferencePoint
	dimension int
}

func (*encodedShape) typeTag() typeTag                 { return typeTagNone }
func (*encodedShape) privateInterface()                {}
func (s *encodedShape) NumEdges() int                  { return len(s.edges) }
func (s *encodedShape) Edge(i int) Edge                { return s.edges[i] }
func (s *encodedShape) ReferencePoint() ReferencePoint { return s.ref }
func (s *encodedShape) NumChains() int                 { return len(s.chains) }
func (s *encodedShape) Chain(i int) Chain              { return s.chains[i] }
func (s *encodedShape) ChainEdge(c, i int) Edge        { return s.edges[s.chains[c].Start+i] }
func (s *encodedShape) ChainPosition(i int) ChainPosition {
	for c, ch := range s.chains {
		if i < ch.Start+ch.Length {
			return ChainPosition{c, i - ch.Start}
		}
	}
	return ChainPosition{}
}
func (s *encodedShape) Dimension() int { return s.dimension }
func (s *encodedShape) IsEmpty() bool  { return defaultShapeIsEmpty(s) }
func (s *encodedShape) IsFull() bool   { return defaultShapeIsFull(s) }

type shapeIndexEncoder struct{ w io.Writer }

func (e shapeIndexEncoder) u32(v uint32) error  { return binary.Write(e.w, binary.LittleEndian, v) }
func (e shapeIndexEncoder) i32(v int32) error   { return binary.Write(e.w, binary.LittleEndian, v) }
func (e shapeIndexEncoder) f64(v float64) error { return binary.Write(e.w, binary.LittleEndian, v) }
func (e shapeIndexEncoder) point(p Point) error {
	return e.f64(p.X) // Point embeds r3.Vector; write all coordinates explicitly.
}

func (s *ShapeIndex) Encode(w io.Writer) error {
	s.maybeApplyUpdates()
	e := shapeIndexEncoder{w}
	if err := e.u32(shapeIndexEncodingVersion); err != nil {
		return err
	}
	if err := e.i32(s.nextID); err != nil {
		return err
	}
	if s.nextID < 0 || s.nextID > maxShapeIndexItems ||
		len(s.shapes) > maxShapeIndexItems || len(s.cells) > maxShapeIndexItems {
		return errors.New("s2: shape index too large")
	}
	if err := e.u32(uint32(s.nextID)); err != nil {
		return err
	}
	for id := int32(0); id < s.nextID; id++ {
		shape, ok := s.shapes[id]
		if !ok {
			if err := e.u32(0); err != nil {
				return err
			}
			continue
		}
		if err := e.u32(1); err != nil {
			return err
		}
		if err := e.i32(id); err != nil {
			return err
		}
		if err := e.u32(uint32(shape.Dimension())); err != nil {
			return err
		}
		if err := e.u32(uint32(shape.NumChains())); err != nil {
			return err
		}
		if err := e.u32(uint32(shape.NumEdges())); err != nil {
			return err
		}
		if err := e.u32(bool32(shape.ReferencePoint().Contained)); err != nil {
			return err
		}
		r := shape.ReferencePoint().Point
		for _, v := range []float64{r.X, r.Y, r.Z} {
			if err := e.f64(v); err != nil {
				return err
			}
		}
		for c := 0; c < shape.NumChains(); c++ {
			ch := shape.Chain(c)
			if ch.Start < 0 || ch.Length < 0 || (ch.Length == 0 && shape.Dimension() != 2) {
				return errors.New("s2: invalid shape chain")
			}
			if c == 0 && ch.Start != 0 || c > 0 && ch.Start != shape.Chain(c-1).Start+shape.Chain(c-1).Length {
				return errors.New("s2: non-contiguous shape chains")
			}
			if c == shape.NumChains()-1 && ch.Start+ch.Length != shape.NumEdges() {
				return errors.New("s2: shape chains do not cover edges")
			}
			if err := e.u32(uint32(ch.Start)); err != nil {
				return err
			}
			if err := e.u32(uint32(ch.Length)); err != nil {
				return err
			}
		}
		for i := 0; i < shape.NumEdges(); i++ {
			p := shape.Edge(i)
			for _, v := range []float64{p.V0.X, p.V0.Y, p.V0.Z, p.V1.X, p.V1.Y, p.V1.Z} {
				if err := e.f64(v); err != nil {
					return err
				}
			}
		}
	}
	if err := e.u32(uint32(len(s.cells))); err != nil {
		return err
	}
	for _, id := range s.cells {
		if err := binary.Write(w, binary.LittleEndian, uint64(id)); err != nil {
			return err
		}
		cell := s.cellMap[id]
		if len(cell.shapes) > maxShapeIndexItems {
			return errors.New("s2: shape index too large")
		}
		if err := e.u32(uint32(len(cell.shapes))); err != nil {
			return err
		}
		for _, c := range cell.shapes {
			if err := e.i32(c.shapeID); err != nil {
				return err
			}
			if err := e.u32(bool32(c.containsCenter)); err != nil {
				return err
			}
			if err := e.u32(uint32(len(c.edges))); err != nil {
				return err
			}
			for _, edge := range c.edges {
				if edge < 0 {
					return errors.New("s2: invalid edge id")
				}
				if err := e.u32(uint32(edge)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func bool32(v bool) uint32 {
	if v {
		return 1
	}
	return 0
}

type shapeIndexDecoder struct{ r io.Reader }

func (d shapeIndexDecoder) u32() (uint32, error) {
	var v uint32
	err := binary.Read(d.r, binary.LittleEndian, &v)
	return v, err
}
func (d shapeIndexDecoder) i32() (int32, error) {
	var v int32
	err := binary.Read(d.r, binary.LittleEndian, &v)
	return v, err
}
func (d shapeIndexDecoder) f64() (float64, error) {
	var v float64
	err := binary.Read(d.r, binary.LittleEndian, &v)
	return v, err
}
func (s *ShapeIndex) Decode(r io.Reader) error {
	d := shapeIndexDecoder{r}
	version, err := d.u32()
	if err != nil {
		return err
	}
	if version != shapeIndexEncodingVersion {
		return fmt.Errorf("s2: unsupported shape index version %d", version)
	}
	next, err := d.i32()
	if err != nil {
		return err
	}
	ns, err := d.u32()
	if err != nil {
		return err
	}
	if next < 0 || ns > maxShapeIndexItems {
		return errors.New("s2: invalid shape count")
	}
	out := NewShapeIndex()
	out.nextID = next
	for n := uint32(0); n < ns; n++ {
		present, err := d.u32()
		if err != nil {
			return err
		}
		if present == 0 {
			continue
		}
		id, err := d.i32()
		if err != nil {
			return err
		}
		dim, err := d.u32()
		if err != nil {
			return err
		}
		nc, err := d.u32()
		if err != nil {
			return err
		}
		ne, err := d.u32()
		if err != nil {
			return err
		}
		contained, err := d.u32()
		if err != nil {
			return err
		}
		if dim > 2 || nc > maxShapeIndexItems || ne > maxShapeIndexItems {
			return errors.New("s2: invalid shape sizes")
		}
		var xyz [3]float64
		for i := range xyz {
			xyz[i], err = d.f64()
			if err != nil {
				return err
			}
		}
		chains := make([]Chain, nc)
		for i := range chains {
			a, e := d.u32()
			if e != nil {
				return e
			}
			b, e2 := d.u32()
			if e2 != nil {
				return e2
			}
			if uint64(a)+uint64(b) > uint64(ne) || (b == 0 && dim != 2) {
				return errors.New("s2: invalid shape chain")
			}
			if i == 0 && a != 0 || i > 0 && a != uint32(chains[i-1].Start+chains[i-1].Length) {
				return errors.New("s2: non-contiguous shape chains")
			}
			if i == int(nc)-1 && uint64(a)+uint64(b) != uint64(ne) {
				return errors.New("s2: shape chains do not cover edges")
			}
			chains[i] = Chain{int(a), int(b)}
		}
		edges := make([]Edge, ne)
		for i := range edges {
			var v [6]float64
			for j := range v {
				v[j], err = d.f64()
				if err != nil {
					return err
				}
			}
			edges[i] = Edge{PointFromCoords(v[0], v[1], v[2]), PointFromCoords(v[3], v[4], v[5])}
		}
		if id < 0 || id >= next {
			return errors.New("s2: invalid shape id")
		}
		out.shapes[id] = &encodedShape{edges, chains, ReferencePoint{PointFromCoords(xyz[0], xyz[1], xyz[2]), contained != 0}, int(dim)}
	}
	nc, err := d.u32()
	if err != nil {
		return err
	}
	if nc > maxShapeIndexItems {
		return errors.New("s2: invalid cell count")
	}
	out.cells = make([]CellID, nc)
	for i := range out.cells {
		var raw uint64
		if err = binary.Read(r, binary.LittleEndian, &raw); err != nil {
			return err
		}
		out.cells[i] = CellID(raw)
		if !out.cells[i].IsValid() {
			return errors.New("s2: invalid cell id")
		}
		n, err := d.u32()
		if err != nil {
			return err
		}
		if n > maxShapeIndexItems {
			return errors.New("s2: invalid cell shape count")
		}
		cell := NewShapeIndexCell(int(n))
		for j := range cell.shapes {
			id, err := d.i32()
			if err != nil {
				return err
			}
			has, err := d.u32()
			if err != nil {
				return err
			}
			ne, err := d.u32()
			if err != nil {
				return err
			}
			if _, ok := out.shapes[id]; !ok || ne > maxShapeIndexItems {
				return errors.New("s2: invalid clipped shape")
			}
			c := newClippedShape(id, int(ne))
			c.containsCenter = has != 0
			for k := range c.edges {
				v, err := d.u32()
				if err != nil {
					return err
				}
				if v >= uint32(out.shapes[id].NumEdges()) {
					return errors.New("s2: invalid edge reference")
				}
				c.edges[k] = int(v)
			}
			cell.shapes[j] = c
		}
		out.cellMap[out.cells[i]] = cell
	}
	out.pendingAdditionsPos = out.nextID
	atomic.StoreInt32(&out.status, fresh)
	*s = *out
	return nil
}
