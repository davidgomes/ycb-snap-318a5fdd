// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package orderedmap

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// SyntaxError is returned for an invalid JSONPath expression.
// Position is the byte offset of the failure.
type SyntaxError struct {
	Message  string
	Position int
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("syntax error at position %d: %s", e.Position, e.Message)
}

// Query evaluates a JSONPath expression against doc.
// An empty slice is returned when nothing matches.
// Selectors that do not apply to a value's type yield no match.
func Query(doc interface{}, path string) ([]interface{}, error) {
	segs, err := parsePath(path)
	if err != nil {
		return nil, err
	}
	cur := []interface{}{doc}
	for _, seg := range segs {
		next := make([]interface{}, 0)
		for _, n := range cur {
			next = append(next, seg.eval(n)...)
		}
		cur = next
	}
	if cur == nil {
		cur = []interface{}{}
	}
	return cur, nil
}

// QueryOne returns the first match. When nothing matches it returns (nil, false, nil).
func QueryOne(doc interface{}, path string) (interface{}, bool, error) {
	res, err := Query(doc, path)
	if err != nil {
		return nil, false, err
	}
	if len(res) == 0 {
		return nil, false, nil
	}
	return res[0], true, nil
}

type segment interface {
	eval(v interface{}) []interface{}
}

type keySeg struct{ key string }
type wildSeg struct{}
type indexSeg struct{ idx int }
type unionKeySeg struct{ keys []string }
type unionIdxSeg struct{ idxs []int }
type scriptSeg struct{ sub int }
type lengthSeg struct{}
type filterSeg struct{ pred predicate }
type recSeg struct{ inner segment }

func (s keySeg) eval(v interface{}) []interface{} {
	val, ok := getKey(v, s.key)
	if !ok {
		return nil
	}
	return []interface{}{val}
}

func (wildSeg) eval(v interface{}) []interface{} {
	return children(v)
}

func (s indexSeg) eval(v interface{}) []interface{} {
	val, ok := getIndex(v, s.idx)
	if !ok {
		return nil
	}
	return []interface{}{val}
}

func (s unionKeySeg) eval(v interface{}) []interface{} {
	var out []interface{}
	for _, k := range s.keys {
		if val, ok := getKey(v, k); ok {
			out = append(out, val)
		}
	}
	return out
}

func (s unionIdxSeg) eval(v interface{}) []interface{} {
	var out []interface{}
	for _, idx := range s.idxs {
		if val, ok := getIndex(v, idx); ok {
			out = append(out, val)
		}
	}
	return out
}

func (s scriptSeg) eval(v interface{}) []interface{} {
	arr, ok := asSlice(v)
	if !ok {
		return nil
	}
	idx := len(arr) - s.sub
	if idx < 0 || idx >= len(arr) {
		return nil
	}
	return []interface{}{arr[idx]}
}

func (lengthSeg) eval(v interface{}) []interface{} {
	n, ok := lengthOf(v)
	if !ok {
		return nil
	}
	return []interface{}{n}
}

func (s filterSeg) eval(v interface{}) []interface{} {
	arr, ok := asSlice(v)
	if !ok {
		return nil
	}
	var out []interface{}
	for _, el := range arr {
		if s.pred.match(el) {
			out = append(out, el)
		}
	}
	return out
}

func (s recSeg) eval(v interface{}) []interface{} {
	if _, ok := s.inner.(wildSeg); ok {
		return allNodes(v)
	}
	var out []interface{}
	var walk func(interface{})
	walk = func(n interface{}) {
		out = append(out, s.inner.eval(n)...)
		for _, c := range children(n) {
			walk(c)
		}
	}
	walk(v)
	return out
}

func allNodes(v interface{}) []interface{} {
	out := []interface{}{v}
	for _, c := range children(v) {
		out = append(out, allNodes(c)...)
	}
	return out
}

func children(v interface{}) []interface{} {
	switch t := v.(type) {
	case *Map:
		if t == nil {
			return nil
		}
		out := make([]interface{}, 0, t.Len())
		t.Iterate(func(_, val interface{}) {
			out = append(out, val)
		})
		return out
	case []interface{}:
		if t == nil {
			return []interface{}{}
		}
		out := make([]interface{}, len(t))
		copy(out, t)
		return out
	case map[string]interface{}:
		// encoding/json object order is not significant; sort for stability.
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sortStrings(keys)
		out := make([]interface{}, 0, len(keys))
		for _, k := range keys {
			out = append(out, t[k])
		}
		return out
	case map[interface{}]interface{}:
		var str []strKV
		var other []interface{}
		for k, val := range t {
			if ks, ok := k.(string); ok {
				str = append(str, strKV{ks, val})
			} else {
				other = append(other, val)
			}
		}
		sortKV(str)
		out := make([]interface{}, 0, len(str)+len(other))
		for _, item := range str {
			out = append(out, item.v)
		}
		out = append(out, other...)
		return out
	default:
		return nil
	}
}

func sortStrings(ss []string) {
	for i := 1; i < len(ss); i++ {
		j := i
		for j > 0 && ss[j] < ss[j-1] {
			ss[j], ss[j-1] = ss[j-1], ss[j]
			j--
		}
	}
}

type strKV struct {
	k string
	v interface{}
}

func sortKV(items []strKV) {
	for i := 1; i < len(items); i++ {
		j := i
		for j > 0 && items[j].k < items[j-1].k {
			items[j], items[j-1] = items[j-1], items[j]
			j--
		}
	}
}

func getKey(v interface{}, key string) (interface{}, bool) {
	switch t := v.(type) {
	case *Map:
		if t == nil {
			return nil, false
		}
		return t.Get(key)
	case map[string]interface{}:
		val, ok := t[key]
		return val, ok
	case map[interface{}]interface{}:
		val, ok := t[key]
		return val, ok
	default:
		return nil, false
	}
}

func asSlice(v interface{}) ([]interface{}, bool) {
	arr, ok := v.([]interface{})
	return arr, ok
}

func getIndex(v interface{}, idx int) (interface{}, bool) {
	arr, ok := asSlice(v)
	if !ok {
		return nil, false
	}
	if idx < 0 {
		idx = len(arr) + idx
	}
	if idx < 0 || idx >= len(arr) {
		return nil, false
	}
	return arr[idx], true
}

func lengthOf(v interface{}) (int, bool) {
	switch t := v.(type) {
	case *Map:
		if t == nil {
			return 0, true
		}
		return t.Len(), true
	case map[string]interface{}:
		return len(t), true
	case map[interface{}]interface{}:
		return len(t), true
	case []interface{}:
		return len(t), true
	case string:
		return len(t), true
	default:
		return 0, false
	}
}

func truthy(v interface{}) bool {
	if v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t != ""
	case []interface{}:
		return len(t) != 0
	case *Map:
		return t != nil && t.Len() != 0
	case map[string]interface{}:
		return len(t) != 0
	case map[interface{}]interface{}:
		return len(t) != 0
	default:
		if n, ok := asNumber(v); ok {
			return n != 0
		}
		return true
	}
}

func asNumber(v interface{}) (float64, bool) {
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
	default:
		return 0, false
	}
}

func valuesEqual(a, b interface{}) bool {
	an, aok := asNumber(a)
	bn, bok := asNumber(b)
	if aok || bok {
		return aok && bok && an == bn
	}
	return fmt.Sprint(a) == fmt.Sprint(b) && sameKind(a, b)
}

func sameKind(a, b interface{}) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	switch a.(type) {
	case bool:
		_, ok := b.(bool)
		return ok
	case string:
		_, ok := b.(string)
		return ok
	default:
		return fmt.Sprintf("%T", a) == fmt.Sprintf("%T", b)
	}
}

func lessValues(a, b interface{}) (bool, bool) {
	an, aok := asNumber(a)
	bn, bok := asNumber(b)
	if aok && bok {
		return an < bn, true
	}
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok && bok {
		return as < bs, true
	}
	return false, false
}

type predicate interface {
	match(cur interface{}) bool
}

type orPred struct{ a, b predicate }
type andPred struct{ a, b predicate }

func (p orPred) match(cur interface{}) bool  { return p.a.match(cur) || p.b.match(cur) }
func (p andPred) match(cur interface{}) bool { return p.a.match(cur) && p.b.match(cur) }

type truthPred struct{ v fvalue }

func (p truthPred) match(cur interface{}) bool {
	val, ok := p.v.eval(cur)
	if !ok {
		return false
	}
	return truthy(val)
}

type cmpPred struct {
	op    string
	left  fvalue
	right fvalue
}

func (p cmpPred) match(cur interface{}) bool {
	lv, lok := p.left.eval(cur)
	rv, rok := p.right.eval(cur)
	if !lok || !rok {
		return false
	}
	switch p.op {
	case "==":
		return valuesEqual(lv, rv)
	case "!=":
		return !valuesEqual(lv, rv)
	case "<":
		lt, ok := lessValues(lv, rv)
		return ok && lt
	case ">":
		lt, ok := lessValues(rv, lv)
		return ok && lt
	case "<=":
		if valuesEqual(lv, rv) {
			return true
		}
		lt, ok := lessValues(lv, rv)
		return ok && lt
	case ">=":
		if valuesEqual(lv, rv) {
			return true
		}
		lt, ok := lessValues(rv, lv)
		return ok && lt
	default:
		return false
	}
}

type fvalue interface {
	eval(cur interface{}) (interface{}, bool)
}

type litValue struct{ v interface{} }

func (l litValue) eval(interface{}) (interface{}, bool) { return l.v, true }

type lenValue struct{ inner fvalue }

func (l lenValue) eval(cur interface{}) (interface{}, bool) {
	v, ok := l.inner.eval(cur)
	if !ok {
		return nil, false
	}
	n, ok := lengthOf(v)
	if !ok {
		return nil, false
	}
	return n, true
}

type pathValue struct{ steps []fstep }

func (p pathValue) eval(cur interface{}) (interface{}, bool) {
	v := cur
	for _, st := range p.steps {
		nv, ok := st.apply(v)
		if !ok {
			return nil, false
		}
		v = nv
	}
	return v, true
}

type fstep interface {
	apply(v interface{}) (interface{}, bool)
}

type fkey struct{ key string }
type fidx struct{ idx int }
type flen struct{}

func (s fkey) apply(v interface{}) (interface{}, bool) { return getKey(v, s.key) }
func (s fidx) apply(v interface{}) (interface{}, bool) { return getIndex(v, s.idx) }
func (flen) apply(v interface{}) (interface{}, bool) {
	n, ok := lengthOf(v)
	if !ok {
		return nil, false
	}
	return n, true
}

type parser struct {
	s string
	i int
}

func parsePath(path string) ([]segment, *SyntaxError) {
	p := &parser{s: path}
	if p.eof() || path[0] != '$' {
		return nil, &SyntaxError{Message: "path must start with '$'", Position: 0}
	}
	p.i = 1
	var segs []segment
	for !p.eof() {
		seg, err := p.parseSegment()
		if err != nil {
			return nil, err
		}
		segs = append(segs, seg)
	}
	return segs, nil
}

func (p *parser) parseSegment() (segment, *SyntaxError) {
	p.skipSpace()
	if p.eof() {
		return nil, p.err("unexpected end of path")
	}
	switch p.s[p.i] {
	case '.':
		return p.parseDot()
	case '[':
		return p.parseBracket()
	default:
		return nil, p.err("unexpected character")
	}
}

func (p *parser) parseDot() (segment, *SyntaxError) {
	p.i++ // '.'
	if !p.eof() && p.s[p.i] == '.' {
		p.i++
		inner, err := p.parseDescendant()
		if err != nil {
			return nil, err
		}
		return recSeg{inner}, nil
	}
	return p.parseChildDot()
}

func (p *parser) parseDescendant() (segment, *SyntaxError) {
	if p.eof() {
		return nil, p.err("expected selector after '..'")
	}
	if p.s[p.i] == '[' {
		return p.parseBracket()
	}
	return p.parseChildDot()
}

func (p *parser) parseChildDot() (segment, *SyntaxError) {
	if p.eof() {
		return nil, p.err("expected selector after '.'")
	}
	if p.s[p.i] == '*' {
		p.i++
		return wildSeg{}, nil
	}
	id, err := p.parseIdent()
	if err != nil {
		return nil, err
	}
	if id == "length" && !p.eof() && p.s[p.i] == '(' {
		if err := p.expectCall(); err != nil {
			return nil, err
		}
		return lengthSeg{}, nil
	}
	return keySeg{id}, nil
}

func (p *parser) parseBracket() (segment, *SyntaxError) {
	if err := p.expectByte('['); err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.eof() {
		return nil, p.err("unclosed '['")
	}
	switch p.s[p.i] {
	case '?':
		return p.parseFilter()
	case '(':
		return p.parseScript()
	case '*':
		p.i++
		p.skipSpace()
		if err := p.expectByte(']'); err != nil {
			return nil, err
		}
		return wildSeg{}, nil
	case '\'', '"':
		return p.parseKeyUnion()
	default:
		return p.parseIndexUnion()
	}
}

func (p *parser) parseKeyUnion() (segment, *SyntaxError) {
	var keys []string
	for {
		p.skipSpace()
		if p.eof() || (p.s[p.i] != '\'' && p.s[p.i] != '"') {
			return nil, p.err("expected string")
		}
		s, err := p.parseString()
		if err != nil {
			return nil, err
		}
		keys = append(keys, s)
		p.skipSpace()
		if p.eof() {
			return nil, p.err("unclosed '['")
		}
		if p.s[p.i] == ',' {
			p.i++
			continue
		}
		if p.s[p.i] == ']' {
			p.i++
			break
		}
		return nil, p.err("expected ',' or ']'")
	}
	if len(keys) == 1 {
		return keySeg{keys[0]}, nil
	}
	return unionKeySeg{keys}, nil
}

func (p *parser) parseIndexUnion() (segment, *SyntaxError) {
	var idxs []int
	for {
		p.skipSpace()
		n, err := p.parseSignedInt()
		if err != nil {
			return nil, err
		}
		idxs = append(idxs, n)
		p.skipSpace()
		if p.eof() {
			return nil, p.err("unclosed '['")
		}
		if p.s[p.i] == ',' {
			p.i++
			continue
		}
		if p.s[p.i] == ']' {
			p.i++
			break
		}
		return nil, p.err("expected ',' or ']'")
	}
	if len(idxs) == 1 {
		return indexSeg{idxs[0]}, nil
	}
	return unionIdxSeg{idxs}, nil
}

func (p *parser) parseScript() (segment, *SyntaxError) {
	if err := p.expectByte('('); err != nil {
		return nil, err
	}
	p.skipSpace()
	if err := p.expectByte('@'); err != nil {
		return nil, err
	}
	p.skipSpace()
	if err := p.expectByte('.'); err != nil {
		return nil, err
	}
	p.skipSpace()
	if !p.startsWith("length") || p.identContAtExceptHyphen(p.i+len("length")) {
		return nil, p.err("expected length")
	}
	p.i += len("length")
	if !p.eof() && p.s[p.i] == '(' {
		if err := p.expectCall(); err != nil {
			return nil, err
		}
	}
	p.skipSpace()
	sub := 0
	if !p.eof() && p.s[p.i] == '-' {
		p.i++
		p.skipSpace()
		n, err := p.parseInt()
		if err != nil {
			return nil, err
		}
		sub = n
	}
	p.skipSpace()
	if err := p.expectByte(')'); err != nil {
		return nil, err
	}
	p.skipSpace()
	if err := p.expectByte(']'); err != nil {
		return nil, err
	}
	return scriptSeg{sub}, nil
}

func (p *parser) parseFilter() (segment, *SyntaxError) {
	if err := p.expectByte('?'); err != nil {
		return nil, err
	}
	p.skipSpace()
	if err := p.expectByte('('); err != nil {
		return nil, err
	}
	pred, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if err := p.expectByte(')'); err != nil {
		return nil, err
	}
	p.skipSpace()
	if err := p.expectByte(']'); err != nil {
		return nil, err
	}
	return filterSeg{pred}, nil
}

func (p *parser) parseOr() (predicate, *SyntaxError) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		p.skipSpace()
		if !p.startsWith("||") {
			return left, nil
		}
		p.i += 2
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = orPred{left, right}
	}
}

func (p *parser) parseAnd() (predicate, *SyntaxError) {
	left, err := p.parsePred()
	if err != nil {
		return nil, err
	}
	for {
		p.skipSpace()
		if !p.startsWith("&&") {
			return left, nil
		}
		p.i += 2
		right, err := p.parsePred()
		if err != nil {
			return nil, err
		}
		left = andPred{left, right}
	}
}

func (p *parser) parsePred() (predicate, *SyntaxError) {
	p.skipSpace()
	if p.eof() {
		return nil, p.err("expected filter expression")
	}
	if p.s[p.i] == '(' {
		p.i++
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if err := p.expectByte(')'); err != nil {
			return nil, err
		}
		return inner, nil
	}
	left, err := p.parseFValue()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	op := p.parseOp()
	if op == "" {
		return truthPred{left}, nil
	}
	right, err := p.parseFValue()
	if err != nil {
		return nil, err
	}
	return cmpPred{op: op, left: left, right: right}, nil
}

func (p *parser) parseOp() string {
	ops := []string{"==", "!=", "<=", ">=", "<", ">"}
	for _, op := range ops {
		if p.startsWith(op) {
			p.i += len(op)
			return op
		}
	}
	return ""
}

func (p *parser) parseFValue() (fvalue, *SyntaxError) {
	p.skipSpace()
	if p.eof() {
		return nil, p.err("expected value")
	}
	if p.startsWith("length") && p.hasLengthCall() {
		p.i += len("length")
		if err := p.expectByte('('); err != nil {
			return nil, err
		}
		inner, err := p.parseFValue()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if err := p.expectByte(')'); err != nil {
			return nil, err
		}
		return lenValue{inner}, nil
	}
	if p.s[p.i] == '@' {
		return p.parseAtPath()
	}
	return p.parseLiteral()
}

func (p *parser) hasLengthCall() bool {
	j := p.i + len("length")
	for j < len(p.s) && isSpace(p.s[j]) {
		j++
	}
	return j < len(p.s) && p.s[j] == '('
}

func (p *parser) parseAtPath() (fvalue, *SyntaxError) {
	if err := p.expectByte('@'); err != nil {
		return nil, err
	}
	var steps []fstep
	for {
		p.skipSpace()
		if p.eof() {
			break
		}
		switch p.s[p.i] {
		case '.':
			p.i++
			p.skipSpace()
			if p.eof() {
				return nil, p.err("expected name")
			}
			if p.s[p.i] == '*' {
				return nil, p.err("wildcard is not allowed in a filter path")
			}
			id, err := p.parseIdent()
			if err != nil {
				return nil, err
			}
			if id == "length" && !p.eof() && p.s[p.i] == '(' {
				if err := p.expectCall(); err != nil {
					return nil, err
				}
				steps = append(steps, flen{})
			} else {
				steps = append(steps, fkey{id})
			}
		case '[':
			p.i++
			p.skipSpace()
			if p.eof() {
				return nil, p.err("unclosed '['")
			}
			if p.s[p.i] == '\'' || p.s[p.i] == '"' {
				s, err := p.parseString()
				if err != nil {
					return nil, err
				}
				p.skipSpace()
				if err := p.expectByte(']'); err != nil {
					return nil, err
				}
				steps = append(steps, fkey{s})
			} else {
				n, err := p.parseSignedInt()
				if err != nil {
					return nil, err
				}
				p.skipSpace()
				if err := p.expectByte(']'); err != nil {
					return nil, err
				}
				steps = append(steps, fidx{n})
			}
		default:
			return pathValue{steps}, nil
		}
	}
	return pathValue{steps}, nil
}

func (p *parser) parseLiteral() (fvalue, *SyntaxError) {
	if p.eof() {
		return nil, p.err("expected literal")
	}
	if p.s[p.i] == '\'' || p.s[p.i] == '"' {
		s, err := p.parseString()
		if err != nil {
			return nil, err
		}
		return litValue{s}, nil
	}
	if p.startsWith("null") && !p.identContAt(p.i+4) {
		p.i += 4
		return litValue{nil}, nil
	}
	if p.startsWith("true") && !p.identContAt(p.i+4) {
		p.i += 4
		return litValue{true}, nil
	}
	if p.startsWith("false") && !p.identContAt(p.i+5) {
		p.i += 5
		return litValue{false}, nil
	}
	if p.s[p.i] == '-' || isDigit(p.s[p.i]) {
		return p.parseNumber()
	}
	return nil, p.err("expected literal")
}

func (p *parser) parseNumber() (fvalue, *SyntaxError) {
	start := p.i
	if p.s[p.i] == '-' {
		p.i++
	}
	if p.eof() || !isDigit(p.s[p.i]) {
		return nil, &SyntaxError{Message: "expected number", Position: start}
	}
	for !p.eof() && isDigit(p.s[p.i]) {
		p.i++
	}
	if !p.eof() && p.s[p.i] == '.' {
		p.i++
		if p.eof() || !isDigit(p.s[p.i]) {
			return nil, &SyntaxError{Message: "expected digit after decimal point", Position: p.i}
		}
		for !p.eof() && isDigit(p.s[p.i]) {
			p.i++
		}
		f, err := strconv.ParseFloat(p.s[start:p.i], 64)
		if err != nil {
			return nil, &SyntaxError{Message: "invalid number", Position: start}
		}
		return litValue{f}, nil
	}
	n, err := strconv.ParseInt(p.s[start:p.i], 10, 64)
	if err != nil {
		return nil, &SyntaxError{Message: "invalid number", Position: start}
	}
	return litValue{n}, nil
}

func (p *parser) parseString() (string, *SyntaxError) {
	if p.eof() || (p.s[p.i] != '\'' && p.s[p.i] != '"') {
		return "", p.err("expected string")
	}
	quote := p.s[p.i]
	p.i++
	var b strings.Builder
	for {
		if p.eof() {
			return "", p.err("unclosed string")
		}
		c := p.s[p.i]
		if c == quote {
			p.i++
			return b.String(), nil
		}
		if c == '\\' {
			p.i++
			if p.eof() {
				return "", p.err("unclosed string escape")
			}
			esc := p.s[p.i]
			p.i++
			switch esc {
			case '\\', '/', '\'', '"':
				b.WriteByte(esc)
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'u':
				if p.i+4 > len(p.s) {
					return "", p.err("short unicode escape")
				}
				r, err := strconv.ParseUint(p.s[p.i:p.i+4], 16, 32)
				if err != nil {
					return "", &SyntaxError{Message: "invalid unicode escape", Position: p.i}
				}
				p.i += 4
				b.WriteRune(rune(r))
			default:
				return "", &SyntaxError{Message: "invalid escape", Position: p.i - 1}
			}
			continue
		}
		r, size := utf8.DecodeRuneInString(p.s[p.i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteByte(c)
			p.i++
			continue
		}
		b.WriteRune(r)
		p.i += size
	}
}

func (p *parser) parseIdent() (string, *SyntaxError) {
	if p.eof() || !isIdentStart(p.s[p.i]) {
		return "", p.err("expected identifier")
	}
	start := p.i
	p.i++
	for !p.eof() && isIdentCont(p.s[p.i]) {
		p.i++
	}
	return p.s[start:p.i], nil
}

func (p *parser) parseSignedInt() (int, *SyntaxError) {
	if p.eof() {
		return 0, p.err("expected integer")
	}
	sign := 1
	if p.s[p.i] == '-' {
		sign = -1
		p.i++
	}
	n, err := p.parseInt()
	if err != nil {
		return 0, err
	}
	return sign * n, nil
}

func (p *parser) parseInt() (int, *SyntaxError) {
	if p.eof() || !isDigit(p.s[p.i]) {
		return 0, p.err("expected integer")
	}
	start := p.i
	for !p.eof() && isDigit(p.s[p.i]) {
		p.i++
	}
	n, err := strconv.Atoi(p.s[start:p.i])
	if err != nil {
		return 0, &SyntaxError{Message: "invalid integer", Position: start}
	}
	return n, nil
}

func (p *parser) expectCall() *SyntaxError {
	if err := p.expectByte('('); err != nil {
		return err
	}
	p.skipSpace()
	return p.expectByte(')')
}

func (p *parser) expectByte(c byte) *SyntaxError {
	if p.eof() || p.s[p.i] != c {
		return p.err(fmt.Sprintf("expected '%c'", c))
	}
	p.i++
	return nil
}

func (p *parser) skipSpace() {
	for !p.eof() && isSpace(p.s[p.i]) {
		p.i++
	}
}

func (p *parser) eof() bool { return p.i >= len(p.s) }

func (p *parser) startsWith(s string) bool {
	return strings.HasPrefix(p.s[p.i:], s)
}

func (p *parser) identContAt(i int) bool {
	return i < len(p.s) && isIdentCont(p.s[i])
}

// identContAtExceptHyphen reports whether s[i] continues an identifier.
// Hyphen is excluded so script expressions like @.length-1 can use subtraction.
func (p *parser) identContAtExceptHyphen(i int) bool {
	return i < len(p.s) && p.s[i] != '-' && isIdentCont(p.s[i])
}

func (p *parser) err(msg string) *SyntaxError {
	return &SyntaxError{Message: msg, Position: p.i}
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

func isIdentCont(c byte) bool {
	return isIdentStart(c) || isDigit(c) || c == '-'
}
