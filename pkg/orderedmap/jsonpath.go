// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package orderedmap

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// SyntaxError describes an invalid JSONPath expression.
type SyntaxError struct {
	Message  string
	Position int
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("syntax error at position %d: %s", e.Position, e.Message)
}

// Query evaluates a JSONPath expression against doc and returns all matches.
func Query(doc interface{}, path string) ([]interface{}, error) {
	p := &jpParser{src: path}
	segs, err := p.parseRoot()
	if err != nil {
		return nil, err
	}
	return evalSegments(doc, doc, segs), nil
}

// QueryOne returns the first match of a JSONPath expression.
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

type jpSelector interface {
	apply(root, node interface{}) []interface{}
}

type jpSegment struct {
	recursive bool
	sel       jpSelector
}

type jpWildcard struct{}
type jpLength struct{}
type jpKeys struct{ keys []string }
type jpIndexes struct{ idx []int }
type jpFromEnd struct{ n int }
type jpFilter struct{ expr jpExpr }

func (jpWildcard) apply(_, node interface{}) []interface{} { return jpChildren(node) }

func (jpLength) apply(_, node interface{}) []interface{} {
	if l, ok := jpLen(node); ok {
		return []interface{}{l}
	}
	return nil
}

func (s jpKeys) apply(_, node interface{}) []interface{} {
	var out []interface{}
	for _, k := range s.keys {
		if v, ok := jpGetKey(node, k); ok {
			out = append(out, v)
		}
	}
	return out
}

func (s jpIndexes) apply(_, node interface{}) []interface{} {
	arr, ok := node.([]interface{})
	if !ok {
		return nil
	}
	var out []interface{}
	for _, i := range s.idx {
		if i < 0 {
			i += len(arr)
		}
		if i >= 0 && i < len(arr) {
			out = append(out, arr[i])
		}
	}
	return out
}

func (s jpFromEnd) apply(root, node interface{}) []interface{} {
	arr, ok := node.([]interface{})
	if !ok {
		return nil
	}
	return jpIndexes{[]int{len(arr) - s.n}}.apply(root, node)
}

func (s jpFilter) apply(root, node interface{}) []interface{} {
	var out []interface{}
	for _, c := range jpChildren(node) {
		if s.expr.eval(root, c) {
			out = append(out, c)
		}
	}
	return out
}

func evalSegments(root, node interface{}, segs []jpSegment) []interface{} {
	cur := []interface{}{node}
	for _, seg := range segs {
		next := []interface{}{}
		for _, n := range cur {
			if seg.recursive {
				for _, d := range jpDescendants(n, nil) {
					if _, isWild := seg.sel.(jpWildcard); isWild {
						next = append(next, d)
					} else {
						next = append(next, seg.sel.apply(root, d)...)
					}
				}
			} else {
				next = append(next, seg.sel.apply(root, n)...)
			}
		}
		cur = next
	}
	return cur
}

func jpDescendants(n interface{}, acc []interface{}) []interface{} {
	acc = append(acc, n)
	for _, c := range jpChildren(n) {
		acc = jpDescendants(c, acc)
	}
	return acc
}

func jpChildren(node interface{}) []interface{} {
	var out []interface{}
	switch t := node.(type) {
	case []interface{}:
		out = append(out, t...)
	case *Map:
		t.Iterate(func(_, v interface{}) { out = append(out, v) })
	case map[string]interface{}:
		for _, k := range sortedKeys(t) {
			out = append(out, t[k])
		}
	case map[interface{}]interface{}:
		for _, v := range t {
			out = append(out, v)
		}
	}
	return out
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

func jpGetKey(node interface{}, k string) (interface{}, bool) {
	switch t := node.(type) {
	case *Map:
		return t.Get(k)
	case map[string]interface{}:
		v, ok := t[k]
		return v, ok
	case map[interface{}]interface{}:
		v, ok := t[k]
		return v, ok
	}
	return nil, false
}

func jpLen(node interface{}) (int, bool) {
	switch t := node.(type) {
	case []interface{}:
		return len(t), true
	case *Map:
		return t.Len(), true
	case map[string]interface{}:
		return len(t), true
	case map[interface{}]interface{}:
		return len(t), true
	case string:
		return utf8.RuneCountInString(t), true
	}
	return 0, false
}

func jpTruthy(v interface{}) bool {
	if v == nil {
		return false
	}
	if f, ok := jpNumber(v); ok {
		return f != 0
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t != ""
	}
	if l, ok := jpLen(v); ok {
		return l != 0
	}
	return true
}

func jpNumber(v interface{}) (float64, bool) {
	switch t := v.(type) {
	case int:
		return float64(t), true
	case int8:
		return float64(t), true
	case int16:
		return float64(t), true
	case int32:
		return float64(t), true
	case int64:
		return float64(t), true
	case uint:
		return float64(t), true
	case uint8:
		return float64(t), true
	case uint16:
		return float64(t), true
	case uint32:
		return float64(t), true
	case uint64:
		return float64(t), true
	case float32:
		return float64(t), true
	case float64:
		return t, true
	}
	return 0, false
}

// Filter expressions

type jpExpr interface {
	eval(root, cur interface{}) bool
}

type jpOr struct{ l, r jpExpr }
type jpAnd struct{ l, r jpExpr }
type jpNot struct{ e jpExpr }
type jpTruth struct{ o jpOperand }
type jpCmp struct {
	op   string
	l, r jpOperand
}

type jpOperand struct {
	isPath   bool
	fromRoot bool
	segs     []jpSegment
	lit      interface{}
}

func (o jpOperand) values(root, cur interface{}) []interface{} {
	if !o.isPath {
		return []interface{}{o.lit}
	}
	start := cur
	if o.fromRoot {
		start = root
	}
	return evalSegments(root, start, o.segs)
}

func (e jpOr) eval(root, cur interface{}) bool  { return e.l.eval(root, cur) || e.r.eval(root, cur) }
func (e jpAnd) eval(root, cur interface{}) bool { return e.l.eval(root, cur) && e.r.eval(root, cur) }
func (e jpNot) eval(root, cur interface{}) bool { return !e.e.eval(root, cur) }

func (e jpTruth) eval(root, cur interface{}) bool {
	vals := e.o.values(root, cur)
	return len(vals) > 0 && jpTruthy(vals[0])
}

func (e jpCmp) eval(root, cur interface{}) bool {
	lv, rv := e.l.values(root, cur), e.r.values(root, cur)
	if len(lv) == 0 || len(rv) == 0 {
		return false
	}
	return jpCompare(e.op, lv[0], rv[0])
}

func jpCompare(op string, a, b interface{}) bool {
	if fa, ok := jpNumber(a); ok {
		if fb, ok := jpNumber(b); ok {
			switch op {
			case "==":
				return fa == fb
			case "!=":
				return fa != fb
			case "<":
				return fa < fb
			case ">":
				return fa > fb
			case "<=":
				return fa <= fb
			case ">=":
				return fa >= fb
			}
		}
	}
	if sa, ok := a.(string); ok {
		if sb, ok := b.(string); ok {
			switch op {
			case "==":
				return sa == sb
			case "!=":
				return sa != sb
			case "<":
				return sa < sb
			case ">":
				return sa > sb
			case "<=":
				return sa <= sb
			case ">=":
				return sa >= sb
			}
		}
	}
	eq := jpScalarEq(a, b)
	switch op {
	case "==":
		return eq
	case "!=":
		return !eq
	}
	return false
}

func jpScalarEq(a, b interface{}) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if ba, ok := a.(bool); ok {
		bb, ok := b.(bool)
		return ok && ba == bb
	}
	return false
}

// Parser

type jpParser struct {
	src string
	pos int
}

func (p *jpParser) errf(pos int, format string, args ...interface{}) error {
	return &SyntaxError{Message: fmt.Sprintf(format, args...), Position: pos}
}

func (p *jpParser) eof() bool { return p.pos >= len(p.src) }

func (p *jpParser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.src[p.pos]
}

func (p *jpParser) hasPrefix(s string) bool { return strings.HasPrefix(p.src[p.pos:], s) }

func (p *jpParser) skipWS() {
	for !p.eof() && (p.peek() == ' ' || p.peek() == '\t' || p.peek() == '\n' || p.peek() == '\r') {
		p.pos++
	}
}

func (p *jpParser) expect(s string) error {
	if !p.hasPrefix(s) {
		return p.errf(p.pos, "expected '%s'", s)
	}
	p.pos += len(s)
	return nil
}

func (p *jpParser) parseRoot() ([]jpSegment, error) {
	if p.peek() != '$' {
		return nil, p.errf(p.pos, "path must start with '$'")
	}
	p.pos++
	segs, err := p.parseSegments(false)
	if err != nil {
		return nil, err
	}
	if !p.eof() {
		return nil, p.errf(p.pos, "unexpected character '%c'", p.peek())
	}
	return segs, nil
}

// parseSegments parses segments until EOF, or (when inFilter) until a
// character that cannot continue a path.
func (p *jpParser) parseSegments(inFilter bool) ([]jpSegment, error) {
	var segs []jpSegment
	for !p.eof() {
		switch {
		case p.hasPrefix(".."):
			p.pos += 2
			sel, err := p.parseAfterDot(true)
			if err != nil {
				return nil, err
			}
			segs = append(segs, jpSegment{recursive: true, sel: sel})
		case p.peek() == '.':
			p.pos++
			sel, err := p.parseAfterDot(false)
			if err != nil {
				return nil, err
			}
			segs = append(segs, jpSegment{sel: sel})
		case p.peek() == '[':
			sel, err := p.parseBracket()
			if err != nil {
				return nil, err
			}
			segs = append(segs, jpSegment{sel: sel})
		default:
			if inFilter {
				return segs, nil
			}
			return nil, p.errf(p.pos, "unexpected character '%c'", p.peek())
		}
	}
	return segs, nil
}

func isIdentChar(c byte) bool {
	return c == '_' || c == '-' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func (p *jpParser) parseAfterDot(recursive bool) (jpSelector, error) {
	if p.peek() == '*' {
		p.pos++
		return jpWildcard{}, nil
	}
	if recursive && p.peek() == '[' {
		return p.parseBracket()
	}
	start := p.pos
	for !p.eof() && isIdentChar(p.peek()) {
		p.pos++
	}
	if start == p.pos {
		if p.eof() {
			return nil, p.errf(p.pos, "expected identifier")
		}
		return nil, p.errf(p.pos, "unexpected character '%c'", p.peek())
	}
	name := p.src[start:p.pos]
	if !recursive && name == "length" && p.hasPrefix("()") {
		p.pos += 2
		return jpLength{}, nil
	}
	return jpKeys{[]string{name}}, nil
}

func (p *jpParser) parseBracket() (jpSelector, error) {
	open := p.pos
	p.pos++
	p.skipWS()
	var sel jpSelector
	switch {
	case p.eof():
		return nil, p.errf(p.pos, "unterminated bracket")
	case p.peek() == '*':
		p.pos++
		sel = jpWildcard{}
	case p.hasPrefix("?("):
		p.pos += 2
		expr, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		p.skipWS()
		if err := p.expect(")"); err != nil {
			return nil, err
		}
		sel = jpFilter{expr}
	case p.peek() == '(':
		s, err := p.parseScript()
		if err != nil {
			return nil, err
		}
		sel = s
	case p.peek() == '\'' || p.peek() == '"':
		var keys []string
		for {
			p.skipWS()
			k, err := p.parseString()
			if err != nil {
				return nil, err
			}
			keys = append(keys, k)
			p.skipWS()
			if p.peek() != ',' {
				break
			}
			p.pos++
		}
		sel = jpKeys{keys}
	case p.peek() == '-' || (p.peek() >= '0' && p.peek() <= '9'):
		var idx []int
		for {
			p.skipWS()
			n, err := p.parseInt()
			if err != nil {
				return nil, err
			}
			idx = append(idx, n)
			p.skipWS()
			if p.peek() != ',' {
				break
			}
			p.pos++
		}
		sel = jpIndexes{idx}
	default:
		return nil, p.errf(p.pos, "unexpected character '%c' in brackets", p.peek())
	}
	p.skipWS()
	if p.eof() {
		return nil, p.errf(open, "unterminated bracket")
	}
	if err := p.expect("]"); err != nil {
		return nil, err
	}
	return sel, nil
}

func (p *jpParser) parseScript() (jpSelector, error) {
	p.pos++
	p.skipWS()
	if err := p.expect("@"); err != nil {
		return nil, err
	}
	p.skipWS()
	if err := p.expect(".length"); err != nil {
		return nil, err
	}
	p.skipWS()
	n := 0
	if p.peek() == '-' {
		p.pos++
		p.skipWS()
		v, err := p.parseInt()
		if err != nil {
			return nil, err
		}
		n = v
	}
	p.skipWS()
	if err := p.expect(")"); err != nil {
		return nil, err
	}
	return jpFromEnd{n}, nil
}

func (p *jpParser) parseInt() (int, error) {
	start := p.pos
	if p.peek() == '-' {
		p.pos++
	}
	for !p.eof() && p.peek() >= '0' && p.peek() <= '9' {
		p.pos++
	}
	n, err := strconv.Atoi(p.src[start:p.pos])
	if err != nil {
		return 0, p.errf(start, "invalid integer")
	}
	return n, nil
}

func (p *jpParser) parseString() (string, error) {
	start := p.pos
	q := p.peek()
	if q != '\'' && q != '"' {
		return "", p.errf(p.pos, "expected string")
	}
	p.pos++
	var sb strings.Builder
	for {
		if p.eof() {
			return "", p.errf(start, "unterminated string")
		}
		c := p.src[p.pos]
		p.pos++
		if c == q {
			return sb.String(), nil
		}
		if c != '\\' {
			sb.WriteByte(c)
			continue
		}
		if p.eof() {
			return "", p.errf(start, "unterminated string")
		}
		e := p.src[p.pos]
		p.pos++
		switch e {
		case 'n':
			sb.WriteByte('\n')
		case 't':
			sb.WriteByte('\t')
		case 'r':
			sb.WriteByte('\r')
		case 'b':
			sb.WriteByte('\b')
		case 'f':
			sb.WriteByte('\f')
		case '/', '\\', '\'', '"':
			sb.WriteByte(e)
		case 'u':
			if p.pos+4 > len(p.src) {
				return "", p.errf(p.pos-2, "invalid unicode escape")
			}
			r, err := strconv.ParseUint(p.src[p.pos:p.pos+4], 16, 32)
			if err != nil {
				return "", p.errf(p.pos-2, "invalid unicode escape")
			}
			p.pos += 4
			sb.WriteRune(rune(r))
		default:
			return "", p.errf(p.pos-2, "invalid escape '\\%c'", e)
		}
	}
}

func (p *jpParser) parseOr() (jpExpr, error) {
	l, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		p.skipWS()
		if !p.hasPrefix("||") {
			return l, nil
		}
		p.pos += 2
		r, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l = jpOr{l, r}
	}
}

func (p *jpParser) parseAnd() (jpExpr, error) {
	l, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		p.skipWS()
		if !p.hasPrefix("&&") {
			return l, nil
		}
		p.pos += 2
		r, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		l = jpAnd{l, r}
	}
}

func (p *jpParser) parseUnary() (jpExpr, error) {
	p.skipWS()
	if p.peek() == '!' && !p.hasPrefix("!=") {
		p.pos++
		e, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return jpNot{e}, nil
	}
	if p.peek() == '(' {
		p.pos++
		e, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		p.skipWS()
		if err := p.expect(")"); err != nil {
			return nil, err
		}
		return e, nil
	}
	l, err := p.parseOperand()
	if err != nil {
		return nil, err
	}
	p.skipWS()
	for _, op := range []string{"==", "!=", "<=", ">=", "<", ">"} {
		if p.hasPrefix(op) {
			p.pos += len(op)
			r, err := p.parseOperand()
			if err != nil {
				return nil, err
			}
			return jpCmp{op, l, r}, nil
		}
	}
	return jpTruth{l}, nil
}

func (p *jpParser) parseOperand() (jpOperand, error) {
	p.skipWS()
	if p.eof() {
		return jpOperand{}, p.errf(p.pos, "unexpected end of filter expression")
	}
	c := p.peek()
	switch {
	case c == '@' || c == '$':
		p.pos++
		segs, err := p.parseSegments(true)
		if err != nil {
			return jpOperand{}, err
		}
		return jpOperand{isPath: true, fromRoot: c == '$', segs: segs}, nil
	case c == '\'' || c == '"':
		s, err := p.parseString()
		if err != nil {
			return jpOperand{}, err
		}
		return jpOperand{lit: s}, nil
	case c == '-' || (c >= '0' && c <= '9'):
		start := p.pos
		if c == '-' {
			p.pos++
		}
		for !p.eof() && strings.IndexByte("0123456789.eE+-", p.peek()) >= 0 {
			if (p.peek() == '+' || p.peek() == '-') && !(p.src[p.pos-1] == 'e' || p.src[p.pos-1] == 'E') {
				break
			}
			p.pos++
		}
		f, err := strconv.ParseFloat(p.src[start:p.pos], 64)
		if err != nil {
			return jpOperand{}, p.errf(start, "invalid number")
		}
		return jpOperand{lit: f}, nil
	case p.hasPrefix("true"):
		p.pos += 4
		return jpOperand{lit: true}, nil
	case p.hasPrefix("false"):
		p.pos += 5
		return jpOperand{lit: false}, nil
	case p.hasPrefix("null"):
		p.pos += 4
		return jpOperand{lit: nil}, nil
	}
	return jpOperand{}, p.errf(p.pos, "unexpected character '%c' in filter expression", c)
}
