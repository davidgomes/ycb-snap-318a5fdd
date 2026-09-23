// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package orderedmap

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SyntaxError is a JSONPath parse failure.
// Position is the byte offset into the path where parsing stopped.
type SyntaxError struct {
	Message  string
	Position int
}

// Error formats the failure as "syntax error at position {Position}: {Message}".
func (e *SyntaxError) Error() string {
	return fmt.Sprintf("syntax error at position %d: %s", e.Position, e.Message)
}

// Query evaluates path against doc and returns every match.
// An expression that selects nothing returns an empty slice.
// Selectors that do not apply to a value (for example an index on a map)
// contribute nothing and are not errors.
func Query(doc interface{}, path string) ([]interface{}, error) {
	segs, err := parseJSONPath(path)
	if err != nil {
		return nil, err
	}
	nodes := []interface{}{doc}
	for _, seg := range segs {
		next := make([]interface{}, 0)
		for _, n := range nodes {
			next = append(next, seg.eval(n)...)
		}
		nodes = next
	}
	return nodes, nil
}

// QueryOne evaluates path and returns the first match.
// When nothing matches it returns nil, false, nil.
func QueryOne(doc interface{}, path string) (interface{}, bool, error) {
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
	eval(node interface{}) []interface{}
}

type keySeg struct{ key string }

func (s keySeg) eval(node interface{}) []interface{} {
	kvs, ok := keyedChildren(node)
	if !ok {
		return nil
	}
	for _, kv := range kvs {
		if kv.hasKey && kv.key == s.key {
			return []interface{}{kv.value}
		}
	}
	return nil
}

type indexSeg struct{ idx int }

func (s indexSeg) eval(node interface{}) []interface{} {
	arr, ok := asSlice(node)
	if !ok {
		return nil
	}
	i, ok := normalizeIndex(s.idx, len(arr))
	if !ok {
		return nil
	}
	return []interface{}{arr[i]}
}

type wildcardSeg struct{}

func (wildcardSeg) eval(node interface{}) []interface{} {
	if kvs, ok := keyedChildren(node); ok {
		out := make([]interface{}, 0, len(kvs))
		for _, kv := range kvs {
			out = append(out, kv.value)
		}
		return out
	}
	if arr, ok := asSlice(node); ok {
		if arr == nil {
			return []interface{}{}
		}
		return arr
	}
	return nil
}

type unionSeg struct{ parts []segment }

func (s unionSeg) eval(node interface{}) []interface{} {
	out := make([]interface{}, 0)
	for _, part := range s.parts {
		out = append(out, part.eval(node)...)
	}
	return out
}

type lengthSeg struct{}

func (lengthSeg) eval(node interface{}) []interface{} {
	n, ok := lengthOf(node)
	if !ok {
		return nil
	}
	return []interface{}{n}
}

type scriptSeg struct{ n int }

func (s scriptSeg) eval(node interface{}) []interface{} {
	arr, ok := asSlice(node)
	if !ok {
		return nil
	}
	idx := len(arr) - s.n
	if idx < 0 || idx >= len(arr) {
		return nil
	}
	return []interface{}{arr[idx]}
}

type filterSeg struct{ expr filterExpr }

func (s filterSeg) eval(node interface{}) []interface{} {
	arr, ok := asSlice(node)
	if !ok {
		return nil
	}
	out := make([]interface{}, 0)
	for _, el := range arr {
		if s.expr.match(el) {
			out = append(out, el)
		}
	}
	return out
}

type recursiveKeySeg struct{ key string }

func (s recursiveKeySeg) eval(node interface{}) []interface{} {
	var out []interface{}
	var walk func(interface{})
	walk = func(n interface{}) {
		if kvs, ok := keyedChildren(n); ok {
			for _, kv := range kvs {
				if kv.hasKey && kv.key == s.key {
					out = append(out, kv.value)
				}
				walk(kv.value)
			}
			return
		}
		if arr, ok := asSlice(n); ok {
			for _, el := range arr {
				walk(el)
			}
		}
	}
	walk(node)
	return out
}

type recursiveIndexSeg struct{ idx int }

func (s recursiveIndexSeg) eval(node interface{}) []interface{} {
	var out []interface{}
	var walk func(interface{})
	walk = func(n interface{}) {
		if arr, ok := asSlice(n); ok {
			if i, ok := normalizeIndex(s.idx, len(arr)); ok {
				out = append(out, arr[i])
			}
			for _, el := range arr {
				walk(el)
			}
			return
		}
		if kvs, ok := keyedChildren(n); ok {
			for _, kv := range kvs {
				walk(kv.value)
			}
		}
	}
	walk(node)
	return out
}

// recursiveWildcardSeg yields the node itself, then every descendant, depth-first.
type recursiveWildcardSeg struct{}

func (recursiveWildcardSeg) eval(node interface{}) []interface{} {
	var out []interface{}
	var walk func(interface{})
	walk = func(n interface{}) {
		out = append(out, n)
		if kvs, ok := keyedChildren(n); ok {
			for _, kv := range kvs {
				walk(kv.value)
			}
			return
		}
		if arr, ok := asSlice(n); ok {
			for _, el := range arr {
				walk(el)
			}
		}
	}
	walk(node)
	return out
}

type recursiveApplySeg struct{ inner segment }

func (s recursiveApplySeg) eval(node interface{}) []interface{} {
	var out []interface{}
	var walk func(interface{})
	walk = func(n interface{}) {
		out = append(out, s.inner.eval(n)...)
		if kvs, ok := keyedChildren(n); ok {
			for _, kv := range kvs {
				walk(kv.value)
			}
			return
		}
		if arr, ok := asSlice(n); ok {
			for _, el := range arr {
				walk(el)
			}
		}
	}
	walk(node)
	return out
}

func makeRecursive(inner segment) segment {
	switch s := inner.(type) {
	case unionSeg:
		parts := make([]segment, len(s.parts))
		for i, part := range s.parts {
			parts[i] = makeRecursive(part)
		}
		return unionSeg{parts: parts}
	case wildcardSeg:
		return recursiveWildcardSeg{}
	case keySeg:
		return recursiveKeySeg{key: s.key}
	case indexSeg:
		return recursiveIndexSeg{idx: s.idx}
	default:
		return recursiveApplySeg{inner: inner}
	}
}

type filterExpr interface {
	match(cur interface{}) bool
}

type orExpr struct{ items []filterExpr }

func (e orExpr) match(cur interface{}) bool {
	for _, item := range e.items {
		if item.match(cur) {
			return true
		}
	}
	return false
}

type andExpr struct{ items []filterExpr }

func (e andExpr) match(cur interface{}) bool {
	for _, item := range e.items {
		if !item.match(cur) {
			return false
		}
	}
	return true
}

type predExpr struct {
	segs []segment
	op   string
	lit  interface{}
}

func (e predExpr) match(cur interface{}) bool {
	vals, ok := valuesAt(cur, e.segs)
	if !ok {
		return false
	}
	if e.op == "" {
		for _, v := range vals {
			if isTruthy(v) {
				return true
			}
		}
		return false
	}
	for _, v := range vals {
		if compareValues(v, e.op, e.lit) {
			return true
		}
	}
	return false
}

func valuesAt(cur interface{}, segs []segment) ([]interface{}, bool) {
	if len(segs) == 0 {
		return []interface{}{cur}, true
	}
	nodes := []interface{}{cur}
	for _, seg := range segs {
		next := make([]interface{}, 0)
		for _, n := range nodes {
			next = append(next, seg.eval(n)...)
		}
		if len(next) == 0 {
			return nil, false
		}
		nodes = next
	}
	return nodes, true
}

type childKV struct {
	key    string
	hasKey bool
	value  interface{}
}

func keyedChildren(n interface{}) ([]childKV, bool) {
	switch v := n.(type) {
	case *Map:
		if v == nil {
			return nil, false
		}
		out := make([]childKV, 0, v.Len())
		v.Iterate(func(k, val interface{}) {
			s, ok := k.(string)
			out = append(out, childKV{key: s, hasKey: ok, value: val})
		})
		return out, true
	case map[string]interface{}:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make([]childKV, 0, len(keys))
		for _, k := range keys {
			out = append(out, childKV{key: k, hasKey: true, value: v[k]})
		}
		return out, true
	case map[interface{}]interface{}:
		out := make([]childKV, 0, len(v))
		keys := make([]string, 0, len(v))
		for k := range v {
			if s, ok := k.(string); ok {
				keys = append(keys, s)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = append(out, childKV{key: k, hasKey: true, value: v[k]})
		}
		// Non-string keys are still descended into, but they are not name-addressable.
		var rest []childKV
		for k, val := range v {
			if _, ok := k.(string); ok {
				continue
			}
			rest = append(rest, childKV{value: val})
		}
		out = append(out, rest...)
		return out, true
	default:
		rv := reflect.ValueOf(n)
		if !rv.IsValid() || rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
			return nil, false
		}
		keys := rv.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		out := make([]childKV, 0, len(keys))
		for _, k := range keys {
			out = append(out, childKV{key: k.String(), hasKey: true, value: rv.MapIndex(k).Interface()})
		}
		return out, true
	}
}

func asSlice(n interface{}) ([]interface{}, bool) {
	switch v := n.(type) {
	case []interface{}:
		return v, true
	case nil:
		return nil, false
	}
	rv := reflect.ValueOf(n)
	if !rv.IsValid() {
		return nil, false
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		out := make([]interface{}, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out[i] = rv.Index(i).Interface()
		}
		return out, true
	default:
		return nil, false
	}
}

func normalizeIndex(idx, n int) (int, bool) {
	if idx < 0 {
		idx = n + idx
	}
	if idx < 0 || idx >= n {
		return 0, false
	}
	return idx, true
}

func lengthOf(n interface{}) (int, bool) {
	switch v := n.(type) {
	case nil:
		return 0, false
	case string:
		return len(v), true
	case *Map:
		if v == nil {
			return 0, false
		}
		return v.Len(), true
	case map[string]interface{}:
		return len(v), true
	case map[interface{}]interface{}:
		return len(v), true
	case []interface{}:
		return len(v), true
	default:
		rv := reflect.ValueOf(n)
		if !rv.IsValid() {
			return 0, false
		}
		switch rv.Kind() {
		case reflect.String, reflect.Slice, reflect.Array, reflect.Map:
			return rv.Len(), true
		default:
			return 0, false
		}
	}
}

func isTruthy(v interface{}) bool {
	if v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t != ""
	case *Map:
		return t != nil && t.Len() > 0
	case map[string]interface{}:
		return len(t) > 0
	case map[interface{}]interface{}:
		return len(t) > 0
	case []interface{}:
		return len(t) > 0
	}
	if i, ok := asInt64(v); ok {
		return i != 0
	}
	if f, ok := asFloat64(v); ok {
		return f != 0
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return false
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return rv.Len() > 0
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return false
		}
	}
	return true
}

func compareValues(left interface{}, op string, right interface{}) bool {
	switch op {
	case "==":
		return valuesEqual(left, right)
	case "!=":
		return !valuesEqual(left, right)
	case "<", ">", "<=", ">=":
		return orderedCompare(left, right, op)
	default:
		return false
	}
}

func valuesEqual(a, b interface{}) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ai, aok := asInt64(a)
	bi, bok := asInt64(b)
	if aok && bok {
		return ai == bi
	}
	af, aok := asFloat64(a)
	bf, bok := asFloat64(b)
	if aok && bok {
		return af == bf
	}
	if as, ok := a.(string); ok {
		bs, ok := b.(string)
		return ok && as == bs
	}
	if ab, ok := a.(bool); ok {
		bb, ok := b.(bool)
		return ok && ab == bb
	}
	return false
}

func orderedCompare(left, right interface{}, op string) bool {
	li, lok := asInt64(left)
	ri, rok := asInt64(right)
	if lok && rok {
		switch op {
		case "<":
			return li < ri
		case ">":
			return li > ri
		case "<=":
			return li <= ri
		case ">=":
			return li >= ri
		}
	}
	lf, lok := asFloat64(left)
	rf, rok := asFloat64(right)
	if lok && rok {
		switch op {
		case "<":
			return lf < rf
		case ">":
			return lf > rf
		case "<=":
			return lf <= rf
		case ">=":
			return lf >= rf
		}
	}
	ls, lok := left.(string)
	rs, rok := right.(string)
	if lok && rok {
		switch op {
		case "<":
			return ls < rs
		case ">":
			return ls > rs
		case "<=":
			return ls <= rs
		case ">=":
			return ls >= rs
		}
	}
	return false
}

func asInt64(v interface{}) (int64, bool) {
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
	case uint:
		if uint64(n) > math.MaxInt64 {
			return 0, false
		}
		return int64(n), true
	case uint8:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint64:
		if n > math.MaxInt64 {
			return 0, false
		}
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return i, true
	default:
		return 0, false
	}
}

func asFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

type jpParser struct {
	path string
	i    int
}

func parseJSONPath(path string) ([]segment, error) {
	p := &jpParser{path: path}
	if path == "" || path[0] != '$' {
		return nil, p.errorf(0, "path must start with '$'")
	}
	p.i = 1
	segs, err := p.parseSegments()
	if err != nil {
		return nil, err
	}
	if p.i != len(p.path) {
		r, _ := utf8.DecodeRuneInString(p.path[p.i:])
		return nil, p.errorf(p.i, "unexpected character %q", r)
	}
	return segs, nil
}

func (p *jpParser) errorf(pos int, format string, args ...interface{}) error {
	if pos < 0 {
		pos = 0
	}
	if pos > len(p.path) {
		pos = len(p.path)
	}
	return &SyntaxError{Message: fmt.Sprintf(format, args...), Position: pos}
}

func (p *jpParser) parseSegments() ([]segment, error) {
	var segs []segment
	for p.i < len(p.path) {
		switch p.path[p.i] {
		case '.':
			seg, err := p.parseDotOrRecursive()
			if err != nil {
				return nil, err
			}
			segs = append(segs, seg)
		case '[':
			seg, err := p.parseBracket()
			if err != nil {
				return nil, err
			}
			segs = append(segs, seg)
		default:
			return segs, nil
		}
	}
	return segs, nil
}

func (p *jpParser) parseDotOrRecursive() (segment, error) {
	start := p.i
	if p.i+1 < len(p.path) && p.path[p.i+1] == '.' {
		return p.parseRecursive()
	}
	// Optional dot before a bracket: $.[0] / $.['key']
	if p.i+1 < len(p.path) && p.path[p.i+1] == '[' {
		p.i++
		return p.parseBracket()
	}
	p.i++
	if p.i >= len(p.path) {
		return nil, p.errorf(start, "expected identifier or '*' after '.'")
	}
	if p.path[p.i] == '*' {
		p.i++
		return wildcardSeg{}, nil
	}
	if !isIdentRune(p.rune()) {
		return nil, p.errorf(p.i, "expected identifier or '*' after '.'")
	}
	return p.parseIdentOrLength()
}

func (p *jpParser) parseRecursive() (segment, error) {
	start := p.i
	p.i += 2
	if p.i >= len(p.path) {
		return nil, p.errorf(start, "expected name, '*', or '[' after '..'")
	}
	switch p.path[p.i] {
	case '*':
		p.i++
		return recursiveWildcardSeg{}, nil
	case '[':
		inner, err := p.parseBracket()
		if err != nil {
			return nil, err
		}
		return makeRecursive(inner), nil
	default:
		if !isIdentRune(p.rune()) {
			return nil, p.errorf(p.i, "expected name, '*', or '[' after '..'")
		}
		seg, err := p.parseIdentOrLength()
		if err != nil {
			return nil, err
		}
		return makeRecursive(seg), nil
	}
}

func (p *jpParser) parseIdentOrLength() (segment, error) {
	name, pos, err := p.parseIdent()
	if err != nil {
		return nil, err
	}
	if name == "length" && p.i < len(p.path) && p.path[p.i] == '(' {
		p.i++
		p.skipWS()
		if p.i >= len(p.path) || p.path[p.i] != ')' {
			return nil, p.errorf(p.pos(), "expected ')' after length(")
		}
		p.i++
		return lengthSeg{}, nil
	}
	if name == "" {
		return nil, p.errorf(pos, "expected identifier")
	}
	return keySeg{key: name}, nil
}

func (p *jpParser) parseIdent() (string, int, error) {
	start := p.i
	r := p.rune()
	if !isIdentRune(r) {
		return "", start, p.errorf(start, "expected identifier")
	}
	for p.i < len(p.path) {
		r = p.rune()
		if !isIdentRune(r) {
			break
		}
		_, size := utf8.DecodeRuneInString(p.path[p.i:])
		p.i += size
	}
	return p.path[start:p.i], start, nil
}

func (p *jpParser) parseBracket() (segment, error) {
	start := p.i
	p.i++ // '['
	p.skipWS()
	if p.i >= len(p.path) {
		return nil, p.errorf(start, "unterminated '['")
	}
	switch p.path[p.i] {
	case '?':
		return p.parseFilter(start)
	case '(':
		return p.parseScript(start)
	case '*':
		p.i++
		p.skipWS()
		if err := p.expect(']', "to close wildcard"); err != nil {
			return nil, err
		}
		return wildcardSeg{}, nil
	default:
		return p.parseUnion(start)
	}
}

func (p *jpParser) parseFilter(start int) (segment, error) {
	p.i++ // '?'
	p.skipWS()
	if err := p.expect('(', "to start filter expression"); err != nil {
		return nil, err
	}
	expr, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	p.skipWS()
	if err := p.expect(')', "to close filter expression"); err != nil {
		return nil, err
	}
	p.skipWS()
	if err := p.expect(']', "to close filter"); err != nil {
		return nil, err
	}
	_ = start
	return filterSeg{expr: expr}, nil
}

func (p *jpParser) parseOr() (filterExpr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	items := []filterExpr{left}
	for {
		mark := p.i
		p.skipWS()
		if strings.HasPrefix(p.rest(), "||") {
			p.i += 2
			right, err := p.parseAnd()
			if err != nil {
				return nil, err
			}
			items = append(items, right)
			continue
		}
		p.i = mark
		break
	}
	if len(items) == 1 {
		return items[0], nil
	}
	return orExpr{items: items}, nil
}

func (p *jpParser) parseAnd() (filterExpr, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	items := []filterExpr{left}
	for {
		mark := p.i
		p.skipWS()
		if strings.HasPrefix(p.rest(), "&&") {
			p.i += 2
			right, err := p.parsePrimary()
			if err != nil {
				return nil, err
			}
			items = append(items, right)
			continue
		}
		p.i = mark
		break
	}
	if len(items) == 1 {
		return items[0], nil
	}
	return andExpr{items: items}, nil
}

func (p *jpParser) parsePrimary() (filterExpr, error) {
	p.skipWS()
	if p.i < len(p.path) && p.path[p.i] == '(' {
		p.i++
		expr, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		p.skipWS()
		if err := p.expect(')', "to close grouped expression"); err != nil {
			return nil, err
		}
		return expr, nil
	}
	return p.parsePredicate()
}

func (p *jpParser) parsePredicate() (filterExpr, error) {
	p.skipWS()
	if p.i >= len(p.path) || p.path[p.i] != '@' {
		return nil, p.errorf(p.pos(), "expected '@' in filter")
	}
	p.i++
	segs, err := p.parseSegments()
	if err != nil {
		return nil, err
	}
	op, err := p.parseOperator()
	if err != nil {
		return nil, err
	}
	if op == "" {
		return predExpr{segs: segs}, nil
	}
	p.skipWS()
	lit, err := p.parseLiteral()
	if err != nil {
		return nil, err
	}
	return predExpr{segs: segs, op: op, lit: lit}, nil
}

func (p *jpParser) parseOperator() (string, error) {
	mark := p.i
	p.skipWS()
	rest := p.rest()
	switch {
	case strings.HasPrefix(rest, "=="):
		p.i += 2
		return "==", nil
	case strings.HasPrefix(rest, "!="):
		p.i += 2
		return "!=", nil
	case strings.HasPrefix(rest, "<="):
		p.i += 2
		return "<=", nil
	case strings.HasPrefix(rest, ">="):
		p.i += 2
		return ">=", nil
	case strings.HasPrefix(rest, "<"):
		p.i++
		return "<", nil
	case strings.HasPrefix(rest, ">"):
		p.i++
		return ">", nil
	case strings.HasPrefix(rest, "!") || strings.HasPrefix(rest, "="):
		return "", p.errorf(p.i, "expected comparison operator")
	default:
		p.i = mark
		return "", nil
	}
}

func (p *jpParser) parseLiteral() (interface{}, error) {
	if p.i >= len(p.path) {
		return nil, p.errorf(p.pos(), "expected literal")
	}
	switch p.path[p.i] {
	case '\'', '"':
		return p.parseString()
	}
	if p.consumeWord("true") {
		return true, nil
	}
	if p.consumeWord("false") {
		return false, nil
	}
	if p.consumeWord("null") {
		return nil, nil
	}
	if isNumberStart(p.rest()) {
		return p.parseNumber()
	}
	return nil, p.errorf(p.i, "expected literal")
}

func (p *jpParser) parseScript(start int) (segment, error) {
	p.i++ // '('
	p.skipWS()
	if err := p.expect('@', "in length script"); err != nil {
		return nil, err
	}
	p.skipWS()
	if err := p.expect('.', "in length script"); err != nil {
		return nil, err
	}
	p.skipWS()
	if !strings.HasPrefix(p.rest(), "length") || !p.wordBoundary("length") {
		return nil, p.errorf(p.pos(), "expected length in script index")
	}
	p.i += len("length")
	p.skipWS()
	if p.i < len(p.path) && p.path[p.i] == '(' {
		p.i++
		p.skipWS()
		if err := p.expect(')', "after length("); err != nil {
			return nil, err
		}
		p.skipWS()
	}
	if err := p.expect('-', "in length script"); err != nil {
		return nil, err
	}
	p.skipWS()
	n, err := p.parseNonNegInt()
	if err != nil {
		return nil, err
	}
	p.skipWS()
	if err := p.expect(')', "to close script"); err != nil {
		return nil, err
	}
	p.skipWS()
	if err := p.expect(']', "to close script"); err != nil {
		return nil, err
	}
	_ = start
	return scriptSeg{n: n}, nil
}

func (p *jpParser) parseUnion(start int) (segment, error) {
	var parts []segment
	for {
		p.skipWS()
		if p.i >= len(p.path) {
			return nil, p.errorf(start, "unterminated '['")
		}
		var seg segment
		switch p.path[p.i] {
		case '\'', '"':
			s, err := p.parseString()
			if err != nil {
				return nil, err
			}
			seg = keySeg{key: s}
		default:
			if !isIndexStart(p.rest()) {
				return nil, p.errorf(p.i, "expected string or index")
			}
			idx, err := p.parseIndex()
			if err != nil {
				return nil, err
			}
			seg = indexSeg{idx: idx}
		}
		parts = append(parts, seg)
		p.skipWS()
		if p.i < len(p.path) && p.path[p.i] == ',' {
			p.i++
			continue
		}
		break
	}
	if err := p.expect(']', "to close bracket"); err != nil {
		return nil, err
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	return unionSeg{parts: parts}, nil
}

func (p *jpParser) parseString() (string, error) {
	if p.i >= len(p.path) {
		return "", p.errorf(p.pos(), "expected string")
	}
	quote := p.path[p.i]
	start := p.i
	p.i++
	var b strings.Builder
	for p.i < len(p.path) {
		c := p.path[p.i]
		if c == quote {
			p.i++
			return b.String(), nil
		}
		if c == '\\' {
			escPos := p.i
			p.i++
			if p.i >= len(p.path) {
				return "", p.errorf(escPos, "unterminated escape")
			}
			esc := p.path[p.i]
			p.i++
			switch esc {
			case '\\', '\'', '"', '/':
				b.WriteByte(esc)
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'u':
				if p.i+4 > len(p.path) {
					return "", p.errorf(escPos, "invalid unicode escape")
				}
				hex := p.path[p.i : p.i+4]
				r, err := strconv.ParseUint(hex, 16, 32)
				if err != nil {
					return "", p.errorf(escPos, "invalid unicode escape")
				}
				p.i += 4
				b.WriteRune(rune(r))
			default:
				return "", p.errorf(escPos, "invalid escape")
			}
			continue
		}
		if c == '\n' || c == '\r' {
			return "", p.errorf(start, "unterminated string")
		}
		b.WriteByte(c)
		p.i++
	}
	return "", p.errorf(start, "unterminated string")
}

func (p *jpParser) parseIndex() (int, error) {
	start := p.i
	if p.i < len(p.path) && (p.path[p.i] == '+' || p.path[p.i] == '-') {
		p.i++
		p.skipWS()
	}
	digs := p.i
	for p.i < len(p.path) && p.path[p.i] >= '0' && p.path[p.i] <= '9' {
		p.i++
	}
	if p.i == digs {
		return 0, p.errorf(start, "expected index")
	}
	n, err := strconv.Atoi(p.path[start:p.i])
	if err != nil {
		// Sign and digits may be separated by whitespace; reparse compactly.
		text := strings.ReplaceAll(p.path[start:p.i], " ", "")
		text = strings.ReplaceAll(text, "\t", "")
		n, err = strconv.Atoi(text)
		if err != nil {
			return 0, p.errorf(start, "invalid index")
		}
	}
	return n, nil
}

func (p *jpParser) parseNonNegInt() (int, error) {
	start := p.i
	if p.i >= len(p.path) || p.path[p.i] < '0' || p.path[p.i] > '9' {
		return 0, p.errorf(p.pos(), "expected integer")
	}
	for p.i < len(p.path) && p.path[p.i] >= '0' && p.path[p.i] <= '9' {
		p.i++
	}
	n, err := strconv.Atoi(p.path[start:p.i])
	if err != nil {
		return 0, p.errorf(start, "invalid integer")
	}
	return n, nil
}

func (p *jpParser) parseNumber() (interface{}, error) {
	start := p.i
	if p.i < len(p.path) && (p.path[p.i] == '+' || p.path[p.i] == '-') {
		p.i++
	}
	sawDigit := false
	for p.i < len(p.path) && p.path[p.i] >= '0' && p.path[p.i] <= '9' {
		sawDigit = true
		p.i++
	}
	isFloat := false
	if p.i < len(p.path) && p.path[p.i] == '.' {
		// A single dot that starts another path segment is not part of the number.
		if p.i+1 < len(p.path) && (p.path[p.i+1] < '0' || p.path[p.i+1] > '9') && p.path[p.i+1] != 'e' && p.path[p.i+1] != 'E' {
			// fall through; the dot is not consumed
		} else {
			isFloat = true
			p.i++
			for p.i < len(p.path) && p.path[p.i] >= '0' && p.path[p.i] <= '9' {
				sawDigit = true
				p.i++
			}
		}
	}
	if p.i < len(p.path) && (p.path[p.i] == 'e' || p.path[p.i] == 'E') {
		isFloat = true
		p.i++
		if p.i < len(p.path) && (p.path[p.i] == '+' || p.path[p.i] == '-') {
			p.i++
		}
		expStart := p.i
		for p.i < len(p.path) && p.path[p.i] >= '0' && p.path[p.i] <= '9' {
			p.i++
		}
		if p.i == expStart {
			return nil, p.errorf(start, "invalid number")
		}
	}
	if !sawDigit {
		return nil, p.errorf(start, "expected number")
	}
	text := p.path[start:p.i]
	if !isFloat {
		i, err := strconv.ParseInt(text, 10, 64)
		if err == nil {
			if i >= math.MinInt && i <= math.MaxInt {
				return int(i), nil
			}
			return i, nil
		}
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return nil, p.errorf(start, "invalid number")
	}
	return f, nil
}

func (p *jpParser) consumeWord(word string) bool {
	if !strings.HasPrefix(p.rest(), word) || !p.wordBoundary(word) {
		return false
	}
	p.i += len(word)
	return true
}

func (p *jpParser) wordBoundary(word string) bool {
	end := p.i + len(word)
	if end >= len(p.path) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(p.path[end:])
	// Hyphen is part of a dot-notation identifier, but it also terminates
	// keywords so that [(@.length-1)] and "length-1" as a following token work.
	if r == '-' {
		return true
	}
	return !isIdentRune(r)
}

func (p *jpParser) expect(c byte, why string) error {
	if p.i >= len(p.path) {
		return p.errorf(len(p.path), "expected '%c' %s", c, why)
	}
	if p.path[p.i] != c {
		return p.errorf(p.i, "expected '%c' %s", c, why)
	}
	p.i++
	return nil
}

func (p *jpParser) skipWS() {
	for p.i < len(p.path) {
		switch p.path[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *jpParser) rest() string {
	if p.i >= len(p.path) {
		return ""
	}
	return p.path[p.i:]
}

func (p *jpParser) pos() int {
	if p.i > len(p.path) {
		return len(p.path)
	}
	return p.i
}

func (p *jpParser) rune() rune {
	if p.i >= len(p.path) {
		return utf8.RuneError
	}
	r, _ := utf8.DecodeRuneInString(p.path[p.i:])
	return r
}

func isIdentRune(r rune) bool {
	return r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func isNumberStart(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '+' || s[0] == '-' {
		s = s[1:]
	}
	if s == "" {
		return false
	}
	if s[0] >= '0' && s[0] <= '9' {
		return true
	}
	return s[0] == '.' && len(s) > 1 && s[1] >= '0' && s[1] <= '9'
}

func isIndexStart(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '+' || s[0] == '-' {
		return true
	}
	return s[0] >= '0' && s[0] <= '9'
}
