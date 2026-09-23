// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package orderedmap

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
)

// SyntaxError is a JSONPath parse error.
type SyntaxError struct {
	// Message describes the syntax error.
	Message string
	// Position is the byte offset of the error in the path.
	Position int
}

// Error formats the syntax error as
// "syntax error at position {Position}: {Message}".
func (e *SyntaxError) Error() string {
	return fmt.Sprintf("syntax error at position %d: %s", e.Position, e.Message)
}

// Query evaluates the JSONPath expression path against doc.
// It returns every match, or an empty slice when nothing matches.
// Syntax errors are returned as *SyntaxError.
func Query(doc any, path string) ([]any, error) {
	segs, err := parsePath(path)
	if err != nil {
		return nil, err
	}

	nodes := []any{doc}
	for _, seg := range segs {
		nodes = applySegment(nodes, seg)
	}
	if len(nodes) == 0 {
		return []any{}, nil
	}
	return nodes, nil
}

// QueryOne evaluates path and returns the first match.
// When nothing matches it returns nil, false, nil.
func QueryOne(doc any, path string) (any, bool, error) {
	nodes, err := Query(doc, path)
	if err != nil {
		return nil, false, err
	}
	if len(nodes) == 0 {
		return nil, false, nil
	}
	return nodes[0], true, nil
}

type segment interface {
	eval(node any) []any
}

type childSegment struct {
	key string
}

type wildSegment struct{}

type indexSegment struct {
	index int
}

type unionPart struct {
	isIndex bool
	key     string
	index   int
}

type unionSegment struct {
	parts []unionPart
}

type filterSegment struct {
	e expr
}

type scriptSegment struct {
	offset int
}

type lengthSegment struct{}

type recursiveSegment struct {
	all   bool
	inner segment
}

type expr interface {
	eval(cur any) any
}

type missingValue struct{}

type litExpr struct {
	val any
}

type pathExpr struct {
	segs []segment
}

type orExpr struct {
	left  expr
	right expr
}

type andExpr struct {
	left  expr
	right expr
}

type cmpExpr struct {
	op    string
	left  expr
	right expr
}

func applySegment(nodes []any, seg segment) []any {
	next := make([]any, 0)
	for _, node := range nodes {
		next = append(next, seg.eval(node)...)
	}
	return next
}

func (s childSegment) eval(node any) []any {
	val, ok := lookupKey(node, s.key)
	if !ok {
		return nil
	}
	return []any{val}
}

func (wildSegment) eval(node any) []any {
	return children(node)
}

func (s indexSegment) eval(node any) []any {
	arr, ok := asSlice(node)
	if !ok {
		return nil
	}
	return indexAt(arr, s.index)
}

func indexAt(arr []any, index int) []any {
	if index < 0 {
		index += len(arr)
	}
	if index < 0 || index >= len(arr) {
		return nil
	}
	return []any{arr[index]}
}

func (s unionSegment) eval(node any) []any {
	var out []any
	for _, part := range s.parts {
		out = append(out, evalUnionPart(node, part)...)
	}
	return out
}

func evalUnionPart(node any, part unionPart) []any {
	if part.isIndex {
		return indexSegment{index: part.index}.eval(node)
	}
	return childSegment{key: part.key}.eval(node)
}

func (s filterSegment) eval(node any) []any {
	arr, ok := asSlice(node)
	if !ok {
		return nil
	}
	var out []any
	for _, el := range arr {
		if isTruthy(s.e.eval(el)) {
			out = append(out, el)
		}
	}
	return out
}

func (s scriptSegment) eval(node any) []any {
	arr, ok := asSlice(node)
	if !ok {
		return nil
	}
	// The script already resolved the index. Do not treat a negative
	// result as a from-the-end index.
	index := len(arr) - s.offset
	if index < 0 || index >= len(arr) {
		return nil
	}
	return []any{arr[index]}
}

func (lengthSegment) eval(node any) []any {
	n, ok := lengthOf(node)
	if !ok {
		return nil
	}
	return []any{n}
}

func (s recursiveSegment) eval(node any) []any {
	if s.all {
		return allNodes(node)
	}
	var out []any
	for _, n := range allNodes(node) {
		out = append(out, s.inner.eval(n)...)
	}
	return out
}

func (e litExpr) eval(any) any { return e.val }

func (e pathExpr) eval(cur any) any {
	nodes := []any{cur}
	for _, seg := range e.segs {
		nodes = applySegment(nodes, seg)
	}
	switch len(nodes) {
	case 0:
		return missingValue{}
	case 1:
		return nodes[0]
	default:
		return nodes
	}
}

func (e orExpr) eval(cur any) any {
	if isTruthy(e.left.eval(cur)) {
		return true
	}
	return isTruthy(e.right.eval(cur))
}

func (e andExpr) eval(cur any) any {
	if !isTruthy(e.left.eval(cur)) {
		return false
	}
	return isTruthy(e.right.eval(cur))
}

func (e cmpExpr) eval(cur any) any {
	return compare(e.op, e.left.eval(cur), e.right.eval(cur))
}

func allNodes(v any) []any {
	out := []any{v}
	for _, child := range children(v) {
		out = append(out, allNodes(child)...)
	}
	return out
}

func children(v any) []any {
	switch typed := v.(type) {
	case *Map:
		return orderedValues(typed)
	case map[string]any:
		return stringMapValues(typed)
	case map[any]any:
		return ifaceMapValues(typed)
	case []any:
		return typed
	default:
		if items, ok := reflectSlice(v); ok {
			return items
		}
		return nil
	}
}

func orderedValues(m *Map) []any {
	if m == nil {
		return nil
	}
	out := make([]any, 0, m.Len())
	m.Iterate(func(_, val any) {
		out = append(out, val)
	})
	return out
}

func stringMapValues(m map[string]any) []any {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(m))
	for _, key := range keys {
		out = append(out, m[key])
	}
	return out
}

func ifaceMapValues(m map[any]any) []any {
	keys := make([]any, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return fmt.Sprint(keys[i]) < fmt.Sprint(keys[j])
	})
	out := make([]any, 0, len(m))
	for _, key := range keys {
		out = append(out, m[key])
	}
	return out
}

func lookupKey(v any, key string) (any, bool) {
	switch typed := v.(type) {
	case *Map:
		if typed == nil {
			return nil, false
		}
		return typed.Get(key)
	case map[string]any:
		val, ok := typed[key]
		return val, ok
	case map[any]any:
		val, ok := typed[key]
		return val, ok
	default:
		return nil, false
	}
}

func asSlice(v any) ([]any, bool) {
	switch typed := v.(type) {
	case []any:
		return typed, true
	default:
		return reflectSlice(v)
	}
}

func reflectSlice(v any) ([]any, bool) {
	if v == nil {
		return nil, false
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return nil, false
	}
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, false
	}
	out := make([]any, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		out[i] = rv.Index(i).Interface()
	}
	return out, true
}

func lengthOf(v any) (int, bool) {
	switch typed := v.(type) {
	case string:
		return len(typed), true
	case *Map:
		if typed == nil {
			return 0, false
		}
		return typed.Len(), true
	case []any:
		return len(typed), true
	case map[string]any:
		return len(typed), true
	case map[any]any:
		return len(typed), true
	case nil:
		return 0, false
	default:
		return reflectLength(v)
	}
}

func reflectLength(v any) (int, bool) {
	if v == nil {
		return 0, false
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.String, reflect.Slice, reflect.Array, reflect.Map:
		return rv.Len(), true
	default:
		return 0, false
	}
}

func isMissing(v any) bool {
	_, ok := v.(missingValue)
	return ok
}

func isTruthy(v any) bool {
	if v == nil || isMissing(v) {
		return false
	}
	if truth, ok := typedTruthy(v); ok {
		return truth
	}
	if number, ok := asFloat(v); ok {
		return number != 0
	}
	return reflectedTruthy(v)
}

func typedTruthy(v any) (truth, ok bool) {
	switch typed := v.(type) {
	case bool:
		return typed, true
	case string:
		return typed != "", true
	case *Map:
		return mapTruthy(typed), true
	case []any:
		return len(typed) > 0, true
	case map[string]any:
		return len(typed) > 0, true
	case map[any]any:
		return len(typed) > 0, true
	default:
		return false, false
	}
}

func mapTruthy(typed *Map) bool {
	return typed != nil && typed.Len() > 0
}

func reflectedTruthy(v any) bool {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return rv.Len() > 0
	case reflect.Ptr, reflect.Interface:
		return !rv.IsNil()
	default:
		return true
	}
}

func compare(op string, left, right any) bool {
	if isMissing(left) || isMissing(right) {
		return false
	}
	switch op {
	case "==":
		return valuesEqual(left, right)
	case "!=":
		return !valuesEqual(left, right)
	case "<", ">", "<=", ">=":
		return compareOrdered(op, left, right)
	default:
		return false
	}
}

func compareOrdered(op string, left, right any) bool {
	cmp, ok := cmpOrdered(left, right)
	if !ok {
		return false
	}
	switch op {
	case "<":
		return cmp < 0
	case ">":
		return cmp > 0
	case "<=":
		return cmp <= 0
	case ">=":
		return cmp >= 0
	default:
		return false
	}
}

func valuesEqual(left, right any) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	if eq, ok := equalNumbers(left, right); ok {
		return eq
	}
	return reflect.DeepEqual(left, right)
}

func equalNumbers(left, right any) (eq, ok bool) {
	if eq, ok := equalInts(left, right); ok {
		return eq, true
	}
	return equalFloats(left, right)
}

func equalInts(left, right any) (eq, ok bool) {
	li, lok := asInt64(left)
	ri, rok := asInt64(right)
	if !lok || !rok {
		return false, false
	}
	return li == ri, true
}

func equalFloats(left, right any) (eq, ok bool) {
	lf, lok := asFloat(left)
	rf, rok := asFloat(right)
	if !lok || !rok {
		return false, false
	}
	return lf == rf, true
}

func cmpOrdered(left, right any) (int, bool) {
	if cmp, ok := cmpInts(left, right); ok {
		return cmp, true
	}
	if cmp, ok := cmpFloats(left, right); ok {
		return cmp, true
	}
	return cmpStrings(left, right)
}

func cmpInts(left, right any) (int, bool) {
	li, lok := asInt64(left)
	ri, rok := asInt64(right)
	if !lok || !rok {
		return 0, false
	}
	return cmpInt64(li, ri), true
}

func cmpFloats(left, right any) (int, bool) {
	lf, lok := asFloat(left)
	rf, rok := asFloat(right)
	if !lok || !rok {
		return 0, false
	}
	return cmpFloat(lf, rf), true
}

func cmpStrings(left, right any) (int, bool) {
	ls, lok := left.(string)
	rs, rok := right.(string)
	if !lok || !rok {
		return 0, false
	}
	return cmpString(ls, rs), true
}

func cmpInt64(left, right int64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func cmpFloat(left, right float64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func cmpString(left, right string) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func asInt64(v any) (int64, bool) {
	if n, ok := signedInt64(v); ok {
		return n, true
	}
	if n, ok := unsignedInt64(v); ok {
		return n, true
	}
	return jsonInt64(v)
}

func signedInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int8:
		return int64(n), true
	case int16:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	default:
		return 0, false
	}
}

func unsignedInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case uint8:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint:
		return uintToInt64(uint64(n))
	case uint64:
		return uintToInt64(n)
	default:
		return 0, false
	}
}

func uintToInt64(n uint64) (int64, bool) {
	if n > math.MaxInt64 {
		return 0, false
	}
	return int64(n), true
}

func jsonInt64(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := n.Int64()
	return i, err == nil
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float32:
		return float64(n), true
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		if i, ok := asInt64(v); ok {
			return float64(i), true
		}
		return 0, false
	}
}
