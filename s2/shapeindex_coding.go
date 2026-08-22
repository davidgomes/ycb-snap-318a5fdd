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
	"io"
	"sync/atomic"
)

const shapeIndexEncodingVersion = 0

const maxClippedEdgesPerShape = 1 << 29

// Encode writes a serialized representation of the index and its shapes to w.
// The spatial cell structure is fully preserved so the decoded index can be
// queried without calling Build.
func (s *ShapeIndex) Encode(w io.Writer) error {
	if err := encodeTaggedShapes(s, w); err != nil {
		return err
	}
	e := &encoder{w: w}
	s.encodeIndex(e)
	return e.err
}

func (s *ShapeIndex) encodeIndex(e *encoder) {
	s.maybeApplyUpdates()
	maxEdgesVersion := uint64(s.maxEdgesPerCell<<2 | shapeIndexEncodingVersion)
	e.writeUvarint(maxEdgesVersion)

	cellIDs := append([]CellID(nil), s.cells...)
	encodeS2CellIDVector(cellIDs, e)

	sve := newStringVectorEncoder()
	for _, id := range cellIDs {
		sub := sve.addViaEncoder()
		encodeShapeIndexCell(s.cellMap[id], int(s.nextID), sub)
		sve.finishSubEncoder(sub)
	}
	sve.encode(e)
}

// Decode reads a serialized ShapeIndex from r.
func (s *ShapeIndex) Decode(r io.Reader) error {
	shapes, nextID, err := decodeTaggedShapes(r)
	if err != nil {
		return err
	}
	d := &decoder{r: asByteReader(r)}
	maxEdgesVersion := d.readUvarint()
	if d.err != nil {
		return d.err
	}
	version := maxEdgesVersion & 3
	if version != shapeIndexEncodingVersion {
		return fmt.Errorf("unsupported ShapeIndex encoding version %d", version)
	}
	maxEdges := int(maxEdgesVersion >> 2)

	cellIDs, err := decodeS2CellIDVector(d)
	if err != nil {
		return err
	}
	cellData, err := decodeStringVector(d)
	if err != nil {
		return err
	}
	if len(cellData) != len(cellIDs) {
		return fmt.Errorf("cell data count mismatch")
	}

	cellMap := make(map[CellID]*ShapeIndexCell, len(cellIDs))
	for i, id := range cellIDs {
		cell, err := decodeShapeIndexCell(int(nextID), bytes.NewReader(cellData[i]))
		if err != nil {
			return err
		}
		cellMap[id] = cell
	}

	s.shapes = shapes
	s.nextID = nextID
	s.maxEdgesPerCell = maxEdges
	s.cellMap = cellMap
	s.cells = cellIDs
	s.pendingAdditionsPos = nextID
	s.pendingRemovals = nil
	atomic.StoreInt32(&s.status, fresh)
	return nil
}

func encodeShapeIndexCell(cell *ShapeIndexCell, numShapeIDs int, e *encoder) {
	if numShapeIDs == 1 {
		if len(cell.shapes) != 1 {
			encodeShapeIndexCellGeneral(cell, numShapeIDs, e)
			return
		}
		clipped := cell.shapes[0]
		if clipped.shapeID != 0 {
			encodeShapeIndexCellGeneral(cell, numShapeIDs, e)
			return
		}
		n := len(clipped.edges)
		if n >= 2 && n <= 17 && clipped.edges[n-1]-clipped.edges[0] == n-1 {
			header := uint64(clipped.edges[0])<<6 | uint64(n-2)<<2
			if clipped.containsCenter {
				header |= 2
			}
			e.writeUvarint(header)
			return
		}
		if n == 1 {
			header := uint64(clipped.edges[0])<<3
			if clipped.containsCenter {
				header |= 4
			}
			header |= 1
			e.writeUvarint(header)
			return
		}
		header := uint64(n)<<3
		if clipped.containsCenter {
			header |= 4
		}
		header |= 3
		e.writeUvarint(header)
		encodeClippedEdges(clipped, e)
		return
	}
	encodeShapeIndexCellGeneral(cell, numShapeIDs, e)
}

func encodeShapeIndexCellGeneral(cell *ShapeIndexCell, numShapeIDs int, e *encoder) {
	if numShapeIDs != 1 && len(cell.shapes) > 1 {
		e.writeUvarint(uint64(len(cell.shapes))<<3 | 3)
	}
	shapeIDBase := int32(0)
	for _, clipped := range cell.shapes {
		if clipped.shapeID < shapeIDBase {
			e.err = fmt.Errorf("clipped shapes out of order")
			return
		}
		shapeDelta := clipped.shapeID - shapeIDBase
		shapeIDBase = clipped.shapeID + 1

		n := len(clipped.edges)
		switch {
		case n >= 1 && n <= 16 && clipped.edges[n-1]-clipped.edges[0] == n-1:
			header := uint64(clipped.edges[0]) << 2
			if clipped.containsCenter {
				header |= 2
			}
			e.writeUvarint(header)
			e.writeUvarint(uint64(shapeDelta)<<4 | uint64(n-1))
		case n == 0:
			header := uint64(shapeDelta) << 4
			if clipped.containsCenter {
				header |= 8
			}
			header |= 7
			e.writeUvarint(header)
		default:
			header := uint64(n-1)<<3 | 1
			if clipped.containsCenter {
				header |= 4
			}
			e.writeUvarint(header)
			e.writeUvarint(uint64(shapeDelta))
			encodeClippedEdges(clipped, e)
		}
	}
}

func encodeClippedEdges(clipped *clippedShape, e *encoder) {
	edgeIDBase := 0
	numEdges := len(clipped.edges)
	for i := 0; i < numEdges; i++ {
		edgeID := clipped.edges[i]
		delta := edgeID - edgeIDBase
		if i+1 == numEdges {
			e.writeUvarint(uint64(delta))
			continue
		}
		count := 1
		for i+1 < numEdges && clipped.edges[i+1] == edgeID+count {
			i++
			count++
		}
		if count < 8 {
			e.writeUvarint(uint64(delta)<<3 | uint64(count-1))
		} else {
			e.writeUvarint(uint64(count-8)<<3 | 7)
			e.writeUvarint(uint64(delta))
		}
		edgeIDBase = edgeID + count
	}
}

func decodeShapeIndexCell(numShapeIDs int, r io.Reader) (*ShapeIndexCell, error) {
	d := &decoder{r: asByteReader(r)}
	if numShapeIDs == 1 {
		header := d.readUvarint()
		if d.err != nil {
			return nil, d.err
		}
		if header&1 == 0 {
			numEdges := int(((header >> 2) & 15) + 2)
			if numEdges > maxClippedEdgesPerShape {
				return nil, fmt.Errorf("too many edges in clipped shape")
			}
			clipped := newClippedShape(0, numEdges)
			clipped.containsCenter = header&2 != 0
			edgeID := int(header >> 6)
			for i := 0; i < numEdges; i++ {
				clipped.edges[i] = edgeID + i
			}
			return &ShapeIndexCell{shapes: []*clippedShape{clipped}}, nil
		}
		if header&2 == 0 {
			clipped := newClippedShape(0, 1)
			clipped.containsCenter = header&4 != 0
			clipped.edges[0] = int(header >> 3)
			return &ShapeIndexCell{shapes: []*clippedShape{clipped}}, nil
		}
		numEdges := int(header >> 3)
		if numEdges > maxClippedEdgesPerShape {
			return nil, fmt.Errorf("too many edges in clipped shape")
		}
		clipped := newClippedShape(0, numEdges)
		clipped.containsCenter = header&4 != 0
		if err := decodeClippedEdges(numEdges, clipped, d); err != nil {
			return nil, err
		}
		return &ShapeIndexCell{shapes: []*clippedShape{clipped}}, nil
	}

	header := d.readUvarint()
	if d.err != nil {
		return nil, d.err
	}
	numClipped := 1
	if header&7 == 3 {
		numClipped = int(header >> 3)
		if numClipped <= 0 || numClipped > maxEncodedShapes {
			return nil, fmt.Errorf("invalid clipped shape count")
		}
		header = d.readUvarint()
		if d.err != nil {
			return nil, d.err
		}
	}

	cell := NewShapeIndexCell(numClipped)
	shapeID := int32(0)
	for j := 0; j < numClipped; j++ {
		if j > 0 {
			header = d.readUvarint()
			if d.err != nil {
				return nil, d.err
			}
		}
		clipped := &clippedShape{}
		if header&1 == 0 {
			shapeIDCount := d.readUvarint()
			if d.err != nil {
				return nil, d.err
			}
			shapeID += int32(shapeIDCount >> 4)
			numEdges := int(shapeIDCount&15) + 1
			if numEdges > maxClippedEdgesPerShape {
				return nil, fmt.Errorf("too many edges in clipped shape")
			}
			clipped.shapeID = shapeID
			clipped.containsCenter = header&2 != 0
			clipped.edges = make([]int, numEdges)
			edgeID := int(header >> 2)
			for i := 0; i < numEdges; i++ {
				clipped.edges[i] = edgeID + i
			}
		} else if header&7 == 7 {
			shapeID += int32(header >> 4)
			clipped.shapeID = shapeID
			clipped.containsCenter = header&8 != 0
			clipped.edges = nil
		} else {
			if header&3 != 1 {
				return nil, fmt.Errorf("corrupted ShapeIndexCell encoding")
			}
			shapeDelta := d.readUvarint()
			if d.err != nil {
				return nil, d.err
			}
			shapeID += int32(shapeDelta)
			numEdges := int(header>>3) + 1
			if numEdges > maxClippedEdgesPerShape {
				return nil, fmt.Errorf("too many edges in clipped shape")
			}
			clipped.shapeID = shapeID
			clipped.containsCenter = header&4 != 0
			clipped.edges = make([]int, numEdges)
			if err := decodeClippedEdges(numEdges, clipped, d); err != nil {
				return nil, err
			}
		}
		cell.shapes[j] = clipped
	}
	return cell, nil
}

func decodeClippedEdges(numEdges int, clipped *clippedShape, d *decoder) error {
	edgeID := 0
	for i := 0; i < numEdges; {
		delta := d.readUvarint()
		if d.err != nil {
			return d.err
		}
		if i+1 == numEdges {
			edgeID += int(delta)
			clipped.edges[i] = edgeID
			i++
			continue
		}
		count := int(delta&7) + 1
		delta >>= 3
		if count == 8 {
			count = int(delta) + 8
			delta = d.readUvarint()
			if d.err != nil {
				return d.err
			}
		}
		if i+count > numEdges {
			return fmt.Errorf("corrupted edge count in ShapeIndexCell")
		}
		edgeID += int(delta)
		for c := 0; c < count; c++ {
			clipped.edges[i] = edgeID
			i++
			edgeID++
		}
	}
	return nil
}
