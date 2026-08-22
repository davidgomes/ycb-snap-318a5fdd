package evaluator

import (
	"github.com/abs-lang/abs/object"
	"github.com/abs-lang/abs/token"
)

func normalizeSliceIndex(idx int, length int) int {
	if idx < 0 {
		idx = length + idx
	}
	if idx < 0 {
		return 0
	}
	if idx > length {
		return length
	}
	return idx
}

func numericRangeValue(obj object.Object, tok token.Token) (int, object.Object) {
	num, ok := obj.(*object.Number)
	if !ok {
		return 0, newError(tok, `index ranges can only be numerical: got "%s" (type %s)`, obj.Inspect(), obj.Type())
	}
	return num.Int(), nil
}

func resolveStep(nodeStep object.Object, tok token.Token) (int, object.Object) {
	if nodeStep == NULL {
		return 1, nil
	}
	step, err := numericRangeValue(nodeStep, tok)
	if err != nil {
		return 0, err
	}
	if step == 0 {
		return 0, newError(tok, "slice step cannot be 0")
	}
	return step, nil
}

func computeSteppedIndices(length int, index, end, step object.Object, startOmitted bool, tok token.Token) ([]int, object.Object) {
	stepVal, errObj := resolveStep(step, tok)
	if errObj != nil {
		return nil, errObj
	}

	var indices []int

	if stepVal > 0 {
		start := 0
		if !startOmitted {
			start = normalizeSliceIndex(index.(*object.Number).Int(), length)
		}

		stop := length
		if end != NULL {
			var err object.Object
			stop, err = numericRangeValue(end, tok)
			if err != nil {
				return nil, err
			}
			stop = normalizeSliceIndex(stop, length)
		}

		for i := start; i < stop; i += stepVal {
			if i >= 0 && i < length {
				indices = append(indices, i)
			}
		}
		return indices, nil
	}

	start := length - 1
	if !startOmitted {
		start = normalizeSliceIndex(index.(*object.Number).Int(), length)
	}

	stop := -(length + 1)
	if end != NULL {
		var err object.Object
		stop, err = numericRangeValue(end, tok)
		if err != nil {
			return nil, err
		}
		stop = normalizeSliceIndex(stop, length)
	}

	for i := start; i > stop; i += stepVal {
		if i >= 0 && i < length {
			indices = append(indices, i)
		}
	}
	return indices, nil
}

func runeCount(value string) int {
	return len([]rune(value))
}
