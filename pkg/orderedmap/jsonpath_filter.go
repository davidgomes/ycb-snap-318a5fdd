// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package orderedmap

import (
	"cmp"
	"math"
	"strings"
)

// filterExpr is an expression within a JSONPath filter (`[?(...)]`).
type filterExpr interface {
	// evaluate computes the expression for the node being filtered. The
	// boolean result is false when the expression yields nothing at all
	// (e.g. a query for a missing key), which is distinct from yielding null.
	evaluate(current, root interface{}) (interface{}, bool)
}

type literalExpr struct {
	value interface{}
}

func (e literalExpr) evaluate(_, _ interface{}) (interface{}, bool) {
	return e.value, true
}

// queryExpr is a query that selects at most one value, relative to either the
// node being filtered (`@`) or the document root (`$`).
type queryExpr struct {
	relative bool
	path     jsonPath
}

func (e queryExpr) evaluate(current, root interface{}) (interface{}, bool) {
	start := root
	if e.relative {
		start = current
	}
	results := e.path.evaluate(start, root)
	if len(results) != 1 {
		return nil, false
	}
	return results[0], true
}

type lengthCallExpr struct {
	arg filterExpr
}

func (e lengthCallExpr) evaluate(current, root interface{}) (interface{}, bool) {
	value, exists := e.arg.evaluate(current, root)
	if !exists {
		return nil, false
	}
	length, hasLength := lengthOf(value)
	if !hasLength {
		return nil, false
	}
	return length, true
}

type notExpr struct {
	operand filterExpr
}

func (e notExpr) evaluate(current, root interface{}) (interface{}, bool) {
	return !isTruthy(e.operand.evaluate(current, root)), true
}

type logicalExpr struct {
	isAnd       bool
	left, right filterExpr
}

func (e logicalExpr) evaluate(current, root interface{}) (interface{}, bool) {
	leftTruthy := isTruthy(e.left.evaluate(current, root))
	if leftTruthy != e.isAnd {
		// short-circuit: false for `&&`, true for `||`
		return leftTruthy, true
	}
	return isTruthy(e.right.evaluate(current, root)), true
}

type comparisonOp string

const (
	opEqual          comparisonOp = "=="
	opNotEqual       comparisonOp = "!="
	opLessOrEqual    comparisonOp = "<="
	opGreaterOrEqual comparisonOp = ">="
	opLess           comparisonOp = "<"
	opGreater        comparisonOp = ">"
)

// comparisonOps is ordered so that two-character operators are matched
// before their one-character prefixes.
var comparisonOps = []comparisonOp{opEqual, opNotEqual, opLessOrEqual, opGreaterOrEqual, opLess, opGreater}

type comparisonExpr struct {
	op          comparisonOp
	left, right filterExpr
}

func (e comparisonExpr) evaluate(current, root interface{}) (interface{}, bool) {
	left, leftExists := e.left.evaluate(current, root)
	right, rightExists := e.right.evaluate(current, root)

	equal := leftExists == rightExists && (!leftExists || valuesEqual(left, right))
	ordering, ordered := 0, false
	if leftExists && rightExists {
		ordering, ordered = orderValues(left, right)
	}

	switch e.op {
	case opEqual:
		return equal, true
	case opNotEqual:
		return !equal, true
	case opLess:
		return ordered && ordering < 0, true
	case opGreater:
		return ordered && ordering > 0, true
	case opLessOrEqual:
		return equal || (ordered && ordering < 0), true
	case opGreaterOrEqual:
		return equal || (ordered && ordering > 0), true
	}
	return false, true
}

func isTruthy(value interface{}, exists bool) bool {
	return exists && isTruthyValue(value)
}

// isTruthyValue treats nil, false, zero, "" and empty arrays and maps as
// false; everything else is true.
func isTruthyValue(value interface{}) bool {
	switch typedValue := value.(type) {
	case nil:
		return false
	case bool:
		return typedValue
	case string:
		return typedValue != ""
	case []interface{}:
		return len(typedValue) > 0
	}
	if entries, isMap := mapEntries(value); isMap {
		return len(entries) > 0
	}
	if number, isNumber := toFloat64(value); isNumber {
		return number != 0
	}
	return true
}

func valuesEqual(a, b interface{}) bool {
	if _, isNumber := toFloat64(a); isNumber {
		ordering, comparable := compareNumbers(a, b)
		return comparable && ordering == 0
	}

	switch typedA := a.(type) {
	case nil:
		return b == nil
	case bool:
		typedB, isBool := b.(bool)
		return isBool && typedA == typedB
	case string:
		typedB, isString := b.(string)
		return isString && typedA == typedB
	case []interface{}:
		typedB, isArray := b.([]interface{})
		return isArray && arraysEqual(typedA, typedB)
	}

	entriesA, isMapA := mapEntries(a)
	entriesB, isMapB := mapEntries(b)
	return isMapA && isMapB && mapsEqual(entriesA, entriesB)
}

func arraysEqual(a, b []interface{}) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !valuesEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func mapsEqual(a, b []MapItem) bool {
	if len(a) != len(b) {
		return false
	}
	other := NewMapWithItems(b)
	for _, entry := range a {
		value, found := other.Get(entry.Key)
		if !found || !valuesEqual(entry.Value, value) {
			return false
		}
	}
	return true
}

// orderValues compares two numbers or two strings. The boolean result is
// false for any other combination of values, which have no ordering.
func orderValues(a, b interface{}) (int, bool) {
	if ordering, comparable := compareNumbers(a, b); comparable {
		return ordering, true
	}
	stringA, isStringA := a.(string)
	stringB, isStringB := b.(string)
	if isStringA && isStringB {
		return strings.Compare(stringA, stringB), true
	}
	return 0, false
}

func compareNumbers(a, b interface{}) (int, bool) {
	intA, isIntA := toInt64(a)
	intB, isIntB := toInt64(b)
	if isIntA && isIntB {
		return cmp.Compare(intA, intB), true
	}
	floatA, isNumberA := toFloat64(a)
	floatB, isNumberB := toFloat64(b)
	if !isNumberA || !isNumberB || math.IsNaN(floatA) || math.IsNaN(floatB) {
		return 0, false
	}
	return cmp.Compare(floatA, floatB), true
}

func toInt64(value interface{}) (int64, bool) {
	switch typedValue := value.(type) {
	case int:
		return int64(typedValue), true
	case int8:
		return int64(typedValue), true
	case int16:
		return int64(typedValue), true
	case int32:
		return int64(typedValue), true
	case int64:
		return typedValue, true
	case uint8:
		return int64(typedValue), true
	case uint16:
		return int64(typedValue), true
	case uint32:
		return int64(typedValue), true
	case uint:
		return int64(typedValue), uint64(typedValue) <= math.MaxInt64
	case uint64:
		return int64(typedValue), typedValue <= math.MaxInt64
	}
	return 0, false
}

func toFloat64(value interface{}) (float64, bool) {
	if intValue, isInt := toInt64(value); isInt {
		return float64(intValue), true
	}
	switch typedValue := value.(type) {
	case float64:
		return typedValue, true
	case float32:
		return float64(typedValue), true
	case uint:
		return float64(typedValue), true
	case uint64:
		return float64(typedValue), true
	}
	return 0, false
}
