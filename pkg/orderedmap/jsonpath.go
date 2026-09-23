// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package orderedmap

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SyntaxError describes a malformed JSONPath expression.
type SyntaxError struct {
	Message string
	// Position is the byte offset within the path at which the error was detected.
	Position int
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("syntax error at position %d: %s", e.Position, e.Message)
}

// Query evaluates the JSONPath expression against doc and returns all matching values.
// When nothing matches, an empty (non-nil) slice is returned.
//
// doc may be composed of *Map, map[string]interface{}, map[interface{}]interface{},
// []interface{} and scalar values.
func Query(doc interface{}, path string) ([]interface{}, error) {
	selectors, err := parseJSONPath(path)
	if err != nil {
		return nil, err
	}

	nodes := []interface{}{doc}
	for _, sel := range selectors {
		var next []interface{}
		for _, node := range nodes {
			next = sel.apply(doc, node, next)
		}
		nodes = next
	}

	if nodes == nil {
		return []interface{}{}, nil
	}
	return nodes, nil
}

// QueryOne evaluates the JSONPath expression against doc and returns the first match.
// The boolean result is false when nothing matches.
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

type jsonPathSelector interface {
	apply(root, node interface{}, out []interface{}) []interface{}
}

type unionItem struct {
	key     string
	index   int
	isIndex bool
}

type unionSelector struct {
	items []unionItem
}

func (s unionSelector) apply(_, node interface{}, out []interface{}) []interface{} {
	for _, item := range s.items {
		var val interface{}
		var found bool
		if item.isIndex {
			val, found = jsonPathIndex(node, item.index)
		} else {
			val, found = jsonPathKey(node, item.key)
		}
		if found {
			out = append(out, val)
		}
	}
	return out
}

type wildcardSelector struct{}

func (wildcardSelector) apply(_, node interface{}, out []interface{}) []interface{} {
	return append(out, jsonPathChildren(node)...)
}

type lengthSelector struct{}

func (lengthSelector) apply(_, node interface{}, out []interface{}) []interface{} {
	if length, ok := jsonPathLength(node); ok {
		out = append(out, length)
	}
	return out
}

// keyOrLengthSelector implements `.length` within filter paths: a "length" key on maps,
// otherwise the length of arrays and strings (matching the script `@.length` form).
type keyOrLengthSelector struct{}

func (keyOrLengthSelector) apply(_, node interface{}, out []interface{}) []interface{} {
	if val, found := jsonPathKey(node, "length"); found {
		return append(out, val)
	}
	switch node.(type) {
	case []interface{}, string:
		return lengthSelector{}.apply(nil, node, out)
	}
	return out
}

// scriptIndexSelector implements [(@.length+offset)].
type scriptIndexSelector struct {
	offset int
}

func (s scriptIndexSelector) apply(_, node interface{}, out []interface{}) []interface{} {
	arr, ok := node.([]interface{})
	if !ok {
		return out
	}
	idx := len(arr) + s.offset
	if idx >= 0 && idx < len(arr) {
		out = append(out, arr[idx])
	}
	return out
}

type filterSelector struct {
	expr filterExpr
}

func (s filterSelector) apply(root, node interface{}, out []interface{}) []interface{} {
	for _, child := range jsonPathChildren(node) {
		if val, ok := s.expr.eval(root, child); ok && jsonPathTruthy(val) {
			out = append(out, child)
		}
	}
	return out
}

// recursiveSelector applies inner to the node and each of its descendants in
// depth-first pre-order. A nil inner selects the nodes themselves.
type recursiveSelector struct {
	inner jsonPathSelector
}

func (s recursiveSelector) apply(root, node interface{}, out []interface{}) []interface{} {
	if s.inner == nil {
		out = append(out, node)
	} else {
		out = s.inner.apply(root, node, out)
	}
	for _, child := range jsonPathChildren(node) {
		out = s.apply(root, child, out)
	}
	return out
}

type filterExpr interface {
	// eval returns the value of the expression and whether it exists
	// (paths that do not resolve yield no value).
	eval(root, current interface{}) (interface{}, bool)
}

type logicalExpr struct {
	isAnd       bool
	left, right filterExpr
}

func (e logicalExpr) eval(root, current interface{}) (interface{}, bool) {
	lv, lok := e.left.eval(root, current)
	left := lok && jsonPathTruthy(lv)
	if e.isAnd && !left {
		return false, true
	}
	if !e.isAnd && left {
		return true, true
	}
	rv, rok := e.right.eval(root, current)
	return rok && jsonPathTruthy(rv), true
}

type comparisonExpr struct {
	op          string
	left, right filterExpr
}

func (e comparisonExpr) eval(root, current interface{}) (interface{}, bool) {
	lv, lok := e.left.eval(root, current)
	rv, rok := e.right.eval(root, current)
	return jsonPathCompare(e.op, lv, lok, rv, rok), true
}

type literalExpr struct {
	val interface{}
}

func (e literalExpr) eval(_, _ interface{}) (interface{}, bool) { return e.val, true }

type pathExpr struct {
	absolute  bool
	selectors []jsonPathSelector
}

func (e pathExpr) eval(root, current interface{}) (interface{}, bool) {
	val := current
	if e.absolute {
		val = root
	}
	for _, sel := range e.selectors {
		res := sel.apply(root, val, nil)
		if len(res) == 0 {
			return nil, false
		}
		val = res[0]
	}
	return val, true
}

type lengthCallExpr struct {
	arg filterExpr
}

func (e lengthCallExpr) eval(root, current interface{}) (interface{}, bool) {
	val, ok := e.arg.eval(root, current)
	if !ok {
		return nil, false
	}
	length, ok := jsonPathLength(val)
	if !ok {
		return nil, false
	}
	return length, true
}

type jsonPathParser struct {
	src string
	pos int
}

func parseJSONPath(path string) ([]jsonPathSelector, error) {
	p := &jsonPathParser{src: path}
	if !p.peekIs('$') {
		return nil, p.errorf("path must start with '$'")
	}
	p.pos++

	var selectors []jsonPathSelector
	for !p.eof() {
		sel, err := p.parseSegment()
		if err != nil {
			return nil, err
		}
		selectors = append(selectors, sel)
	}
	return selectors, nil
}

func (p *jsonPathParser) eof() bool { return p.pos >= len(p.src) }

func (p *jsonPathParser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.src[p.pos]
}

func (p *jsonPathParser) peekIs(c byte) bool { return !p.eof() && p.src[p.pos] == c }

func (p *jsonPathParser) hasPrefix(s string) bool { return strings.HasPrefix(p.src[p.pos:], s) }

func (p *jsonPathParser) skipWhitespace() {
	for !p.eof() && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t' || p.src[p.pos] == '\n' || p.src[p.pos] == '\r') {
		p.pos++
	}
}

func (p *jsonPathParser) errorf(format string, args ...interface{}) *SyntaxError {
	return p.errorAtf(p.pos, format, args...)
}

func (p *jsonPathParser) errorAtf(pos int, format string, args ...interface{}) *SyntaxError {
	return &SyntaxError{Message: fmt.Sprintf(format, args...), Position: pos}
}

func (p *jsonPathParser) unexpected(expected string) *SyntaxError {
	if p.eof() {
		return p.errorf("expected %s but reached end of path", expected)
	}
	r, _ := utf8.DecodeRuneInString(p.src[p.pos:])
	return p.errorf("expected %s but found '%c'", expected, r)
}

func (p *jsonPathParser) expect(c byte) error {
	if !p.peekIs(c) {
		return p.unexpected(fmt.Sprintf("'%c'", c))
	}
	p.pos++
	return nil
}

func (p *jsonPathParser) parseSegment() (jsonPathSelector, error) {
	switch {
	case p.hasPrefix(".."):
		p.pos += 2
		switch {
		case p.peekIs('*'):
			p.pos++
			return recursiveSelector{}, nil
		case p.peekIs('['):
			inner, err := p.parseBracket()
			if err != nil {
				return nil, err
			}
			if _, isWildcard := inner.(wildcardSelector); isWildcard {
				return recursiveSelector{}, nil
			}
			return recursiveSelector{inner}, nil
		default:
			name, ok := p.parseIdentifier()
			if !ok {
				return nil, p.unexpected("identifier, '*' or '[' after '..'")
			}
			return recursiveSelector{unionSelector{[]unionItem{{key: name}}}}, nil
		}

	case p.peekIs('.'):
		p.pos++
		if p.peekIs('*') {
			p.pos++
			return wildcardSelector{}, nil
		}
		nameStart := p.pos
		name, ok := p.parseIdentifier()
		if !ok {
			return nil, p.unexpected("identifier or '*' after '.'")
		}
		if p.peekIs('(') {
			return p.parseFunctionCall(name, nameStart)
		}
		return unionSelector{[]unionItem{{key: name}}}, nil

	case p.peekIs('['):
		return p.parseBracket()

	default:
		return nil, p.unexpected("'.' or '['")
	}
}

func (p *jsonPathParser) parseFunctionCall(name string, nameStart int) (jsonPathSelector, error) {
	if name != "length" {
		return nil, p.errorAtf(nameStart, "unknown function '%s'", name)
	}
	p.pos++
	if err := p.expect(')'); err != nil {
		return nil, err
	}
	return lengthSelector{}, nil
}

func isJSONPathIdentRune(r rune) bool {
	return r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func (p *jsonPathParser) parseIdentifier() (string, bool) {
	start := p.pos
	for !p.eof() {
		r, size := utf8.DecodeRuneInString(p.src[p.pos:])
		if !isJSONPathIdentRune(r) {
			break
		}
		p.pos += size
	}
	return p.src[start:p.pos], p.pos > start
}

func (p *jsonPathParser) parseBracket() (jsonPathSelector, error) {
	if err := p.expect('['); err != nil {
		return nil, err
	}
	p.skipWhitespace()

	var sel jsonPathSelector
	switch {
	case p.peekIs('*'):
		p.pos++
		sel = wildcardSelector{}

	case p.peekIs('?'):
		p.pos++
		expr, err := p.parseOrExpr()
		if err != nil {
			return nil, err
		}
		sel = filterSelector{expr}

	case p.peekIs('('):
		script, err := p.parseScript()
		if err != nil {
			return nil, err
		}
		sel = script

	default:
		union, err := p.parseUnion()
		if err != nil {
			return nil, err
		}
		sel = union
	}

	p.skipWhitespace()
	if err := p.expect(']'); err != nil {
		return nil, err
	}
	return sel, nil
}

func (p *jsonPathParser) parseUnion() (jsonPathSelector, error) {
	var items []unionItem
	for {
		p.skipWhitespace()
		switch c := p.peek(); {
		case c == '\'' || c == '"':
			key, err := p.parseString()
			if err != nil {
				return nil, err
			}
			items = append(items, unionItem{key: key})
		case c == '-' || isDigit(c):
			idx, err := p.parseInt()
			if err != nil {
				return nil, err
			}
			items = append(items, unionItem{index: idx, isIndex: true})
		default:
			return nil, p.unexpected("quoted key, index, '*', '?' or '('")
		}
		p.skipWhitespace()
		if !p.peekIs(',') {
			return unionSelector{items}, nil
		}
		p.pos++
	}
}

// parseScript parses a script expression of the form (@.length), (@.length-N) or (@.length+N).
func (p *jsonPathParser) parseScript() (jsonPathSelector, error) {
	p.pos++
	p.skipWhitespace()
	if !p.hasPrefix("@.length") {
		return nil, p.errorf("unsupported script expression (only '@.length' with optional '+' or '-' offset is supported)")
	}
	p.pos += len("@.length")
	if !p.eof() {
		if r, _ := utf8.DecodeRuneInString(p.src[p.pos:]); (isJSONPathIdentRune(r) && r != '-') || r == '(' {
			return nil, p.errorf("unsupported script expression (only '@.length' with optional '+' or '-' offset is supported)")
		}
	}
	p.skipWhitespace()

	offset := 0
	if p.peekIs('-') || p.peekIs('+') {
		negative := p.peek() == '-'
		p.pos++
		p.skipWhitespace()
		if !isDigit(p.peek()) {
			return nil, p.unexpected("integer")
		}
		n, err := p.parseInt()
		if err != nil {
			return nil, err
		}
		offset = n
		if negative {
			offset = -n
		}
	}

	p.skipWhitespace()
	if err := p.expect(')'); err != nil {
		return nil, err
	}
	return scriptIndexSelector{offset}, nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func (p *jsonPathParser) parseInt() (int, error) {
	start := p.pos
	if p.peekIs('-') {
		p.pos++
	}
	if !isDigit(p.peek()) {
		return 0, p.unexpected("digit")
	}
	for isDigit(p.peek()) {
		p.pos++
	}
	n, err := strconv.Atoi(p.src[start:p.pos])
	if err != nil {
		return 0, p.errorAtf(start, "invalid integer '%s'", p.src[start:p.pos])
	}
	return n, nil
}

func (p *jsonPathParser) parseNumber() (interface{}, error) {
	start := p.pos
	if p.peekIs('-') {
		p.pos++
	}
	if !isDigit(p.peek()) {
		return nil, p.unexpected("digit")
	}
	for isDigit(p.peek()) {
		p.pos++
	}
	isFloat := false
	if p.peekIs('.') {
		isFloat = true
		p.pos++
		if !isDigit(p.peek()) {
			return nil, p.unexpected("digit")
		}
		for isDigit(p.peek()) {
			p.pos++
		}
	}
	if p.peekIs('e') || p.peekIs('E') {
		isFloat = true
		p.pos++
		if p.peekIs('+') || p.peekIs('-') {
			p.pos++
		}
		if !isDigit(p.peek()) {
			return nil, p.unexpected("digit")
		}
		for isDigit(p.peek()) {
			p.pos++
		}
	}

	text := p.src[start:p.pos]
	if !isFloat {
		if n, err := strconv.ParseInt(text, 10, 64); err == nil {
			return n, nil
		}
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return nil, p.errorAtf(start, "invalid number '%s'", text)
	}
	return f, nil
}

func (p *jsonPathParser) parseString() (string, error) {
	start := p.pos
	quote := p.src[p.pos]
	p.pos++

	var sb strings.Builder
	for {
		if p.eof() {
			return "", p.errorAtf(start, "unterminated string")
		}
		c := p.src[p.pos]
		switch {
		case c == quote:
			p.pos++
			return sb.String(), nil
		case c == '\\':
			if err := p.parseEscape(&sb); err != nil {
				return "", err
			}
		default:
			sb.WriteByte(c)
			p.pos++
		}
	}
}

func (p *jsonPathParser) parseEscape(sb *strings.Builder) error {
	escStart := p.pos
	p.pos++
	if p.eof() {
		return p.errorAtf(escStart, "unterminated escape sequence")
	}
	c := p.src[p.pos]
	p.pos++
	switch c {
	case '\'', '"', '\\', '/':
		sb.WriteByte(c)
	case 'b':
		sb.WriteByte('\b')
	case 'f':
		sb.WriteByte('\f')
	case 'n':
		sb.WriteByte('\n')
	case 'r':
		sb.WriteByte('\r')
	case 't':
		sb.WriteByte('\t')
	case 'u':
		if p.pos+4 > len(p.src) {
			return p.errorAtf(escStart, "invalid unicode escape sequence")
		}
		code, err := strconv.ParseUint(p.src[p.pos:p.pos+4], 16, 32)
		if err != nil {
			return p.errorAtf(escStart, "invalid unicode escape sequence")
		}
		p.pos += 4
		sb.WriteRune(rune(code))
	default:
		return p.errorAtf(escStart, "invalid escape sequence '\\%c'", c)
	}
	return nil
}

func (p *jsonPathParser) parseOrExpr() (filterExpr, error) {
	left, err := p.parseAndExpr()
	if err != nil {
		return nil, err
	}
	for {
		p.skipWhitespace()
		if !p.hasPrefix("||") {
			return left, nil
		}
		p.pos += 2
		right, err := p.parseAndExpr()
		if err != nil {
			return nil, err
		}
		left = logicalExpr{isAnd: false, left: left, right: right}
	}
}

func (p *jsonPathParser) parseAndExpr() (filterExpr, error) {
	left, err := p.parseComparisonExpr()
	if err != nil {
		return nil, err
	}
	for {
		p.skipWhitespace()
		if !p.hasPrefix("&&") {
			return left, nil
		}
		p.pos += 2
		right, err := p.parseComparisonExpr()
		if err != nil {
			return nil, err
		}
		left = logicalExpr{isAnd: true, left: left, right: right}
	}
}

func (p *jsonPathParser) parseComparisonExpr() (filterExpr, error) {
	left, err := p.parsePrimaryExpr()
	if err != nil {
		return nil, err
	}
	p.skipWhitespace()

	var op string
	for _, candidate := range []string{"==", "!=", "<=", ">=", "<", ">"} {
		if p.hasPrefix(candidate) {
			op = candidate
			break
		}
	}
	if op == "" {
		if p.peekIs('=') {
			return nil, p.errorf("unexpected '=' (use '==' for equality)")
		}
		return left, nil
	}
	p.pos += len(op)

	right, err := p.parsePrimaryExpr()
	if err != nil {
		return nil, err
	}
	return comparisonExpr{op: op, left: left, right: right}, nil
}

func (p *jsonPathParser) parsePrimaryExpr() (filterExpr, error) {
	p.skipWhitespace()
	switch c := p.peek(); {
	case c == '(':
		p.pos++
		expr, err := p.parseOrExpr()
		if err != nil {
			return nil, err
		}
		p.skipWhitespace()
		if err := p.expect(')'); err != nil {
			return nil, err
		}
		return expr, nil

	case c == '@' || c == '$':
		return p.parseFilterPath()

	case c == '\'' || c == '"':
		s, err := p.parseString()
		if err != nil {
			return nil, err
		}
		return literalExpr{s}, nil

	case c == '-' || isDigit(c):
		n, err := p.parseNumber()
		if err != nil {
			return nil, err
		}
		return literalExpr{n}, nil
	}

	start := p.pos
	word, ok := p.parseIdentifier()
	if !ok {
		return nil, p.unexpected("filter operand")
	}
	switch word {
	case "true":
		return literalExpr{true}, nil
	case "false":
		return literalExpr{false}, nil
	case "null":
		return literalExpr{nil}, nil
	case "length":
		if !p.peekIs('(') {
			break
		}
		p.pos++
		arg, err := p.parsePrimaryExpr()
		if err != nil {
			return nil, err
		}
		p.skipWhitespace()
		if err := p.expect(')'); err != nil {
			return nil, err
		}
		return lengthCallExpr{arg}, nil
	}
	return nil, p.errorAtf(start, "unexpected '%s' in filter expression", word)
}

// parseFilterPath parses a singular path (@ or $ followed by keys, indices and length())
// used as an operand within a filter expression.
func (p *jsonPathParser) parseFilterPath() (filterExpr, error) {
	expr := pathExpr{absolute: p.peek() == '$'}
	p.pos++

	for {
		switch {
		case p.hasPrefix(".."):
			return nil, p.errorf("recursive descent is not supported in filter expressions")

		case p.peekIs('.'):
			p.pos++
			nameStart := p.pos
			name, ok := p.parseIdentifier()
			if !ok {
				return nil, p.unexpected("identifier after '.'")
			}
			if p.peekIs('(') {
				sel, err := p.parseFunctionCall(name, nameStart)
				if err != nil {
					return nil, err
				}
				expr.selectors = append(expr.selectors, sel)
			} else if name == "length" {
				expr.selectors = append(expr.selectors, keyOrLengthSelector{})
			} else {
				expr.selectors = append(expr.selectors, unionSelector{[]unionItem{{key: name}}})
			}

		case p.peekIs('['):
			p.pos++
			p.skipWhitespace()
			var item unionItem
			switch c := p.peek(); {
			case c == '\'' || c == '"':
				key, err := p.parseString()
				if err != nil {
					return nil, err
				}
				item = unionItem{key: key}
			case c == '-' || isDigit(c):
				idx, err := p.parseInt()
				if err != nil {
					return nil, err
				}
				item = unionItem{index: idx, isIndex: true}
			default:
				return nil, p.unexpected("quoted key or index")
			}
			p.skipWhitespace()
			if err := p.expect(']'); err != nil {
				return nil, err
			}
			expr.selectors = append(expr.selectors, unionSelector{[]unionItem{item}})

		default:
			return expr, nil
		}
	}
}

func jsonPathKey(node interface{}, key string) (interface{}, bool) {
	switch typed := node.(type) {
	case *Map:
		return typed.Get(key)
	case map[string]interface{}:
		val, found := typed[key]
		return val, found
	case map[interface{}]interface{}:
		val, found := typed[key]
		return val, found
	}
	return nil, false
}

func jsonPathIndex(node interface{}, idx int) (interface{}, bool) {
	arr, ok := node.([]interface{})
	if !ok {
		return nil, false
	}
	if idx < 0 {
		idx += len(arr)
	}
	if idx < 0 || idx >= len(arr) {
		return nil, false
	}
	return arr[idx], true
}

func jsonPathChildren(node interface{}) []interface{} {
	switch typed := node.(type) {
	case []interface{}:
		return typed
	case *Map:
		var values []interface{}
		typed.Iterate(func(_, v interface{}) { values = append(values, v) })
		return values
	case map[string]interface{}:
		var values []interface{}
		for _, k := range (Conversion{}).sortedMapKeys((Conversion{}).mapKeysFromStringMap(typed)) {
			values = append(values, typed[k.(string)])
		}
		return values
	case map[interface{}]interface{}:
		var values []interface{}
		for _, k := range (Conversion{}).sortedMapKeys((Conversion{}).mapKeysFromInterfaceMap(typed)) {
			values = append(values, typed[k])
		}
		return values
	}
	return nil
}

func jsonPathLength(node interface{}) (int, bool) {
	switch typed := node.(type) {
	case []interface{}:
		return len(typed), true
	case *Map:
		return typed.Len(), true
	case map[string]interface{}:
		return len(typed), true
	case map[interface{}]interface{}:
		return len(typed), true
	case string:
		return utf8.RuneCountInString(typed), true
	}
	return 0, false
}

func jsonPathTruthy(val interface{}) bool {
	switch typed := val.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	}
	if n, ok := jsonPathNumber(val); ok {
		return !n.isZero()
	}
	if length, ok := jsonPathLength(val); ok {
		return length > 0
	}
	return true
}

type jsonPathNum struct {
	i     int64
	f     float64
	isInt bool
}

func (n jsonPathNum) isZero() bool {
	if n.isInt {
		return n.i == 0
	}
	return n.f == 0
}

func (n jsonPathNum) float() float64 {
	if n.isInt {
		return float64(n.i)
	}
	return n.f
}

// cmp returns -1, 0 or 1; ok is false when the values are not comparable (NaN).
func (n jsonPathNum) cmp(other jsonPathNum) (int, bool) {
	if n.isInt && other.isInt {
		switch {
		case n.i < other.i:
			return -1, true
		case n.i > other.i:
			return 1, true
		}
		return 0, true
	}
	a, b := n.float(), other.float()
	switch {
	case a < b:
		return -1, true
	case a > b:
		return 1, true
	case a == b:
		return 0, true
	}
	return 0, false
}

func jsonPathNumber(val interface{}) (jsonPathNum, bool) {
	switch typed := val.(type) {
	case int:
		return jsonPathNum{i: int64(typed), isInt: true}, true
	case int8:
		return jsonPathNum{i: int64(typed), isInt: true}, true
	case int16:
		return jsonPathNum{i: int64(typed), isInt: true}, true
	case int32:
		return jsonPathNum{i: int64(typed), isInt: true}, true
	case int64:
		return jsonPathNum{i: typed, isInt: true}, true
	case uint:
		return jsonPathUnsigned(uint64(typed)), true
	case uint8:
		return jsonPathUnsigned(uint64(typed)), true
	case uint16:
		return jsonPathUnsigned(uint64(typed)), true
	case uint32:
		return jsonPathUnsigned(uint64(typed)), true
	case uint64:
		return jsonPathUnsigned(typed), true
	case float32:
		return jsonPathNum{f: float64(typed)}, true
	case float64:
		return jsonPathNum{f: typed}, true
	}
	return jsonPathNum{}, false
}

func jsonPathUnsigned(u uint64) jsonPathNum {
	if u > math.MaxInt64 {
		return jsonPathNum{f: float64(u)}
	}
	return jsonPathNum{i: int64(u), isInt: true}
}

// jsonPathCompare follows RFC 9535 for absent operands: two absent values are equal,
// an absent value differs from any present value, and ordering comparisons are false.
func jsonPathCompare(op string, left interface{}, leftOk bool, right interface{}, rightOk bool) bool {
	if !leftOk || !rightOk {
		switch op {
		case "==":
			return !leftOk && !rightOk
		case "!=":
			return leftOk != rightOk
		}
		return false
	}

	switch op {
	case "==":
		return jsonPathEqual(left, right)
	case "!=":
		return !jsonPathEqual(left, right)
	}

	var cmp int
	if ln, ok := jsonPathNumber(left); ok {
		rn, ok := jsonPathNumber(right)
		if !ok {
			return false
		}
		if cmp, ok = ln.cmp(rn); !ok {
			return false
		}
	} else if ls, ok := left.(string); ok {
		rs, ok := right.(string)
		if !ok {
			return false
		}
		cmp = strings.Compare(ls, rs)
	} else {
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
	}
	return false
}

func jsonPathEqual(left, right interface{}) bool {
	if ln, ok := jsonPathNumber(left); ok {
		rn, ok := jsonPathNumber(right)
		if !ok {
			return false
		}
		cmp, ok := ln.cmp(rn)
		return ok && cmp == 0
	}

	switch l := left.(type) {
	case nil:
		return right == nil
	case bool:
		r, ok := right.(bool)
		return ok && l == r
	case string:
		r, ok := right.(string)
		return ok && l == r
	case []interface{}:
		r, ok := right.([]interface{})
		if !ok || len(l) != len(r) {
			return false
		}
		for i := range l {
			if !jsonPathEqual(l[i], r[i]) {
				return false
			}
		}
		return true
	}

	leftMap, ok := jsonPathAsMap(left)
	if !ok {
		return false
	}
	rightMap, ok := jsonPathAsMap(right)
	if !ok || leftMap.Len() != rightMap.Len() {
		return false
	}
	equal := true
	leftMap.Iterate(func(k, lv interface{}) {
		if !equal {
			return
		}
		rv, found := rightMap.Get(k)
		equal = found && jsonPathEqual(lv, rv)
	})
	return equal
}

func jsonPathAsMap(val interface{}) (*Map, bool) {
	switch typed := val.(type) {
	case *Map:
		return typed, true
	case map[string]interface{}:
		result := NewMap()
		for k, v := range typed {
			result.Set(k, v)
		}
		return result, true
	case map[interface{}]interface{}:
		result := NewMap()
		for k, v := range typed {
			result.Set(k, v)
		}
		return result, true
	}
	return nil, false
}
