// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package orderedmap

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// SyntaxError is returned by Query and QueryOne when path is not valid JSONPath.
type SyntaxError struct {
	Message  string
	Position int
}

// Error formats the syntax error as "syntax error at position {Position}: {Message}".
func (e *SyntaxError) Error() string {
	if e == nil {
		return "syntax error at position 0: "
	}
	return fmt.Sprintf("syntax error at position %d: %s", e.Position, e.Message)
}

// Query evaluates path against doc and returns every match.
// An empty result is a non-nil empty slice. Invalid syntax returns *SyntaxError.
// Selectors that do not apply to a value (for example an index on a map) produce
// no matches and are not errors.
func Query(doc interface{}, path string) ([]interface{}, error) {
	segs, err := parsePath(path)
	if err != nil {
		return nil, err
	}
	cur := []interface{}{doc}
	for _, seg := range segs {
		next := make([]interface{}, 0)
		for _, node := range cur {
			next = append(next, seg.eval(node)...)
		}
		cur = next
	}
	if len(cur) == 0 {
		return []interface{}{}, nil
	}
	return cur, nil
}

// QueryOne returns the first match from Query.
// When nothing matches, the result is (nil, false, nil).
func QueryOne(doc interface{}, path string) (interface{}, bool, error) {
	results, err := Query(doc, path)
	if err != nil {
		return nil, false, err
	}
	if len(results) == 0 {
		return nil, false, nil
	}
	return results[0], true, nil
}

type segment interface {
	eval(node interface{}) []interface{}
}

type keySegment struct{ keys []string }

func (s keySegment) eval(node interface{}) []interface{} {
	var out []interface{}
	for _, key := range s.keys {
		if v, ok := childByKey(node, key); ok {
			out = append(out, v)
		}
	}
	return out
}

type indexSegment struct{ indexes []int }

func (s indexSegment) eval(node interface{}) []interface{} {
	arr, ok := asArray(node)
	if !ok {
		return nil
	}
	var out []interface{}
	for _, idx := range s.indexes {
		if v, ok := indexAt(arr, idx); ok {
			out = append(out, v)
		}
	}
	return out
}

type wildcardSegment struct{}

func (wildcardSegment) eval(node interface{}) []interface{} {
	return childValues(node)
}

type lengthSegment struct{}

func (lengthSegment) eval(node interface{}) []interface{} {
	n, ok := lengthOf(node)
	if !ok {
		return nil
	}
	return []interface{}{n}
}

type filterSegment struct{ e expr }

func (s filterSegment) eval(node interface{}) []interface{} {
	var out []interface{}
	eachValue(node, func(v interface{}) {
		if s.e.match(v) {
			out = append(out, v)
		}
	})
	return out
}

type scriptSegment struct{ sub int }

func (s scriptSegment) eval(node interface{}) []interface{} {
	arr, ok := asArray(node)
	if !ok {
		return nil
	}
	idx := len(arr) - s.sub
	if idx < 0 || idx >= len(arr) {
		return nil
	}
	return []interface{}{arr[idx]}
}

// recursiveSegment applies inner at the current node, then at every descendant.
// Children of the current node are visited before their own descendants.
type recursiveSegment struct{ inner segment }

func (s recursiveSegment) eval(node interface{}) []interface{} {
	out := append([]interface{}{}, s.inner.eval(node)...)
	forEachChild(node, func(_, v interface{}) {
		out = append(out, s.eval(v)...)
	})
	return out
}

// recursiveAllSegment is `..*` / `..[*]`: the node itself, then every descendant, preorder.
type recursiveAllSegment struct{}

func (recursiveAllSegment) eval(node interface{}) []interface{} {
	out := []interface{}{node}
	forEachChild(node, func(_, v interface{}) {
		out = append(out, recursiveAllSegment{}.eval(v)...)
	})
	return out
}

type expr interface {
	match(ctx interface{}) bool
}

type pathExpr struct{ steps []step }

type stepKind int

const (
	stepKey stepKind = iota
	stepIndex
	stepLength
)

type step struct {
	kind  stepKind
	key   string
	index int
}

func (e pathExpr) eval(ctx interface{}) interface{} {
	cur := ctx
	for _, st := range e.steps {
		switch st.kind {
		case stepKey:
			v, ok := childByKey(cur, st.key)
			if !ok {
				return nil
			}
			cur = v
		case stepIndex:
			arr, ok := asArray(cur)
			if !ok {
				return nil
			}
			v, ok := indexAt(arr, st.index)
			if !ok {
				return nil
			}
			cur = v
		case stepLength:
			n, ok := lengthOf(cur)
			if !ok {
				return nil
			}
			cur = n
		}
	}
	return cur
}

func (e pathExpr) match(ctx interface{}) bool {
	// A missing path is nil, which is falsy — same as an explicit null.
	return isTruthy(e.eval(ctx))
}

type cmpExpr struct {
	left  pathExpr
	op    string
	right interface{}
}

func (e cmpExpr) match(ctx interface{}) bool {
	return compareValues(e.left.eval(ctx), e.right, e.op)
}

type andExpr struct{ left, right expr }

func (e andExpr) match(ctx interface{}) bool {
	return e.left.match(ctx) && e.right.match(ctx)
}

type orExpr struct{ left, right expr }

func (e orExpr) match(ctx interface{}) bool {
	return e.left.match(ctx) || e.right.match(ctx)
}

func compareValues(left, right interface{}, op string) bool {
	// Missing paths evaluate to nil, so they compare equal to null.
	eq := valuesEqual(left, right)
	lt := valuesLess(left, right)
	switch op {
	case "==":
		return eq
	case "!=":
		return !eq
	case "<":
		return lt
	case "<=":
		return lt || eq
	case ">":
		return valuesLess(right, left)
	case ">=":
		return valuesLess(right, left) || eq
	default:
		return false
	}
}

func valuesEqual(a, b interface{}) bool {
	an, aok := numberOf(a)
	bn, bok := numberOf(b)
	if aok || bok {
		if !aok || !bok {
			return false
		}
		if an.isInt && bn.isInt {
			return an.i == bn.i
		}
		return an.f == bn.f
	}
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return reflect.DeepEqual(a, b)
}

func valuesLess(a, b interface{}) bool {
	an, aok := numberOf(a)
	bn, bok := numberOf(b)
	if aok && bok {
		if an.isInt && bn.isInt {
			return an.i < bn.i
		}
		return an.f < bn.f
	}
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok && bok {
		return as < bs
	}
	return false
}

type number struct {
	i     int64
	f     float64
	isInt bool
}

func numberOf(v interface{}) (number, bool) {
	switch n := v.(type) {
	case int:
		return number{i: int64(n), f: float64(n), isInt: true}, true
	case int8:
		return number{i: int64(n), f: float64(n), isInt: true}, true
	case int16:
		return number{i: int64(n), f: float64(n), isInt: true}, true
	case int32:
		return number{i: int64(n), f: float64(n), isInt: true}, true
	case int64:
		return number{i: n, f: float64(n), isInt: true}, true
	case uint:
		return number{i: int64(n), f: float64(n), isInt: true}, true
	case uint8:
		return number{i: int64(n), f: float64(n), isInt: true}, true
	case uint16:
		return number{i: int64(n), f: float64(n), isInt: true}, true
	case uint32:
		return number{i: int64(n), f: float64(n), isInt: true}, true
	case uint64:
		if int64(n) < 0 {
			return number{f: float64(n)}, true
		}
		return number{i: int64(n), f: float64(n), isInt: true}, true
	case float32:
		return number{f: float64(n)}, true
	case float64:
		return number{f: n}, true
	default:
		return number{}, false
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
		return t != nil && t.Len() != 0
	case []interface{}:
		return len(t) != 0
	case map[string]interface{}:
		return len(t) != 0
	case map[interface{}]interface{}:
		return len(t) != 0
	}
	if n, ok := numberOf(v); ok {
		if n.isInt {
			return n.i != 0
		}
		return n.f != 0
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return rv.Len() != 0
	case reflect.Ptr, reflect.Interface:
		return !rv.IsNil()
	default:
		return true
	}
}

// lengthOf reports the length of arrays, maps, and strings.
// The result is a Go int. Strings are counted in Unicode code points.
// Scalars other than strings do not have a length.
func lengthOf(v interface{}) (int, bool) {
	switch t := v.(type) {
	case string:
		return utf8.RuneCountInString(t), true
	case []interface{}:
		return len(t), true
	case *Map:
		if t == nil {
			return 0, false
		}
		return t.Len(), true
	case map[string]interface{}:
		return len(t), true
	case map[interface{}]interface{}:
		return len(t), true
	default:
		rv := reflect.ValueOf(v)
		if !rv.IsValid() {
			return 0, false
		}
		switch rv.Kind() {
		case reflect.String:
			return utf8.RuneCountInString(rv.String()), true
		case reflect.Slice, reflect.Array, reflect.Map:
			return rv.Len(), true
		default:
			return 0, false
		}
	}
}

func asArray(node interface{}) ([]interface{}, bool) {
	if arr, ok := node.([]interface{}); ok {
		return arr, true
	}
	rv := reflect.ValueOf(node)
	if !rv.IsValid() {
		return nil, false
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		out := make([]interface{}, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out[i] = ifaceOf(rv.Index(i))
		}
		return out, true
	default:
		return nil, false
	}
}

func indexAt(arr []interface{}, idx int) (interface{}, bool) {
	if idx < 0 {
		idx += len(arr)
	}
	if idx < 0 || idx >= len(arr) {
		return nil, false
	}
	return arr[idx], true
}

func childByKey(node interface{}, key string) (interface{}, bool) {
	switch n := node.(type) {
	case *Map:
		if n == nil {
			return nil, false
		}
		return n.Get(key)
	case map[string]interface{}:
		v, ok := n[key]
		return v, ok
	case map[interface{}]interface{}:
		v, ok := n[key]
		return v, ok
	default:
		return mapIndex(node, key)
	}
}

func mapIndex(node interface{}, key string) (interface{}, bool) {
	rv := reflect.ValueOf(node)
	if !rv.IsValid() || rv.Kind() != reflect.Map || rv.IsNil() {
		return nil, false
	}
	kv := reflect.ValueOf(key)
	if !kv.Type().AssignableTo(rv.Type().Key()) {
		return nil, false
	}
	mv := rv.MapIndex(kv)
	if !mv.IsValid() {
		return nil, false
	}
	return ifaceOf(mv), true
}

func forEachChild(node interface{}, fn func(key, value interface{})) {
	switch n := node.(type) {
	case *Map:
		if n == nil {
			return
		}
		n.Iterate(fn)
	case map[string]interface{}:
		for _, k := range sortedStringKeys(n) {
			fn(k, n[k])
		}
	case map[interface{}]interface{}:
		keys := make([]interface{}, 0, len(n))
		for k := range n {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			return fmt.Sprint(keys[i]) < fmt.Sprint(keys[j])
		})
		for _, k := range keys {
			fn(k, n[k])
		}
	case []interface{}:
		for i, v := range n {
			fn(i, v)
		}
	default:
		rv := reflect.ValueOf(node)
		if !rv.IsValid() {
			return
		}
		switch rv.Kind() {
		case reflect.Slice, reflect.Array:
			for i := 0; i < rv.Len(); i++ {
				fn(i, ifaceOf(rv.Index(i)))
			}
		case reflect.Map:
			if rv.IsNil() {
				return
			}
			keys := rv.MapKeys()
			sort.Slice(keys, func(i, j int) bool {
				return fmt.Sprint(ifaceOf(keys[i])) < fmt.Sprint(ifaceOf(keys[j]))
			})
			for _, k := range keys {
				fn(ifaceOf(k), ifaceOf(rv.MapIndex(k)))
			}
		}
	}
}

func ifaceOf(v reflect.Value) interface{} {
	if !v.IsValid() || !v.CanInterface() {
		return nil
	}
	return v.Interface()
}

func eachValue(node interface{}, fn func(interface{})) {
	forEachChild(node, func(_, v interface{}) { fn(v) })
}

func childValues(node interface{}) []interface{} {
	var out []interface{}
	forEachChild(node, func(_, v interface{}) {
		out = append(out, v)
	})
	return out
}

func sortedStringKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type parser struct {
	s string
	i int
}

func parsePath(s string) ([]segment, error) {
	if len(s) == 0 || s[0] != '$' {
		return nil, &SyntaxError{Message: "path must start with '$'", Position: 0}
	}
	p := &parser{s: s, i: 1}
	var segs []segment
	for {
		p.skipWS()
		if p.eof() {
			return segs, nil
		}
		seg, err := p.parseSegment()
		if err != nil {
			return nil, err
		}
		segs = append(segs, seg)
	}
}

func (p *parser) parseSegment() (segment, error) {
	if p.eof() {
		return nil, p.err("unexpected end of path")
	}
	switch p.s[p.i] {
	case '.':
		p.i++
		if !p.eof() && p.s[p.i] == '.' {
			p.i++
			return p.parseRecursive()
		}
		return p.parseDot()
	case '[':
		return p.parseBracket(false)
	default:
		return nil, p.err(fmt.Sprintf("unexpected character '%c'", p.s[p.i]))
	}
}

func (p *parser) parseRecursive() (segment, error) {
	p.skipWS()
	if p.eof() {
		return nil, p.err("expected selector after '..'")
	}
	switch p.s[p.i] {
	case '*':
		p.i++
		return recursiveAllSegment{}, nil
	case '[':
		return p.parseBracket(true)
	default:
		name, err := p.parseIdent()
		if err != nil {
			return nil, err
		}
		p.skipWS()
		if !p.eof() && p.s[p.i] == '(' {
			return nil, p.err("unexpected '('")
		}
		return recursiveSegment{inner: keySegment{keys: []string{name}}}, nil
	}
}

func (p *parser) parseDot() (segment, error) {
	p.skipWS()
	if p.eof() {
		return nil, p.err("expected identifier")
	}
	if p.s[p.i] == '*' {
		p.i++
		return wildcardSegment{}, nil
	}
	name, err := p.parseIdent()
	if err != nil {
		return nil, err
	}
	p.skipWS()
	if !p.eof() && p.s[p.i] == '(' {
		if name != "length" {
			return nil, p.err("unknown function '" + name + "'")
		}
		p.i++
		if err := p.want(')'); err != nil {
			return nil, err
		}
		return lengthSegment{}, nil
	}
	return keySegment{keys: []string{name}}, nil
}

func (p *parser) parseBracket(recursive bool) (segment, error) {
	if err := p.want('['); err != nil {
		return nil, err
	}
	p.skipWS()
	if p.eof() {
		return nil, p.err("unterminated '['")
	}
	var inner segment
	switch p.s[p.i] {
	case '?':
		p.i++
		ex, err := p.parseFilter()
		if err != nil {
			return nil, err
		}
		inner = filterSegment{e: ex}
	case '(':
		sc, err := p.parseScript()
		if err != nil {
			return nil, err
		}
		inner = sc
	case '*':
		p.i++
		if err := p.want(']'); err != nil {
			return nil, err
		}
		if recursive {
			return recursiveAllSegment{}, nil
		}
		return wildcardSegment{}, nil
	case '\'', '"':
		keys, err := p.parseStringUnion()
		if err != nil {
			return nil, err
		}
		inner = keySegment{keys: keys}
	default:
		idxs, err := p.parseIndexUnion()
		if err != nil {
			return nil, err
		}
		inner = indexSegment{indexes: idxs}
	}
	if err := p.want(']'); err != nil {
		return nil, err
	}
	if recursive {
		return recursiveSegment{inner: inner}, nil
	}
	return inner, nil
}

func (p *parser) parseStringUnion() ([]string, error) {
	var keys []string
	for {
		s, err := p.parseString()
		if err != nil {
			return nil, err
		}
		keys = append(keys, s)
		p.skipWS()
		if !p.eof() && p.s[p.i] == ',' {
			p.i++
			continue
		}
		break
	}
	return keys, nil
}

func (p *parser) parseIndexUnion() ([]int, error) {
	var idxs []int
	for {
		n, err := p.parseSignedInt()
		if err != nil {
			return nil, err
		}
		idxs = append(idxs, n)
		p.skipWS()
		if !p.eof() && p.s[p.i] == ',' {
			p.i++
			continue
		}
		break
	}
	return idxs, nil
}

func (p *parser) parseScript() (scriptSegment, error) {
	if err := p.want('('); err != nil {
		return scriptSegment{}, err
	}
	if err := p.want('@'); err != nil {
		return scriptSegment{}, err
	}
	if err := p.want('.'); err != nil {
		return scriptSegment{}, err
	}
	p.skipWS()
	if !p.consumeLiteral("length") {
		return scriptSegment{}, p.err("expected 'length'")
	}
	p.skipWS()
	// length and length() are the same current-node length.
	if !p.eof() && p.s[p.i] == '(' {
		p.i++
		if err := p.want(')'); err != nil {
			return scriptSegment{}, err
		}
		p.skipWS()
	}
	if p.eof() || p.s[p.i] != '-' {
		return scriptSegment{}, p.err("expected '-'")
	}
	p.i++
	n, err := p.parseInt()
	if err != nil {
		return scriptSegment{}, err
	}
	if err := p.want(')'); err != nil {
		return scriptSegment{}, err
	}
	return scriptSegment{sub: n}, nil
}

func (p *parser) parseFilter() (expr, error) {
	if err := p.want('('); err != nil {
		return nil, err
	}
	ex, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if err := p.want(')'); err != nil {
		return nil, err
	}
	return ex, nil
}

func (p *parser) parseOr() (expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.consumeExact("||") {
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = orExpr{left, right}
	}
	return left, nil
}

func (p *parser) parseAnd() (expr, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for p.consumeExact("&&") {
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		left = andExpr{left, right}
	}
	return left, nil
}

func (p *parser) parsePrimary() (expr, error) {
	p.skipWS()
	if p.atKeyword("length") {
		saved := p.i
		p.i += len("length")
		p.skipWS()
		if !p.eof() && p.s[p.i] == '(' {
			p.i++
			inner, err := p.parseFilterPath()
			if err != nil {
				return nil, err
			}
			if err := p.want(')'); err != nil {
				return nil, err
			}
			inner.steps = append(inner.steps, step{kind: stepLength})
			return p.finishComparison(inner)
		}
		p.i = saved
	}
	if !p.eof() && p.s[p.i] == '(' {
		p.i++
		ex, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if err := p.want(')'); err != nil {
			return nil, err
		}
		return ex, nil
	}
	pe, err := p.parseFilterPath()
	if err != nil {
		return nil, err
	}
	return p.finishComparison(pe)
}

func (p *parser) finishComparison(left pathExpr) (expr, error) {
	if op, ok := p.peekCmpOp(); ok {
		p.i += len(op)
		lit, err := p.parseLiteral()
		if err != nil {
			return nil, err
		}
		return cmpExpr{left: left, op: op, right: lit}, nil
	}
	return left, nil
}

func (p *parser) parseFilterPath() (pathExpr, error) {
	if err := p.want('@'); err != nil {
		return pathExpr{}, err
	}
	var steps []step
	for {
		p.skipWS()
		if p.eof() {
			break
		}
		switch p.s[p.i] {
		case '.':
			p.i++
			p.skipWS()
			if p.eof() {
				return pathExpr{}, p.err("expected identifier")
			}
			if p.s[p.i] == '*' {
				return pathExpr{}, p.err("wildcard is not allowed in a filter path")
			}
			name, err := p.parseIdent()
			if err != nil {
				return pathExpr{}, err
			}
			p.skipWS()
			if name == "length" && !p.eof() && p.s[p.i] == '(' {
				p.i++
				if err := p.want(')'); err != nil {
					return pathExpr{}, err
				}
				steps = append(steps, step{kind: stepLength})
				continue
			}
			steps = append(steps, step{kind: stepKey, key: name})
		case '[':
			p.i++
			p.skipWS()
			if p.eof() {
				return pathExpr{}, p.err("unterminated '['")
			}
			if p.s[p.i] == '\'' || p.s[p.i] == '"' {
				s, err := p.parseString()
				if err != nil {
					return pathExpr{}, err
				}
				if err := p.want(']'); err != nil {
					return pathExpr{}, err
				}
				steps = append(steps, step{kind: stepKey, key: s})
				continue
			}
			idx, err := p.parseSignedInt()
			if err != nil {
				return pathExpr{}, err
			}
			if err := p.want(']'); err != nil {
				return pathExpr{}, err
			}
			steps = append(steps, step{kind: stepIndex, index: idx})
		default:
			return pathExpr{steps: steps}, nil
		}
	}
	return pathExpr{steps: steps}, nil
}

func (p *parser) parseLiteral() (interface{}, error) {
	p.skipWS()
	if p.eof() {
		return nil, p.err("expected value")
	}
	c := p.s[p.i]
	if c == '\'' || c == '"' {
		return p.parseString()
	}
	if c == '-' || isDigit(c) {
		return p.parseNumber()
	}
	switch {
	case p.consumeKeyword("true"):
		return true, nil
	case p.consumeKeyword("false"):
		return false, nil
	case p.consumeKeyword("null"):
		return nil, nil
	default:
		return nil, p.err("expected value")
	}
}

func (p *parser) parseNumber() (interface{}, error) {
	p.skipWS()
	start := p.i
	if !p.eof() && p.s[p.i] == '-' {
		p.i++
	}
	if p.eof() || !isDigit(p.s[p.i]) {
		return nil, &SyntaxError{Message: "expected number", Position: start}
	}
	for !p.eof() && isDigit(p.s[p.i]) {
		p.i++
	}
	isFloat := false
	if !p.eof() && p.s[p.i] == '.' && p.i+1 < len(p.s) && isDigit(p.s[p.i+1]) {
		isFloat = true
		p.i++
		for !p.eof() && isDigit(p.s[p.i]) {
			p.i++
		}
	}
	if !p.eof() && (p.s[p.i] == 'e' || p.s[p.i] == 'E') {
		isFloat = true
		p.i++
		if !p.eof() && (p.s[p.i] == '+' || p.s[p.i] == '-') {
			p.i++
		}
		expStart := p.i
		for !p.eof() && isDigit(p.s[p.i]) {
			p.i++
		}
		if p.i == expStart {
			return nil, &SyntaxError{Message: "expected exponent", Position: expStart}
		}
	}
	tok := p.s[start:p.i]
	if isFloat {
		f, err := strconv.ParseFloat(tok, 64)
		if err != nil {
			return nil, &SyntaxError{Message: "invalid number", Position: start}
		}
		return f, nil
	}
	v, err := strconv.ParseInt(tok, 10, 64)
	if err != nil {
		return nil, &SyntaxError{Message: "invalid number", Position: start}
	}
	return v, nil
}

func (p *parser) parseString() (string, error) {
	p.skipWS()
	if p.eof() || (p.s[p.i] != '\'' && p.s[p.i] != '"') {
		return "", p.err("expected string")
	}
	quote := p.s[p.i]
	p.i++
	var b strings.Builder
	for {
		if p.eof() {
			return "", p.err("unterminated string")
		}
		c := p.s[p.i]
		if c == quote {
			p.i++
			return b.String(), nil
		}
		if c == '\n' || c == '\r' {
			return "", p.err("unterminated string")
		}
		if c == '\\' {
			p.i++
			r, err := p.parseEscape()
			if err != nil {
				return "", err
			}
			b.WriteRune(r)
			continue
		}
		b.WriteByte(c)
		p.i++
	}
}

func (p *parser) parseEscape() (rune, error) {
	if p.eof() {
		return 0, p.err("unterminated escape")
	}
	c := p.s[p.i]
	p.i++
	switch c {
	case '\\', '/', '\'', '"':
		return rune(c), nil
	case 'b':
		return '\b', nil
	case 'f':
		return '\f', nil
	case 'n':
		return '\n', nil
	case 'r':
		return '\r', nil
	case 't':
		return '\t', nil
	case 'u':
		if p.i+4 > len(p.s) {
			return 0, p.err("invalid unicode escape")
		}
		hex := p.s[p.i : p.i+4]
		p.i += 4
		v, err := strconv.ParseUint(hex, 16, 32)
		if err != nil {
			return 0, p.err("invalid unicode escape")
		}
		return rune(v), nil
	default:
		return 0, p.err("invalid escape")
	}
}

func (p *parser) parseIdent() (string, error) {
	p.skipWS()
	start := p.i
	if p.eof() || !isIdentChar(p.s[p.i]) {
		return "", p.err("expected identifier")
	}
	for !p.eof() && isIdentChar(p.s[p.i]) {
		p.i++
	}
	return p.s[start:p.i], nil
}

func (p *parser) parseSignedInt() (int, error) {
	p.skipWS()
	start := p.i
	sign := 1
	if !p.eof() && p.s[p.i] == '-' {
		sign = -1
		p.i++
		p.skipWS()
	}
	if p.eof() || !isDigit(p.s[p.i]) {
		return 0, &SyntaxError{Message: "expected integer", Position: start}
	}
	numStart := p.i
	for !p.eof() && isDigit(p.s[p.i]) {
		p.i++
	}
	v, err := strconv.ParseInt(p.s[numStart:p.i], 10, strconv.IntSize)
	if err != nil {
		return 0, &SyntaxError{Message: "invalid integer", Position: start}
	}
	return sign * int(v), nil
}

func (p *parser) parseInt() (int, error) {
	p.skipWS()
	start := p.i
	if p.eof() || !isDigit(p.s[p.i]) {
		return 0, &SyntaxError{Message: "expected integer", Position: start}
	}
	for !p.eof() && isDigit(p.s[p.i]) {
		p.i++
	}
	v, err := strconv.ParseInt(p.s[start:p.i], 10, strconv.IntSize)
	if err != nil {
		return 0, &SyntaxError{Message: "invalid integer", Position: start}
	}
	return int(v), nil
}

func (p *parser) peekCmpOp() (string, bool) {
	p.skipWS()
	rest := p.s[p.i:]
	for _, op := range []string{"==", "!=", "<=", ">=", "<", ">"} {
		if strings.HasPrefix(rest, op) {
			return op, true
		}
	}
	return "", false
}

func (p *parser) consumeExact(tok string) bool {
	p.skipWS()
	if strings.HasPrefix(p.s[p.i:], tok) {
		p.i += len(tok)
		return true
	}
	return false
}

func (p *parser) atKeyword(kw string) bool {
	if p.i+len(kw) > len(p.s) || p.s[p.i:p.i+len(kw)] != kw {
		return false
	}
	end := p.i + len(kw)
	if end < len(p.s) && isIdentChar(p.s[end]) {
		return false
	}
	return true
}

func (p *parser) consumeKeyword(kw string) bool {
	if !p.atKeyword(kw) {
		return false
	}
	p.i += len(kw)
	return true
}

func (p *parser) consumeLiteral(kw string) bool {
	if p.i+len(kw) > len(p.s) || p.s[p.i:p.i+len(kw)] != kw {
		return false
	}
	p.i += len(kw)
	return true
}

func (p *parser) want(c byte) error {
	p.skipWS()
	if !p.eof() && p.s[p.i] == c {
		p.i++
		return nil
	}
	return p.err(fmt.Sprintf("expected '%c'", c))
}

func (p *parser) skipWS() {
	for !p.eof() {
		switch p.s[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *parser) eof() bool { return p.i >= len(p.s) }

func (p *parser) err(msg string) error {
	pos := p.i
	if pos > len(p.s) {
		pos = len(p.s)
	}
	return &SyntaxError{Message: msg, Position: pos}
}

func isIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
