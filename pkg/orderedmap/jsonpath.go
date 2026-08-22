// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package orderedmap

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
)

// SyntaxError is returned when a JSONPath expression cannot be parsed.
type SyntaxError struct {
	Message  string
	Position int
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("syntax error at position %d: %s", e.Position, e.Message)
}

// Query evaluates a JSONPath expression against doc and returns every match.
// A path that matches nothing returns an empty (non-nil) slice.
func Query(doc interface{}, path string) ([]interface{}, error) {
	segs, err := parsePath(path)
	if err != nil {
		return nil, err
	}
	cur := []interface{}{doc}
	for _, s := range segs {
		cur = s.apply(cur)
	}
	if cur == nil {
		cur = []interface{}{}
	}
	return cur, nil
}

// QueryOne evaluates a JSONPath expression and returns the first match.
// When nothing matches it returns (nil, false, nil).
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
	apply(nodes []interface{}) []interface{}
}

type childKey struct{ key string }

type childIndex struct{ index int }

type childUnionKeys struct{ keys []string }

type childUnionIndices struct{ indices []int }

type childWildcard struct{}

type lengthSeg struct{}

type scriptIndex struct{ n int }

type filterSeg struct {
	expr      filterExpr
	recursive bool
}

type recursiveKey struct{ key string }

type recursiveUnionKeys struct{ keys []string }

type recursiveWildcard struct{}

type recursiveIndex struct{ index int }

type recursiveUnionIndices struct{ indices []int }

type recursiveLength struct{}

type recursiveScriptIndex struct{ n int }

func (s childKey) apply(nodes []interface{}) []interface{} {
	return mapNodes(nodes, func(n interface{}) []interface{} {
		if m, ok := asMap(n); ok {
			if v, found := m.get(s.key); found {
				return []interface{}{v}
			}
		}
		return nil
	})
}

func (s childIndex) apply(nodes []interface{}) []interface{} {
	return mapNodes(nodes, func(n interface{}) []interface{} {
		arr, ok := asArray(n)
		if !ok {
			return nil
		}
		i, ok := resolveIndex(s.index, len(arr))
		if !ok {
			return nil
		}
		return []interface{}{arr[i]}
	})
}

func (s childUnionKeys) apply(nodes []interface{}) []interface{} {
	return mapNodes(nodes, func(n interface{}) []interface{} {
		m, ok := asMap(n)
		if !ok {
			return nil
		}
		var out []interface{}
		for _, k := range s.keys {
			if v, found := m.get(k); found {
				out = append(out, v)
			}
		}
		return out
	})
}

func (s childUnionIndices) apply(nodes []interface{}) []interface{} {
	return mapNodes(nodes, func(n interface{}) []interface{} {
		arr, ok := asArray(n)
		if !ok {
			return nil
		}
		var out []interface{}
		for _, raw := range s.indices {
			i, ok := resolveIndex(raw, len(arr))
			if ok {
				out = append(out, arr[i])
			}
		}
		return out
	})
}

func (childWildcard) apply(nodes []interface{}) []interface{} {
	return mapNodes(nodes, func(n interface{}) []interface{} {
		if arr, ok := asArray(n); ok {
			return append([]interface{}{}, arr...)
		}
		if m, ok := asMap(n); ok {
			return append([]interface{}{}, m.vals...)
		}
		return nil
	})
}

func (lengthSeg) apply(nodes []interface{}) []interface{} {
	return mapNodes(nodes, func(n interface{}) []interface{} {
		if l, ok := lengthOf(n); ok {
			return []interface{}{l}
		}
		return nil
	})
}

func (s scriptIndex) apply(nodes []interface{}) []interface{} {
	return mapNodes(nodes, func(n interface{}) []interface{} {
		arr, ok := asArray(n)
		if !ok {
			return nil
		}
		i := len(arr) - s.n
		if i < 0 || i >= len(arr) {
			return nil
		}
		return []interface{}{arr[i]}
	})
}

func (s filterSeg) apply(nodes []interface{}) []interface{} {
	out := make([]interface{}, 0)
	if s.recursive {
		for _, n := range nodes {
			walkNodes(n, func(x interface{}) {
				if s.expr.eval(x) {
					out = append(out, x)
				}
			})
		}
		return out
	}
	for _, n := range nodes {
		if arr, ok := asArray(n); ok {
			for _, elem := range arr {
				if s.expr.eval(elem) {
					out = append(out, elem)
				}
			}
			continue
		}
		if s.expr.eval(n) {
			out = append(out, n)
		}
	}
	return out
}

func (s recursiveKey) apply(nodes []interface{}) []interface{} {
	out := make([]interface{}, 0)
	for _, n := range nodes {
		walkNodes(n, func(x interface{}) {
			if m, ok := asMap(x); ok {
				if v, found := m.get(s.key); found {
					out = append(out, v)
				}
			}
		})
	}
	return out
}

func (s recursiveUnionKeys) apply(nodes []interface{}) []interface{} {
	out := make([]interface{}, 0)
	for _, n := range nodes {
		walkNodes(n, func(x interface{}) {
			if m, ok := asMap(x); ok {
				for _, k := range s.keys {
					if v, found := m.get(k); found {
						out = append(out, v)
					}
				}
			}
		})
	}
	return out
}

func (recursiveWildcard) apply(nodes []interface{}) []interface{} {
	out := make([]interface{}, 0)
	for _, n := range nodes {
		walkNodes(n, func(x interface{}) {
			out = append(out, x)
		})
	}
	return out
}

func (s recursiveIndex) apply(nodes []interface{}) []interface{} {
	out := make([]interface{}, 0)
	for _, n := range nodes {
		walkNodes(n, func(x interface{}) {
			arr, ok := asArray(x)
			if !ok {
				return
			}
			i, ok := resolveIndex(s.index, len(arr))
			if ok {
				out = append(out, arr[i])
			}
		})
	}
	return out
}

func (s recursiveUnionIndices) apply(nodes []interface{}) []interface{} {
	out := make([]interface{}, 0)
	for _, n := range nodes {
		walkNodes(n, func(x interface{}) {
			arr, ok := asArray(x)
			if !ok {
				return
			}
			for _, raw := range s.indices {
				i, ok := resolveIndex(raw, len(arr))
				if ok {
					out = append(out, arr[i])
				}
			}
		})
	}
	return out
}

func (recursiveLength) apply(nodes []interface{}) []interface{} {
	out := make([]interface{}, 0)
	for _, n := range nodes {
		walkNodes(n, func(x interface{}) {
			if l, ok := lengthOf(x); ok {
				out = append(out, l)
			}
		})
	}
	return out
}

func (s recursiveScriptIndex) apply(nodes []interface{}) []interface{} {
	out := make([]interface{}, 0)
	for _, n := range nodes {
		walkNodes(n, func(x interface{}) {
			arr, ok := asArray(x)
			if !ok {
				return
			}
			i := len(arr) - s.n
			if i >= 0 && i < len(arr) {
				out = append(out, arr[i])
			}
		})
	}
	return out
}

func mapNodes(nodes []interface{}, fn func(interface{}) []interface{}) []interface{} {
	out := make([]interface{}, 0)
	for _, n := range nodes {
		out = append(out, fn(n)...)
	}
	return out
}

func walkNodes(n interface{}, visit func(interface{})) {
	visit(n)
	if arr, ok := asArray(n); ok {
		for _, elem := range arr {
			walkNodes(elem, visit)
		}
		return
	}
	if m, ok := asMap(n); ok {
		for _, v := range m.vals {
			walkNodes(v, visit)
		}
	}
}

func resolveIndex(idx, length int) (int, bool) {
	if idx < 0 {
		idx = length + idx
	}
	if idx < 0 || idx >= length {
		return 0, false
	}
	return idx, true
}

type filterExpr interface {
	eval(node interface{}) bool
}

type orExpr struct{ left, right filterExpr }

type andExpr struct{ left, right filterExpr }

type cmpExpr struct {
	left  valueSrc
	op    string
	right valueSrc
}

type truthyExpr struct{ src valueSrc }

func (e orExpr) eval(node interface{}) bool {
	return e.left.eval(node) || e.right.eval(node)
}

func (e andExpr) eval(node interface{}) bool {
	return e.left.eval(node) && e.right.eval(node)
}

func (e cmpExpr) eval(node interface{}) bool {
	lv, _ := e.left.resolve(node)
	rv, _ := e.right.resolve(node)
	switch e.op {
	case "==":
		return valuesEqual(lv, rv)
	case "!=":
		return !valuesEqual(lv, rv)
	case "<", ">", "<=", ">=":
		return compareOrdered(e.op, lv, rv)
	default:
		return false
	}
}

func (e truthyExpr) eval(node interface{}) bool {
	v, ok := e.src.resolve(node)
	if !ok {
		return false
	}
	return isTruthy(v)
}

type valueSrc interface {
	resolve(node interface{}) (interface{}, bool)
}

type literalVal struct{ v interface{} }

type pathVal struct{ steps []filterStep }

func (l literalVal) resolve(interface{}) (interface{}, bool) {
	return l.v, true
}

func (p pathVal) resolve(node interface{}) (interface{}, bool) {
	cur := node
	for _, step := range p.steps {
		next, ok := step.apply(cur)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

type filterStep interface {
	apply(node interface{}) (interface{}, bool)
}

type filterKey struct{ key string }

type filterIndex struct{ index int }

type filterLength struct{}

func (s filterKey) apply(node interface{}) (interface{}, bool) {
	m, ok := asMap(node)
	if !ok {
		return nil, false
	}
	return m.get(s.key)
}

func (s filterIndex) apply(node interface{}) (interface{}, bool) {
	arr, ok := asArray(node)
	if !ok {
		return nil, false
	}
	i, ok := resolveIndex(s.index, len(arr))
	if !ok {
		return nil, false
	}
	return arr[i], true
}

func (filterLength) apply(node interface{}) (interface{}, bool) {
	l, ok := lengthOf(node)
	if !ok {
		return nil, false
	}
	return l, true
}

type parser struct {
	path string
	i    int
}

func parsePath(path string) ([]segment, error) {
	p := &parser{path: path}
	if p.atEnd() || p.peek() != '$' {
		return nil, p.err(0, "path must start with '$'")
	}
	p.i++
	var segs []segment
	for {
		p.skipSpace()
		if p.atEnd() {
			break
		}
		seg, err := p.parseSegment()
		if err != nil {
			return nil, err
		}
		segs = append(segs, seg)
	}
	return segs, nil
}

func (p *parser) parseSegment() (segment, error) {
	switch p.peek() {
	case '.':
		return p.parseDot()
	case '[':
		return p.parseBracket(false)
	default:
		return nil, p.err(p.i, "expected '.' or '['")
	}
}

func (p *parser) parseDot() (segment, error) {
	p.i++ // '.'
	if p.peek() == '.' {
		p.i++
		return p.parseRecursive()
	}
	if p.peek() == '*' {
		p.i++
		return childWildcard{}, nil
	}
	ident, err := p.parseIdentifier()
	if err != nil {
		return nil, err
	}
	if ident == "length" && p.tryCall() {
		if err := p.consumeCall(); err != nil {
			return nil, err
		}
		return lengthSeg{}, nil
	}
	return childKey{key: ident}, nil
}

func (p *parser) parseRecursive() (segment, error) {
	if p.atEnd() {
		return nil, p.err(p.i, "expected key, '*' or '[' after '..'")
	}
	if p.peek() == '*' {
		p.i++
		return recursiveWildcard{}, nil
	}
	if p.peek() == '[' {
		return p.parseBracket(true)
	}
	ident, err := p.parseIdentifier()
	if err != nil {
		return nil, err
	}
	if ident == "length" && p.tryCall() {
		if err := p.consumeCall(); err != nil {
			return nil, err
		}
		return recursiveLength{}, nil
	}
	return recursiveKey{key: ident}, nil
}

func (p *parser) parseBracket(recursive bool) (segment, error) {
	open := p.i
	p.i++ // '['
	p.skipSpace()
	if p.atEnd() {
		return nil, p.err(open, "unclosed '['")
	}

	switch p.peek() {
	case '?':
		expr, err := p.parseFilter()
		if err != nil {
			return nil, err
		}
		return filterSeg{expr: expr, recursive: recursive}, nil
	case '(':
		n, err := p.parseScript()
		if err != nil {
			return nil, err
		}
		if recursive {
			return recursiveScriptIndex{n: n}, nil
		}
		return scriptIndex{n: n}, nil
	case '*':
		p.i++
		p.skipSpace()
		if err := p.expect(']'); err != nil {
			return nil, err
		}
		if recursive {
			return recursiveWildcard{}, nil
		}
		return childWildcard{}, nil
	case '\'', '"':
		keys, err := p.parseStringUnion()
		if err != nil {
			return nil, err
		}
		if recursive {
			if len(keys) == 1 {
				return recursiveKey{key: keys[0]}, nil
			}
			return recursiveUnionKeys{keys: keys}, nil
		}
		if len(keys) == 1 {
			return childKey{key: keys[0]}, nil
		}
		return childUnionKeys{keys: keys}, nil
	default:
		if p.peek() == '-' || isDigit(p.peek()) {
			idxs, err := p.parseIndexUnion()
			if err != nil {
				return nil, err
			}
			if recursive {
				if len(idxs) == 1 {
					return recursiveIndex{index: idxs[0]}, nil
				}
				return recursiveUnionIndices{indices: idxs}, nil
			}
			if len(idxs) == 1 {
				return childIndex{index: idxs[0]}, nil
			}
			return childUnionIndices{indices: idxs}, nil
		}
		return nil, p.err(p.i, "expected index, key, '*', filter or script")
	}
}

func (p *parser) parseFilter() (filterExpr, error) {
	p.i++ // '?'
	p.skipSpace()
	if err := p.expect('('); err != nil {
		return nil, err
	}
	expr, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if err := p.expect(')'); err != nil {
		return nil, err
	}
	p.skipSpace()
	if err := p.expect(']'); err != nil {
		return nil, err
	}
	return expr, nil
}

func (p *parser) parseOr() (filterExpr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		p.skipSpace()
		if !p.match("||") {
			return left, nil
		}
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = orExpr{left: left, right: right}
	}
}

func (p *parser) parseAnd() (filterExpr, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		p.skipSpace()
		if !p.match("&&") {
			return left, nil
		}
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		left = andExpr{left: left, right: right}
	}
}

func (p *parser) parsePrimary() (filterExpr, error) {
	p.skipSpace()
	if p.peek() == '(' {
		p.i++
		expr, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if err := p.expect(')'); err != nil {
			return nil, err
		}
		return expr, nil
	}
	src, err := p.parseValueSrc()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if op, ok := p.parseCompareOp(); ok {
		p.skipSpace()
		rhs, err := p.parseValueSrc()
		if err != nil {
			return nil, err
		}
		return cmpExpr{left: src, op: op, right: rhs}, nil
	}
	return truthyExpr{src: src}, nil
}

func (p *parser) parseValueSrc() (valueSrc, error) {
	p.skipSpace()
	if p.peek() == '@' {
		p.i++
		steps, err := p.parseFilterSteps()
		if err != nil {
			return nil, err
		}
		return pathVal{steps: steps}, nil
	}
	v, err := p.parseLiteral()
	if err != nil {
		return nil, err
	}
	return literalVal{v: v}, nil
}

func (p *parser) parseFilterSteps() ([]filterStep, error) {
	var steps []filterStep
	for {
		saved := p.i
		p.skipSpace()
		switch p.peek() {
		case '.':
			p.i++
			ident, err := p.parseIdentifier()
			if err != nil {
				return nil, err
			}
			if ident == "length" && p.tryCall() {
				if err := p.consumeCall(); err != nil {
					return nil, err
				}
				steps = append(steps, filterLength{})
				continue
			}
			steps = append(steps, filterKey{key: ident})
		case '[':
			p.i++
			p.skipSpace()
			if p.peek() == '\'' || p.peek() == '"' {
				key, err := p.parseQuotedString()
				if err != nil {
					return nil, err
				}
				p.skipSpace()
				if err := p.expect(']'); err != nil {
					return nil, err
				}
				steps = append(steps, filterKey{key: key})
				continue
			}
			idx, err := p.parseInt()
			if err != nil {
				return nil, err
			}
			p.skipSpace()
			if err := p.expect(']'); err != nil {
				return nil, err
			}
			steps = append(steps, filterIndex{index: idx})
		default:
			p.i = saved
			return steps, nil
		}
	}
}

func (p *parser) parseLiteral() (interface{}, error) {
	p.skipSpace()
	switch p.peek() {
	case '\'', '"':
		return p.parseQuotedString()
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return p.parseNumber()
	default:
		pos := p.i
		switch {
		case p.matchWord("true"):
			return true, nil
		case p.matchWord("false"):
			return false, nil
		case p.matchWord("null"):
			return nil, nil
		default:
			if p.atEnd() {
				return nil, p.err(pos, "expected value")
			}
			return nil, p.err(pos, "expected value")
		}
	}
}

func (p *parser) parseCompareOp() (string, bool) {
	switch {
	case p.match("=="):
		return "==", true
	case p.match("!="):
		return "!=", true
	case p.match("<="):
		return "<=", true
	case p.match(">="):
		return ">=", true
	case p.match("<"):
		return "<", true
	case p.match(">"):
		return ">", true
	default:
		return "", false
	}
}

func (p *parser) parseScript() (int, error) {
	open := p.i
	p.i++ // '('
	p.skipSpace()
	if err := p.expect('@'); err != nil {
		return 0, err
	}
	p.skipSpace()
	if err := p.expect('.'); err != nil {
		return 0, err
	}
	p.skipSpace()
	if !p.match("length") {
		return 0, p.err(open, "script must be (@.length-N)")
	}
	p.skipSpace()
	if err := p.expect('-'); err != nil {
		return 0, err
	}
	p.skipSpace()
	n, err := p.parseUnsignedInt()
	if err != nil {
		return 0, err
	}
	p.skipSpace()
	if err := p.expect(')'); err != nil {
		return 0, err
	}
	p.skipSpace()
	if err := p.expect(']'); err != nil {
		return 0, err
	}
	return n, nil
}

func (p *parser) parseStringUnion() ([]string, error) {
	var keys []string
	for {
		p.skipSpace()
		key, err := p.parseQuotedString()
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
		p.skipSpace()
		if p.peek() == ',' {
			p.i++
			continue
		}
		if err := p.expect(']'); err != nil {
			return nil, err
		}
		return keys, nil
	}
}

func (p *parser) parseIndexUnion() ([]int, error) {
	var idxs []int
	for {
		p.skipSpace()
		idx, err := p.parseInt()
		if err != nil {
			return nil, err
		}
		idxs = append(idxs, idx)
		p.skipSpace()
		if p.peek() == ',' {
			p.i++
			continue
		}
		if err := p.expect(']'); err != nil {
			return nil, err
		}
		return idxs, nil
	}
}

func (p *parser) parseIdentifier() (string, error) {
	start := p.i
	if p.atEnd() || !isIdentChar(p.peek()) {
		return "", p.err(start, "expected identifier")
	}
	for !p.atEnd() && isIdentChar(p.peek()) {
		p.i++
	}
	return p.path[start:p.i], nil
}

func (p *parser) parseQuotedString() (string, error) {
	if p.atEnd() {
		return "", p.err(p.i, "expected quoted string")
	}
	quote := p.peek()
	if quote != '\'' && quote != '"' {
		return "", p.err(p.i, "expected quoted string")
	}
	start := p.i
	p.i++
	var b []byte
	for !p.atEnd() {
		c := p.peek()
		if c == '\\' {
			p.i++
			if p.atEnd() {
				return "", p.err(p.i, "unterminated escape")
			}
			esc := p.next()
			switch esc {
			case '\\', '\'', '"':
				b = append(b, esc)
			case 'n':
				b = append(b, '\n')
			case 't':
				b = append(b, '\t')
			case 'r':
				b = append(b, '\r')
			default:
				b = append(b, esc)
			}
			continue
		}
		if c == quote {
			p.i++
			return string(b), nil
		}
		b = append(b, p.next())
	}
	return "", p.err(start, "unterminated string")
}

func (p *parser) parseInt() (int, error) {
	start := p.i
	if p.peek() == '-' {
		p.i++
	}
	if !isDigit(p.peek()) {
		return 0, p.err(start, "expected integer")
	}
	for isDigit(p.peek()) {
		p.i++
	}
	n, err := strconv.Atoi(p.path[start:p.i])
	if err != nil {
		return 0, p.err(start, "invalid integer")
	}
	return n, nil
}

func (p *parser) parseUnsignedInt() (int, error) {
	start := p.i
	if !isDigit(p.peek()) {
		return 0, p.err(start, "expected integer")
	}
	for isDigit(p.peek()) {
		p.i++
	}
	n, err := strconv.Atoi(p.path[start:p.i])
	if err != nil {
		return 0, p.err(start, "invalid integer")
	}
	return n, nil
}

func (p *parser) parseNumber() (interface{}, error) {
	start := p.i
	if p.peek() == '-' {
		p.i++
	}
	if !isDigit(p.peek()) {
		return nil, p.err(start, "expected number")
	}
	isFloat := false
	for isDigit(p.peek()) {
		p.i++
	}
	if p.peek() == '.' {
		isFloat = true
		p.i++
		if !isDigit(p.peek()) {
			return nil, p.err(p.i, "expected digit after '.'")
		}
		for isDigit(p.peek()) {
			p.i++
		}
	}
	if p.peek() == 'e' || p.peek() == 'E' {
		isFloat = true
		p.i++
		if p.peek() == '+' || p.peek() == '-' {
			p.i++
		}
		if !isDigit(p.peek()) {
			return nil, p.err(p.i, "expected digit in exponent")
		}
		for isDigit(p.peek()) {
			p.i++
		}
	}
	s := p.path[start:p.i]
	if !isFloat {
		n, err := strconv.ParseInt(s, 10, 64)
		if err == nil {
			return n, nil
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, p.err(start, "invalid number")
	}
	return f, nil
}

func (p *parser) tryCall() bool {
	saved := p.i
	p.skipSpace()
	if p.peek() == '(' {
		return true
	}
	p.i = saved
	return false
}

func (p *parser) consumeCall() error {
	p.skipSpace()
	if err := p.expect('('); err != nil {
		return err
	}
	p.skipSpace()
	return p.expect(')')
}

func (p *parser) expect(c byte) error {
	if p.peek() != c {
		if p.atEnd() {
			return p.err(p.i, fmt.Sprintf("expected '%c'", c))
		}
		return p.err(p.i, fmt.Sprintf("expected '%c'", c))
	}
	p.i++
	return nil
}

func (p *parser) match(s string) bool {
	if p.i+len(s) > len(p.path) || p.path[p.i:p.i+len(s)] != s {
		return false
	}
	p.i += len(s)
	return true
}

func (p *parser) matchWord(s string) bool {
	if !p.match(s) {
		return false
	}
	if !p.atEnd() && isIdentChar(p.peek()) {
		p.i -= len(s)
		return false
	}
	return true
}

func (p *parser) skipSpace() {
	for !p.atEnd() && isSpace(p.peek()) {
		p.i++
	}
}

func (p *parser) peek() byte {
	if p.atEnd() {
		return 0
	}
	return p.path[p.i]
}

func (p *parser) next() byte {
	c := p.path[p.i]
	p.i++
	return c
}

func (p *parser) atEnd() bool {
	return p.i >= len(p.path)
}

func (p *parser) err(pos int, msg string) error {
	if pos < 0 {
		pos = 0
	}
	if pos > len(p.path) {
		pos = len(p.path)
	}
	return &SyntaxError{Message: msg, Position: pos}
}

func isIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') || c == '_' || c == '-'
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

type mapView struct {
	get  func(string) (interface{}, bool)
	vals []interface{}
}

func asMap(v interface{}) (mapView, bool) {
	switch m := v.(type) {
	case *Map:
		if m == nil {
			return mapView{get: func(string) (interface{}, bool) { return nil, false }}, true
		}
		vals := make([]interface{}, 0, m.Len())
		m.Iterate(func(_, val interface{}) {
			vals = append(vals, val)
		})
		return mapView{
			get:  func(key string) (interface{}, bool) { return m.Get(key) },
			vals: vals,
		}, true
	case map[string]interface{}:
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		vals := make([]interface{}, len(keys))
		for i, k := range keys {
			vals[i] = m[k]
		}
		return mapView{
			get: func(key string) (interface{}, bool) {
				val, ok := m[key]
				return val, ok
			},
			vals: vals,
		}, true
	case map[interface{}]interface{}:
		type kv struct {
			k string
			v interface{}
		}
		var pairs []kv
		for k, val := range m {
			ks, ok := k.(string)
			if !ok {
				continue
			}
			pairs = append(pairs, kv{ks, val})
		}
		sort.Slice(pairs, func(i, j int) bool { return pairs[i].k < pairs[j].k })
		vals := make([]interface{}, len(pairs))
		for i, p := range pairs {
			vals[i] = p.v
		}
		return mapView{
			get: func(key string) (interface{}, bool) {
				val, ok := m[key]
				return val, ok
			},
			vals: vals,
		}, true
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || rv.Kind() != reflect.Map {
		return mapView{}, false
	}
	keys := rv.MapKeys()
	type kv struct {
		k string
		v interface{}
	}
	var pairs []kv
	for _, rk := range keys {
		if rk.Kind() != reflect.String {
			continue
		}
		pairs = append(pairs, kv{rk.String(), rv.MapIndex(rk).Interface()})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].k < pairs[j].k })
	vals := make([]interface{}, len(pairs))
	for i, p := range pairs {
		vals[i] = p.v
	}
	return mapView{
		get: func(key string) (interface{}, bool) {
			val := rv.MapIndex(reflect.ValueOf(key))
			if !val.IsValid() {
				return nil, false
			}
			return val.Interface(), true
		},
		vals: vals,
	}, true
}

func asArray(v interface{}) ([]interface{}, bool) {
	switch a := v.(type) {
	case []interface{}:
		if a == nil {
			return []interface{}{}, true
		}
		return a, true
	case []byte:
		return nil, false
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || rv.Kind() != reflect.Slice {
		return nil, false
	}
	if rv.Type().Elem().Kind() == reflect.Uint8 {
		return nil, false
	}
	n := rv.Len()
	out := make([]interface{}, n)
	for i := 0; i < n; i++ {
		out[i] = rv.Index(i).Interface()
	}
	return out, true
}

func lengthOf(v interface{}) (int, bool) {
	switch t := v.(type) {
	case string:
		return len(t), true
	case *Map:
		if t == nil {
			return 0, true
		}
		return t.Len(), true
	case []interface{}:
		return len(t), true
	case map[string]interface{}:
		return len(t), true
	case map[interface{}]interface{}:
		return len(t), true
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return 0, false
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Map, reflect.String:
		return rv.Len(), true
	}
	return 0, false
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
	case int:
		return t != 0
	case int8:
		return t != 0
	case int16:
		return t != 0
	case int32:
		return t != 0
	case int64:
		return t != 0
	case uint:
		return t != 0
	case uint8:
		return t != 0
	case uint16:
		return t != 0
	case uint32:
		return t != 0
	case uint64:
		return t != 0
	case float32:
		return t != 0
	case float64:
		return t != 0
	case *Map:
		return t != nil && t.Len() > 0
	case []interface{}:
		return len(t) > 0
	case map[string]interface{}:
		return len(t) > 0
	case map[interface{}]interface{}:
		return len(t) > 0
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Map, reflect.String:
		return rv.Len() > 0
	case reflect.Ptr, reflect.Interface:
		return !rv.IsNil()
	}
	return true
}

func asFloat(v interface{}) (float64, bool) {
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
	}
	return 0, false
}

func valuesEqual(a, b interface{}) bool {
	if an, ok := asFloat(a); ok {
		if bn, ok := asFloat(b); ok {
			return an == bn
		}
		return false
	}
	if _, ok := asFloat(b); ok {
		return false
	}
	if as, ok := a.(string); ok {
		bs, ok := b.(string)
		return ok && as == bs
	}
	if ab, ok := a.(bool); ok {
		bb, ok := b.(bool)
		return ok && ab == bb
	}
	if a == nil && b == nil {
		return true
	}
	return reflect.DeepEqual(a, b)
}

func compareOrdered(op string, a, b interface{}) bool {
	if an, ok := asFloat(a); ok {
		if bn, ok := asFloat(b); ok {
			switch op {
			case "<":
				return an < bn
			case ">":
				return an > bn
			case "<=":
				return an <= bn
			case ">=":
				return an >= bn
			}
		}
		return false
	}
	as, aOK := a.(string)
	bs, bOK := b.(string)
	if aOK && bOK {
		switch op {
		case "<":
			return as < bs
		case ">":
			return as > bs
		case "<=":
			return as <= bs
		case ">=":
			return as >= bs
		}
	}
	return false
}
