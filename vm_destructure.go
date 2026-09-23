package tengo

import "fmt"

func destructureIndex(left, index Object, wantMap bool) (Object, error) {
	if left == missingValue || left == UndefinedValue {
		return missingValue, nil
	}
	if !wantMap {
		var vals []Object
		switch left := left.(type) {
		case *Array:
			vals = left.Value
		case *ImmutableArray:
			vals = left.Value
		default:
			return nil, fmt.Errorf("not an array: %s", left.TypeName())
		}
		intIdx, ok := index.(*Int)
		if !ok {
			return nil, fmt.Errorf("invalid index type: %s", index.TypeName())
		}
		idx := int(intIdx.Value)
		if idx < 0 || idx >= len(vals) {
			return missingValue, nil
		}
		if vals[idx] == nil {
			return UndefinedValue, nil
		}
		return vals[idx], nil
	}

	var m map[string]Object
	switch left := left.(type) {
	case *Map:
		m = left.Value
	case *ImmutableMap:
		m = left.Value
	default:
		return nil, fmt.Errorf("not a map: %s", left.TypeName())
	}
	key, ok := index.(*String)
	if !ok {
		return nil, fmt.Errorf("invalid index type: %s", index.TypeName())
	}
	if m == nil {
		return missingValue, nil
	}
	v, ok := m[key.Value]
	if !ok {
		return missingValue, nil
	}
	if v == nil {
		return UndefinedValue, nil
	}
	return v, nil
}

func destructureTail(src Object, start int) (Object, error) {
	if start < 0 {
		start = 0
	}
	if src == missingValue || src == UndefinedValue {
		return &Array{Value: []Object{}}, nil
	}
	var vals []Object
	switch src := src.(type) {
	case *Array:
		vals = src.Value
	case *ImmutableArray:
		vals = src.Value
	default:
		return nil, fmt.Errorf("not an array: %s", src.TypeName())
	}
	if start >= len(vals) {
		return &Array{Value: []Object{}}, nil
	}
	out := make([]Object, len(vals)-start)
	copy(out, vals[start:])
	return &Array{Value: out}, nil
}
